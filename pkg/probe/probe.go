// SPDX-License-Identifier: LGPL-3.0-or-later

// Package probe implements readiness probes for cluster services.
package probe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// Func is a probe function that checks a single readiness condition.
type Func func(ctx context.Context, client *docker.Client, name docker.ContainerName) error

// Probe is a named readiness check.
type Probe struct {
	Name  string
	Check Func
}

// NodeProbes returns the probes applicable to a node with the given role.
func NodeProbes(role config.Role) []Probe {
	probes := []Probe{
		{"container", ContainerRunning},
		{"systemd", SystemdReady},
		{"sshd", SSHDReady},
	}
	switch role {
	case config.RoleController:
		probes = append(probes, Probe{"slurmctld", SlurmctldReady})
	case config.RoleWorker:
		probes = append(probes, Probe{"slurmd", SlurmdReady})
	}
	return probes
}

// UntilReady polls the given probes until they all pass or the context expires.
// The caller controls the deadline via the context. The interval controls the
// delay between polling attempts. On timeout, the error includes the name and
// message of the last failing probe.
func UntilReady(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []Probe, interval time.Duration) error {
	log := sindlog.From(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	probeNames := make([]string, len(probes))
	for i, p := range probes {
		probeNames[i] = p.Name
	}
	log.DebugContext(ctx, "starting readiness probes", "node", string(name), "probes", strings.Join(probeNames, ","))

	var lastErr error
	for {
		var failed bool
		for _, p := range probes {
			if err := p.Check(ctx, client, name); err != nil {
				lastErr = fmt.Errorf("probe %s: %w", p.Name, err)
				log.Log(ctx, sindlog.LevelTrace, "probe failed", "node", string(name), "probe", p.Name, "err", err)
				failed = true
				break
			}
		}
		if !failed {
			log.DebugContext(ctx, "all probes passed", "node", string(name))
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("node %s not ready: %w", name, lastErr)
		case <-ticker.C:
		}
	}
}

// ContainerRunning verifies that the container is in the "running" state.
func ContainerRunning(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return fmt.Errorf("inspecting container: %w", err)
	}
	if info.Status != docker.StateRunning {
		return fmt.Errorf("container %s is %s, expected running", name, info.Status)
	}
	return nil
}

// SystemdReady verifies that systemd has finished booting.
// Both "running" and "degraded" are considered ready, since degraded means
// systemd completed startup but some units failed (which is expected when
// Slurm daemons haven't been configured yet).
func SystemdReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
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

// sshPort is the TCP port used by the SSH daemon.
const sshPort = "22"

// SSHDReady verifies that sshd is accepting connections and responding with
// the SSH protocol banner on port 22.
func SSHDReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	stdout, err := client.Exec(ctx, name,
		"bash", "-c", "read -t1 line < /dev/tcp/localhost/"+sshPort+" && echo \"$line\"")
	if err != nil {
		return fmt.Errorf("sshd not ready: %w", err)
	}
	banner := strings.TrimSpace(stdout)
	if !strings.HasPrefix(banner, "SSH-") {
		return fmt.Errorf("sshd not ready: unexpected banner %q", banner)
	}
	return nil
}

// MungeReady verifies that the munge authentication service is active.
func MungeReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	_, err := client.Exec(ctx, name, "systemctl", "is-active", "munge")
	if err != nil {
		return fmt.Errorf("munge not ready: %w", err)
	}
	return nil
}

// SlurmctldReady verifies that slurmctld is responding to RPC requests.
func SlurmctldReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	_, err := client.Exec(ctx, name, "scontrol", "ping")
	if err != nil {
		return fmt.Errorf("slurmctld not ready: %w", err)
	}
	return nil
}

// SlurmdReady verifies that the slurmd service is active.
func SlurmdReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	_, err := client.Exec(ctx, name, "systemctl", "is-active", "slurmd")
	if err != nil {
		return fmt.Errorf("slurmd not ready: %w", err)
	}
	return nil
}
