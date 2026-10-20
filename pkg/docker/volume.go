// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/csv"
	"strings"
	"time"
)

// VolumeExists returns true if the given volume exists.
func (c *Client) VolumeExists(ctx context.Context, name VolumeName) (bool, error) {
	return c.exists(ctx, "volume", "inspect", string(name))
}

// VolumeLabels returns the labels of the given volume, and whether it
// exists.
func (c *Client) VolumeLabels(ctx context.Context, name VolumeName) (Labels, bool, error) {
	return c.labels(ctx, "volume", "inspect", string(name), "--format", "{{json .Labels}}")
}

// CreateVolume creates a Docker volume.
// Labels are applied as --label flags when non-nil.
func (c *Client) CreateVolume(ctx context.Context, name VolumeName, labels Labels) error {
	args := []string{"volume", "create"}
	args = append(args, SortedLabelFlags(labels)...)
	args = append(args, string(name))
	return c.do(ctx, args...)
}

// RemoveVolume removes a Docker volume, retrying past the dockerd
// async-cleanup race that follows `docker rm -f` (see isVolumeInUse): six
// attempts in all, 100ms apart at first and twice as long after each.
func (c *Client) RemoveVolume(ctx context.Context, name VolumeName) error {
	delay := 100 * time.Millisecond
	for try := 1; ; try++ {
		err := c.do(ctx, "volume", "rm", string(name))
		if try == 6 || !isVolumeInUse(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}

// isVolumeInUse reports whether err indicates the volume is still held by a
// just-removed container. dockerd releases volume mounts asynchronously after
// `docker rm -f`, so a removal that tears down a cluster races the daemon and
// retries this specific error.
func isVolumeInUse(err error) bool {
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
