// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"fmt"
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
	_, _, err := c.run(ctx, args...)
	return err
}

// RemoveVolume removes a Docker volume.
func (c *Client) RemoveVolume(ctx context.Context, name VolumeName) error {
	_, _, err := c.run(ctx, "volume", "rm", string(name))
	return err
}

// VolumeListEntry holds summary information from docker volume ls.
type VolumeListEntry struct {
	Name   VolumeName
	Driver string
	Labels Labels
}

// volumeLsEntry maps the docker volume ls --format json output.
type volumeLsEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Labels string `json:"Labels"`
}

// ListVolumes returns volumes matching the given filters.
// Each filter is passed as a --filter flag (e.g. "name=sind-dev").
func (c *Client) ListVolumes(ctx context.Context, filters ...string) ([]VolumeListEntry, error) {
	args := []string{"volume", "ls", "--format", "json"}
	for _, f := range filters {
		args = append(args, "--filter", f)
	}
	stdout, _, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var entries []VolumeListEntry
	for _, line := range strings.Split(stdout, "\n") {
		var v volumeLsEntry
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("parsing volume ls output: %w", err)
		}
		entries = append(entries, VolumeListEntry{
			Name:   VolumeName(v.Name),
			Driver: v.Driver,
			Labels: parseLabels(v.Labels),
		})
	}
	return entries, nil
}
