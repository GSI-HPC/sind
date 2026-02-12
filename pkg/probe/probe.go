// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// CheckContainerRunning verifies that the container is in the "running" state.
func CheckContainerRunning(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return fmt.Errorf("inspecting container: %w", err)
	}
	if info.Status != "running" {
		return fmt.Errorf("container %s is %s, expected running", name, info.Status)
	}
	return nil
}

// CheckSystemdReady verifies that systemd has finished booting.
// Both "running" and "degraded" are considered ready, since degraded means
// systemd completed startup but some units failed (which is expected when
// Slurm daemons haven't been configured yet).
func CheckSystemdReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	// systemctl is-system-running exits non-zero for all states except "running".
	// Wrap in sh so we always get stdout (Client.Exec discards it on error).
	stdout, err := client.Exec(ctx, name, "sh", "-c", "systemctl is-system-running 2>/dev/null || true")
	if err != nil {
		return fmt.Errorf("checking systemd state: %w", err)
	}
	state := strings.TrimSpace(stdout)
	if state != "running" && state != "degraded" {
		return fmt.Errorf("systemd not ready: %s", state)
	}
	return nil
}

// CheckSSHD verifies that sshd is accepting connections and responding with
// the SSH protocol banner on port 22.
func CheckSSHD(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	stdout, err := client.Exec(ctx, name,
		"bash", "-c", "read -t1 line < /dev/tcp/localhost/22 && echo \"$line\"")
	if err != nil {
		return fmt.Errorf("sshd not ready: %w", err)
	}
	banner := strings.TrimSpace(stdout)
	if !strings.HasPrefix(banner, "SSH-") {
		return fmt.Errorf("sshd not ready: unexpected banner %q", banner)
	}
	return nil
}
