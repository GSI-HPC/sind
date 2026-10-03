// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testContainer docker.ContainerName = "sind-dev-controller"

// exitCode1 runs a command that exits with code 1 and returns its
// ProcessState so tests can construct a realistic *exec.ExitError.
func exitCode1(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ProcessState
}

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
	m.AddResult("activating\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
	c := docker.NewClient(&m)

	err := SlurmctldReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmctld not ready")
	var te *TerminalError
	assert.NotErrorAs(t, err, &te)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "slurmctld"}, m.Calls[1].Args)
}

func TestSlurmctldReady_UnitStateUnknown(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	m.AddResult("", "", fmt.Errorf("daemon unreachable"))
	c := docker.NewClient(&m)

	err := SlurmctldReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Equal(t, "slurmctld not ready: exit status 1", err.Error())
}

func TestSlurmctldReady_Failed(t *testing.T) {
	// slurmctld exited after systemctl enable --now returned, e.g. on a
	// slurm.conf line it rejects: the wait ends at once with its journal.
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	m.AddResult("failed\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("slurmctld: fatal: Unable to process configuration file\n", "", nil)
	c := docker.NewClient(&m)

	err := SlurmctldReady(t.Context(), c, testContainer)
	var te *TerminalError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "slurmctld failed:\nslurmctld: fatal: Unable to process configuration file", te.Msg)
}

func TestSlurmctldReady_BackupController(t *testing.T) {
	pingOut := "Slurmctld(primary) at controller is DOWN\nSlurmctld(backup) at controller-backup is UP\n"
	tests := []struct {
		name      string
		container docker.ContainerName
		wantErr   string
	}{
		{name: "primary down", container: "sind-dev-controller", wantErr: "controller is DOWN"},
		{name: "backup up", container: "sind-dev-controller-backup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(pingOut, "", nil)
			m.AddResult("active\n", "", nil)
			c := docker.NewClient(&m)

			err := SlurmctldReady(t.Context(), c, tt.container)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSlurmctldReady_NoMatchingLine(t *testing.T) {
	var m mock.Executor
	m.AddResult("Slurmctld(primary) at ctl0 is UP\n", "", nil)
	c := docker.NewClient(&m)

	require.NoError(t, SlurmctldReady(t.Context(), c, testContainer))
}

func TestParseSlurmctldPing(t *testing.T) {
	out := "Slurmctld(primary) at controller is UP\n" +
		"Slurmctld(backup) at controller-backup is DOWN\n" +
		"*****************************************\n" +
		"** RESTORE SLURMCTLD DAEMON TO SERVICE **\n"
	assert.Equal(t, map[string]bool{"controller": true, "controller-backup": false}, ParseSlurmctldPing(out))
	assert.Empty(t, ParseSlurmctldPing(""))
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

func TestSackdReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\n", "", nil)
	m.AddResult("", "", fmt.Errorf("exit status 3"))
	c := docker.NewClient(&m)

	require.NoError(t, SackdReady(t.Context(), c, testContainer))
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "sackd"}, m.Calls[0].Args)

	err := SackdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Equal(t, "sackd not ready: exit status 3", err.Error())

	p := ForService(ServiceSackd)
	assert.Equal(t, "sackd", p.Name)
	assert.NotNil(t, p.Check)
}

func TestClusterRegistered(t *testing.T) {
	var m mock.Executor
	m.AddResult("other\ndev\n", "", nil)
	c := docker.NewClient(&m)

	require.NoError(t, ClusterRegistered("dev")(t.Context(), c, testContainer))
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "sacctmgr", "-n", "-P", "show", "cluster", "format=cluster"}, m.Calls[0].Args)
}

func TestClusterRegistered_NotYet(t *testing.T) {
	var m mock.Executor
	m.AddResult("devel\n", "", nil)
	c := docker.NewClient(&m)

	err := ClusterRegistered("dev")(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Equal(t, "cluster dev not registered with slurmdbd yet", err.Error())
}

func TestClusterRegistered_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := ClusterRegistered("dev")(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Equal(t, "listing clusters: exit status 1", err.Error())
}

func TestUnitJournal(t *testing.T) {
	var m mock.Executor
	m.AddResult("line 1\nline 2\n", "", nil)
	c := docker.NewClient(&m)

	assert.Equal(t, "line 1\nline 2", UnitJournal(t.Context(), c, testContainer, "slurmd"))
	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "journalctl", "-u", "slurmd", "-n", "20", "--no-pager", "-o", "cat"},
		m.Calls[0].Args)
}

func TestUnitProbes_Failed(t *testing.T) {
	for _, tt := range []struct {
		unit  string
		check Func
	}{
		{"munge", MungeReady},
		{"slurmd", SlurmdReady},
		{"sackd", SackdReady},
		{"slurmdbd", SlurmdbdReady},
	} {
		t.Run(tt.unit, func(t *testing.T) {
			var m mock.Executor
			m.AddResult("failed\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
			m.AddResult("fatal: something\n", "", nil)
			c := docker.NewClient(&m)

			err := tt.check(t.Context(), c, testContainer)
			var te *TerminalError
			require.ErrorAs(t, err, &te)
			assert.Equal(t, tt.unit+" failed:\nfatal: something", te.Msg)
			require.Len(t, m.Calls, 2)
			assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", tt.unit}, m.Calls[0].Args)
			assert.Equal(t,
				[]string{"exec", string(testContainer), "journalctl", "-u", tt.unit, "-n", "20", "--no-pager", "-o", "cat"},
				m.Calls[1].Args)
		})
	}
}

func TestUnitActive_Inactive(t *testing.T) {
	var m mock.Executor
	m.AddResult("inactive\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
	c := docker.NewClient(&m)

	err := UnitActive(t.Context(), c, testContainer, "munge")
	require.EqualError(t, err, "munge not ready: inactive")
	var te *TerminalError
	assert.NotErrorAs(t, err, &te)
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

func TestSlurmdbdReady_ExecError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("daemon unreachable"))
	c := docker.NewClient(&m)

	err := SlurmdbdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmdbd not ready")
	var te *TerminalError
	assert.NotErrorAs(t, err, &te)
}

func TestSlurmdbdReady_Activating(t *testing.T) {
	var m mock.Executor
	m.AddResult("activating\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
	c := docker.NewClient(&m)

	err := SlurmdbdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmdbd not ready: activating")
	var te *TerminalError
	assert.NotErrorAs(t, err, &te)
	assert.Len(t, m.Calls, 1)
}

func TestSlurmdbdReady_Failed(t *testing.T) {
	var m mock.Executor
	m.AddResult("failed\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("slurmdbd: fatal: mysql_real_connect failed\n", "", nil)
	c := docker.NewClient(&m)

	err := SlurmdbdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	var te *TerminalError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "slurmdbd failed:\nslurmdbd: fatal: mysql_real_connect failed", te.Msg)

	require.Len(t, m.Calls, 2)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "journalctl", "-u", "slurmdbd", "-n", "20", "--no-pager", "-o", "cat"},
		m.Calls[1].Args,
	)
}

func TestForService(t *testing.T) {
	p := ForService(ServiceSlurmctld)
	assert.Equal(t, "slurmctld", p.Name)
	assert.NotNil(t, p.Check)

	p = ForService(ServiceSlurmd)
	assert.Equal(t, "slurmd", p.Name)
	assert.NotNil(t, p.Check)

	p = ForService(ServiceSlurmdbd)
	assert.Equal(t, "slurmdbd", p.Name)
	assert.NotNil(t, p.Check)

	p = ForService("unknown")
	assert.Equal(t, "unknown", p.Name)
	assert.Nil(t, p.Check)
}

func TestServiceForRole(t *testing.T) {
	svc, ok := ServiceForRole(config.RoleController)
	assert.True(t, ok)
	assert.Equal(t, ServiceSlurmctld, svc)

	svc, ok = ServiceForRole(config.RoleDB)
	assert.True(t, ok)
	assert.Equal(t, ServiceSlurmdbd, svc)

	svc, ok = ServiceForRole(config.RoleWorker)
	assert.True(t, ok)
	assert.Equal(t, ServiceSlurmd, svc)

	_, ok = ServiceForRole(config.RoleSubmitter)
	assert.False(t, ok)

	_, ok = ServiceForRole("unknown")
	assert.False(t, ok)
}

func TestNodeProbes(t *testing.T) {
	tests := []struct {
		role  config.Role
		names []string
	}{
		{config.RoleController, []string{"container", "systemd", "sshd", "slurmctld"}},
		{config.RoleDB, []string{"container", "systemd", "sshd", "slurmdbd"}},
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
	assert.ErrorIs(t, err, context.Canceled)
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

func TestUntilReadyWithEvents_TakesQueuedEventsInOneRound(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	// A burst of unit events triggers one probe round, not one per event.
	events := make(chan monitor.Event, 3)
	for range 3 {
		events <- monitor.Event{Kind: monitor.EventUnitActive, Container: testContainer}
	}

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Minute, events)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Len(t, m.Calls, 2)
}

func TestUntilReadyWithEvents_QueuedDie(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSON("created"), "", nil)
	c := docker.NewClient(&m)

	events := make(chan monitor.Event, 2)
	events <- monitor.Event{Kind: monitor.EventUnitActive, Container: testContainer}
	events <- monitor.Event{Kind: monitor.EventContainerDie, Container: testContainer, Detail: "exitCode=1"}

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(t.Context(), c, testContainer, probes, time.Minute, events)
	var te *TerminalError
	require.ErrorAs(t, err, &te)
	assert.Contains(t, err.Error(), "died: exitCode=1")
	assert.Len(t, m.Calls, 1)
}

func TestUntilReadyWithEvents_ClosedWhileTaking(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	events := make(chan monitor.Event, 1)
	events <- monitor.Event{Kind: monitor.EventUnitActive, Container: testContainer}
	close(events)

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(t.Context(), c, testContainer, probes, time.Minute, events)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
}

func TestUntilReadyWithEvents_ClosedFallsBackToPolling(t *testing.T) {
	var m mock.Executor
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	// A closed channel is ready at once; the wait goes on with the ticker.
	events := make(chan monitor.Event)
	close(events)

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, 20*time.Millisecond, events)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
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
	assert.ErrorIs(t, err, context.Canceled)
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
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "last probe error: probe container")
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

// Services status queries check on managed workers and controllers.
var (
	workerServices     = []Service{ServiceMunge, ServiceSSHD, ServiceSlurmd}
	controllerServices = []Service{ServiceMunge, ServiceSSHD, ServiceSlurmctld}
)

func TestSnapshot_WorkerAllActive(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\nactive\nactive\n", "", nil)
	c := docker.NewClient(&m)

	snap, err := Snapshot(t.Context(), c, testContainer, workerServices)
	require.NoError(t, err)
	assert.True(t, snap[ServiceMunge])
	assert.True(t, snap[ServiceSSHD])
	assert.True(t, snap[ServiceSlurmd])
	_, hasCtld := snap[ServiceSlurmctld]
	assert.False(t, hasCtld)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "systemctl", "is-active", "munge", "sshd", "slurmd"},
		m.Calls[0].Args,
	)
}

func TestSnapshot_WorkerOneInactive(t *testing.T) {
	var m mock.Executor
	// systemctl exits non-zero when any unit is inactive but still prints
	// the per-unit states. ExecAllowNonZero surfaces that stdout.
	m.AddResult("active\nactive\nfailed\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
	c := docker.NewClient(&m)

	snap, err := Snapshot(t.Context(), c, testContainer, workerServices)
	require.NoError(t, err)
	assert.True(t, snap[ServiceMunge])
	assert.True(t, snap[ServiceSSHD])
	assert.False(t, snap[ServiceSlurmd])
}

func TestSnapshot_MungeAndSSHD(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\nactive\n", "", nil)
	c := docker.NewClient(&m)

	snap, err := Snapshot(t.Context(), c, testContainer, []Service{ServiceMunge, ServiceSSHD})
	require.NoError(t, err)
	assert.True(t, snap[ServiceMunge])
	assert.True(t, snap[ServiceSSHD])
	assert.Len(t, snap, 2)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "systemctl", "is-active", "munge", "sshd"},
		m.Calls[0].Args,
	)
}

func TestSnapshot_ControllerWithScontrolPing(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\nactive\n", "", nil)           // systemctl is-active munge sshd
	m.AddResult("Slurmctld(primary) is UP\n", "", nil) // scontrol ping
	c := docker.NewClient(&m)

	snap, err := Snapshot(t.Context(), c, testContainer, controllerServices)
	require.NoError(t, err)
	assert.True(t, snap[ServiceMunge])
	assert.True(t, snap[ServiceSSHD])
	assert.True(t, snap[ServiceSlurmctld])
	_, hasSlurmd := snap[ServiceSlurmd]
	assert.False(t, hasSlurmd)

	require.Len(t, m.Calls, 2)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "systemctl", "is-active", "munge", "sshd"},
		m.Calls[0].Args,
	)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "scontrol", "ping"},
		m.Calls[1].Args,
	)
}

func TestSnapshot_ControllerScontrolFails(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\nactive\n", "", nil)
	m.AddResult("", "", fmt.Errorf("scontrol: connection refused"))
	c := docker.NewClient(&m)

	snap, err := Snapshot(t.Context(), c, testContainer, controllerServices)
	require.NoError(t, err)
	assert.True(t, snap[ServiceMunge])
	assert.True(t, snap[ServiceSSHD])
	assert.False(t, snap[ServiceSlurmctld])
}

func TestSnapshot_SlurmctldOnly(t *testing.T) {
	var m mock.Executor
	m.AddResult("Slurmctld(primary) at controller is UP\n", "", nil)
	c := docker.NewClient(&m)

	snap, err := Snapshot(t.Context(), c, testContainer, []Service{ServiceSlurmctld})
	require.NoError(t, err)
	assert.Equal(t, map[Service]bool{ServiceSlurmctld: true}, snap)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "scontrol", "ping"}, m.Calls[0].Args)
}

func TestSnapshot_NoServices(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)

	snap, err := Snapshot(t.Context(), c, testContainer, nil)
	require.NoError(t, err)
	assert.Empty(t, snap)
	assert.Empty(t, m.Calls)
}

func TestSnapshot_ExecDaemonError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := Snapshot(t.Context(), c, testContainer, workerServices)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "systemctl is-active")
}

func TestSnapshot_MalformedOutput(t *testing.T) {
	var m mock.Executor
	// Only 2 lines for a 3-unit worker query.
	m.AddResult("active\nactive\n", "", nil)
	c := docker.NewClient(&m)

	_, err := Snapshot(t.Context(), c, testContainer, workerServices)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "got 2 lines")
}

func TestWaitEnded_NoProbeFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := waitEnded(ctx, testContainer, nil)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "node "+string(testContainer)+" not ready: context canceled", err.Error())
}

func TestWaitEnded_KeepsLastProbeError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	probeErr := &TerminalError{Msg: "boom"}

	err := waitEnded(ctx, testContainer, probeErr)
	require.ErrorIs(t, err, context.Canceled)
	var te *TerminalError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "node "+string(testContainer)+" not ready: context canceled; last probe error: boom", err.Error())
}
