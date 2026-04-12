// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/monitor"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testContainer docker.ContainerName = "sind-dev-controller"

func inspectJSON(status string) string {
	return inspectJSONFull(status, 0, false)
}

func inspectJSONFull(status string, exitCode int, oomKilled bool) string {
	return fmt.Sprintf(`[{
  "Id": "abc123",
  "Name": "/%s",
  "State": {"Status": %q, "ExitCode": %d, "OOMKilled": %v},
  "Config": {"Labels": {}},
  "NetworkSettings": {"Networks": {}}
}]`, testContainer, status, exitCode, oomKilled)
}

func TestContainerRunning(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	err := ContainerRunning(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"inspect", string(testContainer)}, m.Calls[0].Args)
}

func TestContainerRunning_NotRunning(t *testing.T) {
	for _, status := range []string{"created", "paused"} {
		t.Run(status, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(inspectJSON(status), "", nil)
			c := docker.NewClient(&m)

			err := ContainerRunning(t.Context(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), status)
			assert.Contains(t, err.Error(), "expected running")
		})
	}
}

func TestContainerRunning_Terminal(t *testing.T) {
	for _, status := range []string{"exited", "dead"} {
		t.Run(status, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(inspectJSON(status), "", nil)
			c := docker.NewClient(&m)

			err := ContainerRunning(t.Context(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), status)

			var te *TerminalError
			assert.ErrorAs(t, err, &te)
		})
	}
}

func TestContainerRunning_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := ContainerRunning(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting container")
}

func TestSystemdReady_Running(t *testing.T) {
	var m mock.Executor
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	err := SystemdReady(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "sh", "-c", "systemctl is-system-running 2>/dev/null || true"},
		m.Calls[0].Args)
}

func TestSystemdReady_Degraded(t *testing.T) {
	var m mock.Executor
	// The sh wrapper always exits 0, so stdout contains "degraded".
	m.AddResult("degraded\n", "", nil)
	c := docker.NewClient(&m)

	err := SystemdReady(t.Context(), c, testContainer)
	require.NoError(t, err)
}

func TestSystemdReady_NotReady(t *testing.T) {
	for _, state := range []string{"starting", "initializing", "stopping"} {
		t.Run(state, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(state+"\n", "", nil)
			c := docker.NewClient(&m)

			err := SystemdReady(t.Context(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), state)
		})
	}
}

func TestSystemdReady_ExecError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)

	err := SystemdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking systemd state")
}

func TestSSHDReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("SSH-2.0-OpenSSH_9.8\n", "", nil)
	c := docker.NewClient(&m)

	err := SSHDReady(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "bash", "-c", "read -t1 line < /dev/tcp/localhost/22 && echo \"$line\""},
		m.Calls[0].Args)
}

func TestSSHDReady_UnexpectedBanner(t *testing.T) {
	var m mock.Executor
	m.AddResult("HTTP/1.1 400 Bad Request\n", "", nil)
	c := docker.NewClient(&m)

	err := SSHDReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected banner")
}

func TestSSHDReady_EmptyBanner(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	err := SSHDReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected banner")
}

func TestSSHDReady_ConnectionRefused(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SSHDReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sshd not ready")
}

func TestSlurmctldReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("Slurmctld(primary) at controller is UP\n", "", nil)
	c := docker.NewClient(&m)

	err := SlurmctldReady(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "scontrol", "ping"}, m.Calls[0].Args)
}

func TestSlurmctldReady_NotReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "slurm_persist_conn_open_without_init: failed to open persistent connection\n",
		fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SlurmctldReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmctld not ready")
}

func TestMungeReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\n", "", nil)
	c := docker.NewClient(&m)

	err := MungeReady(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "munge"}, m.Calls[0].Args)
}

func TestMungeReady_NotReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := MungeReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "munge not ready")
}

func TestSlurmdReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\n", "", nil)
	c := docker.NewClient(&m)

	err := SlurmdReady(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "slurmd"}, m.Calls[0].Args)
}

func TestSlurmdReady_NotReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SlurmdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmd not ready")
}

func TestMariadbReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("1\n", "", nil)
	c := docker.NewClient(&m)

	err := MariadbReady(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "mysql", "-e", "SELECT 1"}, m.Calls[0].Args)
}

func TestMariadbReady_NotReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := MariadbReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mariadb not ready")
}

func TestSlurmdbdReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\n", "", nil)
	c := docker.NewClient(&m)

	err := SlurmdbdReady(t.Context(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "slurmdbd"}, m.Calls[0].Args)
}

func TestSlurmdbdReady_NotReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SlurmdbdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmdbd not ready")
}

func TestForService(t *testing.T) {
	p := ForService(slurm.Slurmctld)
	assert.Equal(t, "slurmctld", p.Name)
	assert.NotNil(t, p.Check)

	p = ForService(slurm.Slurmdbd)
	assert.Equal(t, "slurmdbd", p.Name)
	assert.NotNil(t, p.Check)

	p = ForService(slurm.Slurmd)
	assert.Equal(t, "slurmd", p.Name)
	assert.NotNil(t, p.Check)

	p = ForService(slurm.Mariadb)
	assert.Equal(t, "mariadb", p.Name)
	assert.NotNil(t, p.Check)

	p = ForService("unknown")
	assert.Equal(t, "unknown", p.Name)
	assert.Nil(t, p.Check)
}

func TestNodeProbes(t *testing.T) {
	tests := []struct {
		role  config.Role
		names []string
	}{
		{config.RoleController, []string{"container", "systemd", "sshd", "slurmctld"}},
		{config.RoleDb, []string{"container", "systemd", "sshd", "mariadb", "slurmdbd"}},
		{config.RoleWorker, []string{"container", "systemd", "sshd", "slurmd"}},
		{config.RoleSubmitter, []string{"container", "systemd", "sshd"}},
		{"unknown", []string{"container", "systemd", "sshd"}},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			probes := NodeProbes(tt.role)
			var names []string
			for _, p := range probes {
				names = append(names, p.Name)
			}
			assert.Equal(t, tt.names, names)
		})
	}
}

func TestUntilReady_AllPass(t *testing.T) {
	var m mock.Executor
	// Single probe that succeeds immediately.
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 1)
}

func TestUntilReady_RetryThenPass(t *testing.T) {
	var m mock.Executor
	// First attempt: not running. Second attempt: running.
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
}

func TestUntilReady_Timeout(t *testing.T) {
	var m mock.Executor
	// Always fail — queue enough results to cover polling attempts.
	for i := 0; i < 100; i++ {
		m.AddResult(inspectJSON("created"), "", nil)
	}
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
	assert.Contains(t, err.Error(), "probe container")
}

func TestUntilReady_MultipleProbes(t *testing.T) {
	var m mock.Executor
	// Two probes, both pass on first attempt.
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	probes := []Probe{
		{"container", ContainerRunning},
		{"systemd", SystemdReady},
	}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
}

func TestUntilReady_SecondProbeFails(t *testing.T) {
	var m mock.Executor
	// First attempt: container OK, systemd not ready.
	// Second attempt: container OK, systemd ready.
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("starting\n", "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	probes := []Probe{
		{"container", ContainerRunning},
		{"systemd", SystemdReady},
	}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 4)
}

func TestUntilReady_TimeoutSecondProbe(t *testing.T) {
	var m mock.Executor
	// Container always passes, systemd always fails.
	for i := 0; i < 100; i++ {
		m.AddResult(inspectJSON("running"), "", nil)
		m.AddResult("starting\n", "", nil)
	}
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	probes := []Probe{
		{"container", ContainerRunning},
		{"systemd", SystemdReady},
	}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
	assert.Contains(t, err.Error(), "probe systemd")
}

func TestUntilReady_EmptyProbes(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)

	err := UntilReady(t.Context(), c, testContainer, nil, time.Millisecond)
	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestUntilReady_ContextCanceled(t *testing.T) {
	var m mock.Executor
	for i := 0; i < 100; i++ {
		m.AddResult(inspectJSON("created"), "", nil)
	}
	c := docker.NewClient(&m)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already canceled

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}

func TestUntilReady_TerminalError(t *testing.T) {
	var m mock.Executor
	// Container exited — should fail immediately without retrying.
	m.AddResult(inspectJSON("exited"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
	assert.Contains(t, err.Error(), "exited")
	// Only one call — no retries for terminal state.
	assert.Len(t, m.Calls, 1)
}

func TestContainerRunning_OOMKilled(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSONFull("exited", 137, true), "", nil)
	c := docker.NewClient(&m)

	err := ContainerRunning(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OOM killed")
	assert.Contains(t, err.Error(), "137")

	var te *TerminalError
	assert.ErrorAs(t, err, &te)
}

func TestUntilReadyWithEvents_AllPass(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	events := make(chan monitor.Event, 1)
	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Millisecond, events)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 1)
}

func TestUntilReadyWithEvents_EventTriggersImmediateCheck(t *testing.T) {
	var m mock.Executor
	// First attempt: not running. Second attempt (triggered by event): running.
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	events := make(chan monitor.Event, 1)
	// Pre-load an event so the select picks it up instead of waiting for ticker.
	events <- monitor.Event{Kind: monitor.EventContainerStart, Node: "controller", Container: testContainer}

	probes := []Probe{{"container", ContainerRunning}}
	// Use a very long interval so only the event can trigger the re-check.
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Minute, events)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
}

func TestUntilReadyWithEvents_ContainerDie(t *testing.T) {
	var m mock.Executor
	// First attempt: not running.
	m.AddResult(inspectJSON("created"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	events := make(chan monitor.Event, 1)
	events <- monitor.Event{
		Kind:      monitor.EventContainerDie,
		Node:      "controller",
		Container: testContainer,
		Detail:    "exitCode=1",
	}

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Minute, events)
	require.Error(t, err)

	var te *TerminalError
	assert.ErrorAs(t, err, &te)
	assert.Contains(t, err.Error(), "not ready")
	assert.Contains(t, err.Error(), "died")
}

func TestUntilReadyWithEvents_IgnoresOtherContainerEvents(t *testing.T) {
	var m mock.Executor
	// First attempt: not running. Second attempt (after ticker): running.
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	events := make(chan monitor.Event, 1)
	// Event for a different container — should be ignored.
	events <- monitor.Event{
		Kind:      monitor.EventContainerStart,
		Node:      "worker-0",
		Container: "sind-dev-worker-0",
	}

	probes := []Probe{{"container", ContainerRunning}}
	// Short interval so the ticker fires after the ignored event.
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Millisecond, events)
	require.NoError(t, err)
}

func TestUntilReadyWithEvents_ContextCanceled(t *testing.T) {
	var m mock.Executor
	for range 100 {
		m.AddResult(inspectJSON("created"), "", nil)
	}
	c := docker.NewClient(&m)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already cancelled

	events := make(chan monitor.Event)
	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Millisecond, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}

func TestUntilReadyWithEvents_Timeout(t *testing.T) {
	var m mock.Executor
	for range 100 {
		m.AddResult(inspectJSON("created"), "", nil)
	}
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	events := make(chan monitor.Event)
	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Millisecond, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}

func TestUntilReadyWithEvents_TerminalError(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSON("exited"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	events := make(chan monitor.Event)
	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Millisecond, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited")
	assert.Len(t, m.Calls, 1)
}
