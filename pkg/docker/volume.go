// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// VolumeExists returns true if the given volume exists.
func (c *Client) VolumeExists(ctx context.Context, name VolumeName) (bool, error) {
	_, _, err := c.run(ctx, "volume", "inspect", string(name))
	if err != nil {
		// docker volume inspect exits 1 for missing volumes;
		// distinguish from other errors by checking ExitError.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateVolume creates a Docker volume.
func (c *Client) CreateVolume(ctx context.Context, name VolumeName) error {
	_, _, err := c.run(ctx, "volume", "create", string(name))
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
}

// volumeLsEntry maps the docker volume ls --format json output.
type volumeLsEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
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
		})
	}
	return entries, nil
}
