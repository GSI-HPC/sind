// SPDX-License-Identifier: LGPL-3.0-or-later

// Package slurm handles Slurm version discovery and configuration generation.
package slurm

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// DiscoverVersion runs slurmctld -V in an ephemeral container and returns
// the Slurm version string (e.g. "25.11.0").
func DiscoverVersion(ctx context.Context, dc *docker.Client, image string) (string, error) {
	stdout, err := dc.RunEphemeral(ctx, image, "slurmctld", "-V")
	if err != nil {
		return "", fmt.Errorf("running slurmctld -V: %w", err)
	}
	return parseVersion(stdout)
}

// parseVersion extracts the version from slurmctld -V output.
// Expected format: "slurm 25.11.0\n".
func parseVersion(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) != 2 || parts[0] != "slurm" || parts[1] == "" {
		return "", fmt.Errorf("unexpected slurmctld -V output: %q", output)
	}
	return parts[1], nil
}
