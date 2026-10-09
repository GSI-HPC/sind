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
// systemd unit name for munge/sshd/slurmd/slurmdbd/mariadb/sackd/slurmrestd
// and the slurm RPC endpoint name for slurmctld, so it also doubles as the
// user-facing label for each check in status output.
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
	// ServiceSlurmrestd is the REST API daemon of the api node.
	ServiceSlurmrestd Service = "slurmrestd"
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
	case config.RoleAPI:
		return ServiceSlurmrestd, true
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
	// Unit is the systemd unit whose state the check follows, as busctl
	// names it ("munge.service"), or empty. A wait with events does not run
	// a check that has passed again until an event of its unit arrives (see
	// UntilReadyWithEvents).
	Unit string
}

// serviceChecks are the readiness checks of the services sind waits for.
var serviceChecks = map[Service]Func{
	ServiceMunge:     MungeReady,
	ServiceSSHD:      SSHDReady,
	ServiceSlurmctld: SlurmctldReady,
	ServiceSlurmd:    SlurmdReady,
	ServiceSlurmdbd:  SlurmdbdReady,
	ServiceSackd:     SackdReady,
	// Its port check follows the unit too: slurmrestd closes the port only
	// when its unit stops.
	ServiceSlurmrestd: SlurmrestdReady,
}

// ForService returns the readiness probe for a service, which follows the
// service's systemd unit. A service sind does not wait for (mariadb) gets
// a probe without a check.
func ForService(svc Service) Probe {
	check, ok := serviceChecks[svc]
	if !ok {
		return Probe{Name: string(svc)}
	}
	return Probe{Name: string(svc), Check: check, Unit: string(svc) + ".service"}
}

// NodeProbes returns the probes applicable to a node with the given role.
func NodeProbes(role config.Role) []Probe {
	probes := []Probe{
		{Name: "container", Check: ContainerRunning},
		{Name: "systemd", Check: SystemdReady},
		ForService(ServiceSSHD),
	}
	if svc, ok := ServiceForRole(role); ok {
		probes = append(probes, ForService(svc))
	}
	return probes
}

// DefaultInterval is the delay between readiness probe rounds that
// UntilReady and UntilReadyWithEvents use for an interval of zero or less.
const DefaultInterval = 500 * time.Millisecond

// UntilReady polls the given probes until they all pass or the context expires.
// The caller controls the deadline via the context. The interval controls the
// delay between polling attempts; zero or less means DefaultInterval. Each
// round runs the probes in order and stops at the first that fails; as
// nothing tells UntilReady when what a passed probe checks changes, every
// round starts again from the first probe. On timeout, the error includes
// the name and message of the probe that was failing: when the end of the
// wait killed the docker call of the probe that failed last, or of one
// before it that had passed, what the probe that failed last said, not the
// killed call; a probe after it, which the round reached as that one
// passed, with its own error.
func UntilReady(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []Probe, interval time.Duration) error {
	return untilReady(ctx, client, name, probes, interval, nil, "starting readiness probes")
}

// UntilReadyWithEvents is like UntilReady but also listens for events from
// a monitor. An event of the container triggers immediate probe
// re-evaluation instead of waiting for the next poll interval, reducing
// detection latency for event-backed state transitions. The events already
// queued then are taken with it, so that a burst (systemd activates dozens
// of units while it boots) costs one probe round, not one per event.
// Container die events are treated as terminal errors.
//
// A probe that has passed is not run again in later rounds while the
// events can tell that what it checks has changed:
//
//   - an event of its unit (Probe.Unit) runs it again: a unit that became
//     active again, or one that failed, which a unit probe reports as a
//     TerminalError;
//   - another event of the container (start, oom, pause, unpause) runs
//     every passed probe again;
//   - a full events buffer runs every passed probe again, as the watcher
//     drops the events a full subscriber has no room for;
//   - from a monitor error on (monitor.EventMonitorError of the container
//     or of the whole watcher), once events is closed, and for an
//     unbuffered events channel, every round runs every probe, as in
//     UntilReady.
//
// Once events is closed, the wait goes on polling.
func UntilReadyWithEvents(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []Probe, interval time.Duration, events <-chan monitor.Event) error {
	return untilReady(ctx, client, name, probes, interval, events, "starting readiness probes (event-driven)")
}

// untilReady is UntilReady with events nil, UntilReadyWithEvents
// otherwise; it logs msg first.
func untilReady(ctx context.Context, client *docker.Client, name docker.ContainerName, probes []Probe, interval time.Duration, events <-chan monitor.Event, msg string) error {
	log := sindlog.From(ctx)
	ticker := time.NewTicker(orDefault(interval))
	defer ticker.Stop()

	probeNames := make([]string, len(probes))
	for i, p := range probes {
		probeNames[i] = p.Name
	}
	log.DebugContext(ctx, msg, "node", string(name), "probes", strings.Join(probeNames, ","))

	pr := newProgress(probes, events)
	// lastErr is the error of the probe that failed last, the probe at
	// lastAt; -1 for none yet.
	var lastErr error
	lastAt := -1
	for {
		if pr.died != nil {
			return pr.died
		}
		var failed bool
		for i, p := range probes {
			if pr.passed[i] {
				continue
			}
			if err := p.Check(ctx, client, name); err != nil {
				log.Log(ctx, sindlog.LevelTrace, "probe failed", "node", string(name), "probe", p.Name, "err", err)
				failed = true
				var te *TerminalError
				terminal := errors.As(err, &te)
				if ctx.Err() != nil && i <= lastAt && !terminal {
					// The wait ended while the probe ran, which killed
					// its docker call ("signal: killed"): that says
					// nothing of the node. The error of the probe that
					// failed last, this one or one after it, does. A
					// probe after that one reports its own error: the
					// round got past the one that failed, which passed.
					break
				}
				lastErr = fmt.Errorf("probe %s: %w", p.Name, err)
				lastAt = i
				if terminal {
					return fmt.Errorf("node %s not ready: %w", name, lastErr)
				}
				break
			}
			pr.pass(i)
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
				// The tick and queued events can be ready at once, and
				// select picks one at random: take the events first, so
				// that the round runs the probes they concern.
				events = pr.takeQueued(name, events)
			case ev, ok := <-events:
				if !ok {
					events = nil
					pr.distrust()
					continue
				}
				if !pr.take(name, ev, events) {
					continue
				}
			}
			break
		}
	}
}

// concerns reports whether ev concerns the wait for the container name: an
// event of the container, or a monitor error of the whole watcher.
func concerns(name docker.ContainerName, ev monitor.Event) bool {
	return ev.Container == name || ev.Kind == monitor.EventMonitorError && ev.Container == ""
}

// progress remembers which probes of a wait have passed, which later
// rounds skip while the events can tell when what they check changes.
type progress struct {
	probes []Probe
	passed []bool
	// trusted is whether the events tell the wait about changes: they come
	// on a buffered channel, and no monitor has stopped.
	trusted bool
	// died is the error of the wait once a die event of its container has
	// come, which ends it before the next round.
	died error
}

// newProgress returns the progress of a wait on probes with events, none
// passed yet.
func newProgress(probes []Probe, events <-chan monitor.Event) *progress {
	return &progress{
		probes:  probes,
		passed:  make([]bool, len(probes)),
		trusted: events != nil && cap(events) > 0,
	}
}

// pass records that probe i passed: later rounds skip it, if the events
// can tell when it changes.
func (p *progress) pass(i int) {
	p.passed[i] = p.trusted
}

// distrust makes every later round run every probe: the events no longer
// tell what changed.
func (p *progress) distrust() {
	p.trusted = false
	clear(p.passed)
}

// take takes ev, just received from events, and the events already queued
// there, and forgets the passed probes whose state they may have changed.
// It reports whether any of them concerns the wait, and records in died a
// die event of the container among them.
func (p *progress) take(name docker.ContainerName, ev monitor.Event, events <-chan monitor.Event) bool {
	relevant := false
	for {
		// The buffer was full before ev was received: the watcher may have
		// dropped events since the wait last received one.
		if len(events) >= cap(events)-1 {
			clear(p.passed)
		}
		if concerns(name, ev) {
			if ev.Kind == monitor.EventContainerDie {
				p.died = fmt.Errorf("node %s not ready: %w", name, &TerminalError{
					Msg: fmt.Sprintf("container %s died: %s", name, ev.Detail),
				})
				return true
			}
			relevant = true
			p.changed(ev)
		}
		var ok bool
		select {
		case ev, ok = <-events:
			if !ok {
				p.distrust()
				return relevant
			}
		default:
			return relevant
		}
	}
}

// takeQueued takes the events queued on events, as take does, without
// waiting for one. It returns events, or nil once it is closed.
func (p *progress) takeQueued(name docker.ContainerName, events <-chan monitor.Event) <-chan monitor.Event {
	select {
	case ev, ok := <-events:
		if !ok {
			p.distrust()
			return nil
		}
		p.take(name, ev, events)
		return events
	default:
		return events
	}
}

// changed forgets the passed probes whose state ev may have changed: those
// of its unit for a unit event, every probe for another event of the
// container, and from a monitor error on every probe of every round.
func (p *progress) changed(ev monitor.Event) {
	switch {
	case ev.Kind == monitor.EventMonitorError:
		p.distrust()
	case ev.Unit != "":
		for i, probe := range p.probes {
			if probe.Unit == ev.Unit {
				p.passed[i] = false
			}
		}
	default:
		clear(p.passed)
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

// SlurmrestdPort is the TCP port slurmrestd listens on: SLURMRESTD_LISTEN
// of the unit Slurm ships, Slurm's default port for it.
const SlurmrestdPort = "6820"

// SlurmrestdReady verifies that the slurmrestd service is active and
// accepts connections on SlurmrestdPort. The unit is of type simple, so it
// is active as soon as slurmrestd started, before it listens. A failed unit
// is a TerminalError (see UnitActive).
func SlurmrestdReady(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
	if err := UnitActive(ctx, client, name, string(ServiceSlurmrestd)); err != nil {
		return err
	}
	if _, err := client.Exec(ctx, name, "bash", "-c", "exec 3<>/dev/tcp/localhost/"+SlurmrestdPort); err != nil {
		return fmt.Errorf("slurmrestd not listening on port %s: %w", SlurmrestdPort, err)
	}
	return nil
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
