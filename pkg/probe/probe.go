// SPDX-License-Identifier: LGPL-3.0-or-later

// Package probe implements readiness probes for cluster services.
package probe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/monitor"
)

// Service identifies a per-node readiness check. The string value is the
// systemd unit name for munge/sshd/slurmd and the slurm RPC endpoint name
// for slurmctld, so it also doubles as the user-facing label for each
// check in status output.
type Service string

// Per-node readiness services managed by sind.
const (
	ServiceMunge     Service = "munge"
	ServiceSSHD      Service = "sshd"
	ServiceSlurmctld Service = "slurmctld"
	ServiceSlurmd    Service = "slurmd"
)

// ServiceForRole returns the Slurm readiness-check service associated with
// a node role. Returns empty string and false for roles with no Slurm
// service (e.g. submitter).
func ServiceForRole(role config.Role) (Service, bool) {
	switch role {
	case config.RoleController:
		return ServiceSlurmctld, true
	case config.RoleWorker:
		return ServiceSlurmd, true
	default:
		return "", false
	}
}

// TerminalError indicates a probe failure that cannot be recovered by
// retrying. For example, a container in "exited" or "dead" state will
// never become "running" on its own.
type TerminalError struct {
	Msg string
}

func (e *TerminalError) Error() string { return e.Msg }

// Func is a probe function that checks a single readiness condition.
type Func func(ctx context.Context, client *docker.Client, name docker.ContainerName) error

// Probe is a named readiness check.
type Probe struct {
	Name  string
	Check Func
}

// ForService returns the readiness probe for a Slurm daemon service.
func ForService(svc Service) Probe {
	switch svc {
	case ServiceSlurmctld:
		return Probe{Name: string(svc), Check: SlurmctldReady}
	case ServiceSlurmd:
		return Probe{Name: string(svc), Check: SlurmdReady}
	default:
		return Probe{Name: string(svc)}
	}
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
				var te *TerminalError
				if errors.As(err, &te) {
					return fmt.Errorf("node %s not ready: %w", name, lastErr)
				}
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

// UntilReadyWithEvents is like UntilReady but also listens for events from
// a monitor. Events trigger immediate probe re-evaluation instead of waiting
// for the next poll interval, reducing detection latency for event-backed
// state transitions. Container die events are treated as terminal errors.
func UntilReadyWithEvents(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []Probe, interval time.Duration, events <-chan monitor.Event) error {
	log := sindlog.From(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	probeNames := make([]string, len(probes))
	for i, p := range probes {
		probeNames[i] = p.Name
	}
	log.DebugContext(ctx, "starting readiness probes (event-driven)", "node", string(name), "probes", strings.Join(probeNames, ","))

	var lastErr error
	for {
		var failed bool
		for _, p := range probes {
			if err := p.Check(ctx, client, name); err != nil {
				lastErr = fmt.Errorf("probe %s: %w", p.Name, err)
				log.Log(ctx, sindlog.LevelTrace, "probe failed", "node", string(name), "probe", p.Name, "err", err)
				failed = true
				var te *TerminalError
				if errors.As(err, &te) {
					return fmt.Errorf("node %s not ready: %w", name, lastErr)
				}
				break
			}
		}
		if !failed {
			log.DebugContext(ctx, "all probes passed", "node", string(name))
			return nil
		}
		for {
			select {
			case <-ctx.Done():
				return fmt.Errorf("node %s not ready: %w", name, lastErr)
			case <-ticker.C:
			case ev := <-events:
				if ev.Kind == monitor.EventContainerDie && ev.Container == name {
					return fmt.Errorf("node %s not ready: %w", name, &TerminalError{
						Msg: fmt.Sprintf("container %s died: %s", name, ev.Detail),
					})
				}
				if ev.Container != name {
					continue
				}
			}
			break
		}
	}
}

// ContainerRunning verifies that the container is in the "running" state.
// Returns a TerminalError for states that cannot recover (exited, dead).
func ContainerRunning(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return fmt.Errorf("inspecting container: %w", err)
	}
	if info.Status == docker.StateExited || info.Status == docker.StateDead {
		msg := fmt.Sprintf("container %s is %s (exit code %d)", name, info.Status, info.ExitCode)
		if info.OOMKilled {
			msg += " (OOM killed)"
		}
		return &TerminalError{Msg: msg}
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

// Snapshot returns a one-shot readiness snapshot of a running node, fusing
// the systemd-based checks (munge, sshd, and on workers slurmd) into a single
// docker exec. Controllers additionally run scontrol ping because
// "slurmctld is active" is weaker than "slurmctld answers RPCs" — the unit
// can be active during startup while RPCs still fail.
//
// Snapshot is intended for status-query call sites such as cluster.GetStatus.
// Unlike the individual *Ready probes, it does not surface per-probe errors;
// a failing check simply maps to false. Callers that need retry granularity
// (e.g. cluster-create readiness polling) should keep using NodeProbes and
// UntilReady.
//
// The container must be running. Non-exit errors (daemon unreachable, etc.)
// are propagated; a non-zero exit from systemctl (at least one unit
// inactive) is expected and parsed normally.
func Snapshot(ctx context.Context, client *docker.Client, name docker.ContainerName, role config.Role) (map[Service]bool, error) {
	// Build the systemctl unit list for this role. sshd and munge are
	// universal; slurmd is added for workers only.
	units := []Service{ServiceMunge, ServiceSSHD}
	if role == config.RoleWorker {
		units = append(units, ServiceSlurmd)
	}

	args := append([]string{"systemctl", "is-active"}, serviceStrings(units)...)
	stdout, err := client.ExecAllowNonZero(ctx, name, args...)
	if err != nil {
		return nil, fmt.Errorf("systemctl is-active: %w", err)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != len(units) {
		return nil, fmt.Errorf("systemctl is-active: got %d lines, want %d (stdout=%q)",
			len(lines), len(units), stdout)
	}

	result := make(map[Service]bool, len(units)+1)
	for i, u := range units {
		result[u] = strings.TrimSpace(lines[i]) == "active"
	}

	// Controllers need an additional RPC-level check for slurmctld.
	if role == config.RoleController {
		result[ServiceSlurmctld] = SlurmctldReady(ctx, client, name) == nil
	}

	return result, nil
}

// serviceStrings converts a slice of Service values to plain strings for
// passing to exec argv.
func serviceStrings(svcs []Service) []string {
	out := make([]string, len(svcs))
	for i, s := range svcs {
		out[i] = string(s)
	}
	return out
}
