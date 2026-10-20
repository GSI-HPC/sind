// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/csv"
	"strings"
)

// VolumeExists returns true if the given volume exists.
func (c *Client) VolumeExists(ctx context.Context, name VolumeName) (bool, error) {
	return c.exists(ctx, "volume", "inspect", string(name))
}

// CreateVolume creates a Docker volume.
// Labels are applied as --label flags when non-nil.
func (c *Client) CreateVolume(ctx context.Context, name VolumeName, labels Labels) error {
	args := []string{"volume", "create"}
	args = append(args, SortedLabelFlags(labels)...)
	args = append(args, string(name))
	return c.do(ctx, args...)
}

// RemoveVolume removes a Docker volume.
func (c *Client) RemoveVolume(ctx context.Context, name VolumeName) error {
	return c.do(ctx, "volume", "rm", string(name))
}

// IsVolumeInUse reports whether err indicates the volume is still held by a
// just-removed container. dockerd releases volume mounts asynchronously after
// `docker rm -f`, so callers tearing down a cluster race the daemon and can
// retry this specific error.
func IsVolumeInUse(err error) bool {
	return err != nil && strings.Contains(err.Error(), "volume is in use")
}

// VolumeListEntry holds summary information from docker volume ls.
type VolumeListEntry struct {
	Name   VolumeName
	Driver string
	Labels Labels
}

// ListVolumes returns volumes matching the given filters.
// Each filter is passed as a --filter flag (e.g. "name=sind-dev").
func (c *Client) ListVolumes(ctx context.Context, filters ...string) ([]VolumeListEntry, error) {
	ls, err := listJSON[lsEntry](ctx, c, "volume ls", filters, "volume", "ls")
	if err != nil {
		return nil, err
	}
	var entries []VolumeListEntry
	for _, v := range ls {
		entries = append(entries, VolumeListEntry{
			Name:   VolumeName(v.Name),
			Driver: v.Driver,
			Labels: parseLabels(v.Labels),
		})
	}
	return entries, nil
}

// VolumeMount returns the --mount value that mounts the volume source at
// target: with subpath, only that directory of the volume, which must exist
// when the container starts (volume-subpath, Docker 26 and later, which
// -v cannot express), read-only with readOnly, and with noCopy without
// Docker copying the image's files at target into an empty volume. Docker
// reads the value as a CSV record, so a field with a comma or a quote is
// quoted.
func VolumeMount(source VolumeName, target, subpath string, readOnly, noCopy bool) string {
	fields := []string{"type=volume", "source=" + string(source), "target=" + target}
	if subpath != "" {
		fields = append(fields, "volume-subpath="+subpath)
	}
	if readOnly {
		fields = append(fields, "readonly")
	}
	if noCopy {
		fields = append(fields, "volume-nocopy")
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	// Writing to a strings.Builder cannot fail.
	_ = w.Write(fields)
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}
