// SPDX-License-Identifier: LGPL-3.0-or-later

// Package slurm handles Slurm version discovery and configuration generation.
package slurm

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// DiscoverVersion runs scontrol --version in an ephemeral container and returns
// the Slurm version string (e.g. "25.11.0").
func DiscoverVersion(ctx context.Context, dc *docker.Client, image string) (string, error) {
	stdout, err := dc.RunEphemeral(ctx, image, "scontrol", "--version")
	if err != nil {
		return "", fmt.Errorf("running scontrol --version: %w", err)
	}
	return parseVersion(stdout)
}

// parseVersion extracts the version from scontrol --version output.
// Expected format: "slurm 25.11.0\n".
func parseVersion(output string) (string, error) {
	trimmed := strings.TrimSpace(output)
	parts := strings.SplitN(trimmed, " ", 2)
	if len(parts) != 2 || parts[0] != "slurm" || parts[1] == "" {
		return "", fmt.Errorf("unexpected scontrol --version output: %q", output)
	}
	return parts[1], nil
}
