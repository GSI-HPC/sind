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
// systemd unit name for munge/sshd/slurmd/slurmdbd/mariadb and the slurm RPC
// endpoint name for slurmctld, so it also doubles as the user-facing label
// for each check in status output.
type Service string

// Per-node readiness services managed by sind.
const (
	ServiceMunge     Service = "munge"
	ServiceSSHD      Service = "sshd"
	ServiceSlurmctld Service = "slurmctld"
	ServiceSlurmd    Service = "slurmd"
	ServiceSlurmdbd  Service = "slurmdbd"
	ServiceMariadb   Service = "mariadb"
	// ServiceSackd is the auth/slurm token daemon of a login node.
	ServiceSackd Service = "sackd"
)

// ServiceForRole returns the Slurm readiness-check service associated with
// a node role. Returns empty string and false for roles with no Slurm
// service (e.g. submitter).
func ServiceForRole(role config.Role) (Service, bool) {
	switch role {
	case config.RoleController:
		return ServiceSlurmctld, true
	case config.RoleDB:
		return ServiceSlurmdbd, true
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
	case ServiceSlurmdbd:
		return Probe{Name: string(svc), Check: SlurmdbdReady}
	case ServiceSackd:
		return Probe{Name: string(svc), Check: SackdReady}
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
	case config.RoleDB:
		probes = append(probes, Probe{"slurmdbd", SlurmdbdReady})
	case config.RoleWorker:
		probes = append(probes, Probe{"slurmd", SlurmdReady})
	}
	return probes
}

// DefaultInterval is the delay between readiness probe rounds that
// UntilReady and UntilReadyWithEvents use for an interval of zero or less.
const DefaultInterval = 500 * time.Millisecond

// UntilReady polls the given probes until they all pass or the context expires.
// The caller controls the deadline via the context. The interval controls the
// delay between polling attempts; zero or less means DefaultInterval. On
// timeout, the error includes the name and message of the last failing probe.
func UntilReady(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []Probe, interval time.Duration) error {
	log := sindlog.From(ctx)
	ticker := time.NewTicker(orDefault(interval))
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
			return waitEnded(ctx, name, lastErr)
		case <-ticker.C:
		}
	}
}

// UntilReadyWithEvents is like UntilReady but also listens for events from
// a monitor. An event of the container triggers immediate probe
// re-evaluation instead of waiting for the next poll interval, reducing
// detection latency for event-backed state transitions. The events already
// queued then are taken with it, so that a burst (systemd activates dozens
// of units while it boots) costs one probe round, not one per event.
// Container die events are treated as terminal errors. Once events is
// closed, the wait goes on polling.
func UntilReadyWithEvents(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []Probe, interval time.Duration, events <-chan monitor.Event) error {
	log := sindlog.From(ctx)
	ticker := time.NewTicker(orDefault(interval))
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
				return waitEnded(ctx, name, lastErr)
			case <-ticker.C:
			case ev, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if ev.Container != name {
					continue
				}
				if err := takeQueued(name, ev, events); err != nil {
					return err
				}
			}
			break
		}
	}
}

// takeQueued takes ev and the events already queued on events. It returns
// the error for a die event of the container among them.
func takeQueued(name docker.ContainerName, ev monitor.Event, events <-chan monitor.Event) error {
	for {
		if ev.Kind == monitor.EventContainerDie && ev.Container == name {
			return fmt.Errorf("node %s not ready: %w", name, &TerminalError{
				Msg: fmt.Sprintf("container %s died: %s", name, ev.Detail),
			})
		}
		var ok bool
		select {
		case ev, ok = <-events:
			if !ok {
				return nil
			}
		default:
			return nil
		}
	}
}

// orDefault returns interval, or DefaultInterval when it is not positive
// (time.NewTicker panics on that).
func orDefault(interval time.Duration) time.Duration {
	if interval <= 0 {
		return DefaultInterval
	}
	return interval
}

// waitEnded is the error of a readiness wait that its context ended: an
// interrupt or a timeout, not a failed node. It wraps ctx.Err() so callers
// can tell the two apart with errors.Is, and keeps the last probe failure,
// if there was one, to say what the node was still waiting for.
func waitEnded(ctx context.Context, name docker.ContainerName, lastErr error) error {
	if lastErr == nil {
		return fmt.Errorf("node %s not ready: %w", name, ctx.Err())
	}
	return fmt.Errorf("node %s not ready: %w; last probe error: %w", name, ctx.Err(), lastErr)
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

// MungeReady verifies that the munge authentication service is active. A
// failed unit is a TerminalError (see UnitActive).
func MungeReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	return UnitActive(ctx, client, name, string(ServiceMunge))
}

// SlurmctldReady verifies that the slurmctld on the given controller node is
// responding to RPC requests. While it does not, a failed slurmctld unit
// (e.g. a slurm.conf line slurmctld rejects) is a TerminalError carrying the
// tail of the unit's journal.
//
// scontrol ping exits zero as soon as any configured controller answers, so
// with a backup controller the exit code alone cannot tell which one is up.
// The per-controller lines ("Slurmctld(primary) at controller is UP") are
// matched against the container: sind names containers
// <realm>-<cluster>-<hostname>, so the line whose host is the container
// name's suffix belongs to this node. When no line matches (e.g. a
// user-supplied slurm.conf with other hostnames) the exit code decides.
func SlurmctldReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	err := slurmctldPing(ctx, client, name)
	if err == nil {
		return nil
	}
	if failed := unitFailed(ctx, client, name, string(ServiceSlurmctld)); failed != nil {
		return failed
	}
	return err
}

// slurmctldPing is the scontrol ping check of SlurmctldReady.
func slurmctldPing(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	stdout, err := client.Exec(ctx, name, "scontrol", "ping")
	if err != nil {
		return fmt.Errorf("slurmctld not ready: %w", err)
	}
	for host, up := range ParseSlurmctldPing(stdout) {
		if !strings.HasSuffix(string(name), "-"+host) {
			continue
		}
		if !up {
			return fmt.Errorf("slurmctld not ready: %s is DOWN", host)
		}
		return nil
	}
	return nil
}

// ParseSlurmctldPing parses scontrol ping output into a map from controller
// hostname to whether it answered. Lines not in the
// "Slurmctld(<mode>) at <host> is <UP|DOWN>" format are ignored.
func ParseSlurmctldPing(stdout string) map[string]bool {
	result := make(map[string]bool)
	for line := range strings.Lines(stdout) {
		fields := strings.Fields(line)
		if len(fields) != 5 || !strings.HasPrefix(fields[0], "Slurmctld(") ||
			fields[1] != "at" || fields[3] != "is" {
			continue
		}
		result[fields[2]] = fields[4] == "UP"
	}
	return result
}

// SlurmdReady verifies that the slurmd service is active. A failed unit is
// a TerminalError (see UnitActive).
func SlurmdReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	return UnitActive(ctx, client, name, string(ServiceSlurmd))
}

// ClusterRegistered returns a check that passes once slurmdbd lists the
// cluster, which slurmctld registers when it first starts with accounting.
// Until then, sacctmgr refuses to add users. The registration can land
// after slurmctld answers pings.
func ClusterRegistered(clusterName string) Func {
	return func(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
		stdout, err := client.Exec(ctx, name, "sacctmgr", "-n", "-P", "show", "cluster", "format=cluster")
		if err != nil {
			return fmt.Errorf("listing clusters: %w", err)
		}
		for line := range strings.Lines(stdout) {
			if strings.TrimSpace(line) == clusterName {
				return nil
			}
		}
		return fmt.Errorf("cluster %s not registered with slurmdbd yet", clusterName)
	}
}

// SackdReady verifies that the sackd service is active. A failed unit is a
// TerminalError (see UnitActive).
func SackdReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	return UnitActive(ctx, client, name, string(ServiceSackd))
}

// journalLines is the number of journal lines UnitJournal returns.
const journalLines = "20"

// UnitJournal returns the tail of a systemd unit's journal on the node, to
// show why the unit failed. It is best effort: on error it returns what
// journalctl printed, possibly nothing.
func UnitJournal(ctx context.Context, client *docker.Client, name docker.ContainerName, unit string) string {
	journal, _ := client.ExecAllowNonZero(ctx, name,
		"journalctl", "-u", unit, "-n", journalLines, "--no-pager", "-o", "cat")
	return strings.TrimSpace(journal)
}

// SlurmdbdReady verifies that the slurmdbd service is active. A failed unit
// is a TerminalError (see UnitActive).
func SlurmdbdReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	return UnitActive(ctx, client, name, string(ServiceSlurmdbd))
}

// UnitActive verifies that a systemd unit on the node is active. A failed
// unit does not recover on its own (a unit that systemd restarts is
// "activating" meanwhile), so it is reported as a TerminalError carrying
// the tail of the unit's journal to show why the unit stopped.
func UnitActive(ctx context.Context, client *docker.Client, name docker.ContainerName, unit string) error {
	stdout, err := client.ExecAllowNonZero(ctx, name, "systemctl", "is-active", unit)
	if err != nil {
		return fmt.Errorf("%s not ready: %w", unit, err)
	}
	switch state := strings.TrimSpace(stdout); state {
	case "active":
		return nil
	case "failed":
		return failedUnit(ctx, client, name, unit)
	default:
		return fmt.Errorf("%s not ready: %s", unit, state)
	}
}

// unitFailed returns the TerminalError for a failed unit, or nil when the
// unit has not failed or its state cannot be read.
func unitFailed(ctx context.Context, client *docker.Client, name docker.ContainerName, unit string) error {
	stdout, err := client.ExecAllowNonZero(ctx, name, "systemctl", "is-active", unit)
	if err != nil || strings.TrimSpace(stdout) != "failed" {
		return nil
	}
	return failedUnit(ctx, client, name, unit)
}

// failedUnit is the TerminalError for a failed unit, with its journal tail.
func failedUnit(ctx context.Context, client *docker.Client, name docker.ContainerName, unit string) error {
	return &TerminalError{Msg: unit + " failed:\n" + UnitJournal(ctx, client, name, unit)}
}

// Snapshot returns a one-shot readiness snapshot of the given services on a
// running node, fusing the systemd-based checks (munge, sshd, slurmd,
// mariadb, slurmdbd) into a single docker exec. slurmctld is checked with
// scontrol ping instead, because "slurmctld is active" is weaker than
// "slurmctld answers RPCs" — the unit can be active during startup while RPCs
// still fail.
//
// Snapshot is intended for status-query call sites such as cluster.GetStatus.
// Unlike the individual *Ready probes, it does not surface per-probe errors;
// a failing check simply maps to false. Callers that need retry granularity
// (e.g. cluster-create readiness polling) should keep using the individual
// probes and UntilReady.
//
// The container must be running. Non-exit errors (daemon unreachable, etc.)
// are propagated; a non-zero exit from systemctl (at least one unit
// inactive) is expected and parsed normally.
func Snapshot(ctx context.Context, client *docker.Client, name docker.ContainerName, services []Service) (map[Service]bool, error) {
	var units []Service
	var slurmctld bool
	for _, svc := range services {
		if svc == ServiceSlurmctld {
			slurmctld = true
			continue
		}
		units = append(units, svc)
	}

	result := make(map[Service]bool, len(services))
	if len(units) > 0 {
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
		for i, u := range units {
			result[u] = strings.TrimSpace(lines[i]) == "active"
		}
	}

	if slurmctld {
		result[ServiceSlurmctld] = slurmctldPing(ctx, client, name) == nil
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
