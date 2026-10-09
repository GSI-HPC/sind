// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
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

func TestSlurmrestdReady(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\n", "", nil) // systemctl is-active
	m.AddResult("", "", nil)         // port check
	c := docker.NewClient(&m)

	require.NoError(t, SlurmrestdReady(t.Context(), c, testContainer))
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "slurmrestd"}, m.Calls[0].Args)
	assert.Equal(t, []string{"exec", string(testContainer), "bash", "-c", "exec 3<>/dev/tcp/localhost/6820"}, m.Calls[1].Args)
}

func TestSlurmrestdReady_NotListening(t *testing.T) {
	var m mock.Executor
	m.AddResult("active\n", "", nil)
	m.AddResult("", "connection refused", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SlurmrestdReady(t.Context(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmrestd not listening on port 6820")
	var te *TerminalError
	assert.NotErrorAs(t, err, &te, "slurmrestd may still be starting")
}

func TestSlurmrestdReady_Failed(t *testing.T) {
	var m mock.Executor
	m.AddResult("failed\n", "", nil)                            // systemctl is-active
	m.AddResult("fatal: Unable to bind listen port\n", "", nil) // journalctl
	c := docker.NewClient(&m)

	err := SlurmrestdReady(t.Context(), c, testContainer)
	var te *TerminalError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "slurmrestd failed:\nfatal: Unable to bind listen port", te.Msg)
	assert.Len(t, m.Calls, 2, "no port check after a failed unit")
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
	for _, svc := range []Service{ServiceMunge, ServiceSSHD, ServiceSlurmctld, ServiceSlurmd, ServiceSlurmdbd, ServiceSackd, ServiceSlurmrestd} {
		p := ForService(svc)
		assert.Equal(t, string(svc), p.Name)
		assert.NotNil(t, p.Check, svc)
		assert.Equal(t, string(svc)+".service", p.Unit, "the unit the check follows")
	}

	p := ForService(ServiceMariadb)
	assert.Equal(t, Probe{Name: "mariadb"}, p, "no check: sind does not wait for mariadb")

	p = ForService("unknown")
	assert.Equal(t, "unknown", p.Name)
	assert.Nil(t, p.Check)
	assert.Empty(t, p.Unit)
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

	svc, ok = ServiceForRole(config.RoleAPI)
	assert.True(t, ok)
	assert.Equal(t, ServiceSlurmrestd, svc)

	_, ok = ServiceForRole(config.RoleSubmitter)
	assert.False(t, ok)

	_, ok = ServiceForRole("unknown")
	assert.False(t, ok)
}

func TestNodeProbes(t *testing.T) {
	tests := []struct {
		role  config.Role
		names []string
		units []string
	}{
		{config.RoleController, []string{"container", "systemd", "sshd", "slurmctld"}, []string{"", "", "sshd.service", "slurmctld.service"}},
		{config.RoleDB, []string{"container", "systemd", "sshd", "slurmdbd"}, []string{"", "", "sshd.service", "slurmdbd.service"}},
		{config.RoleAPI, []string{"container", "systemd", "sshd", "slurmrestd"}, []string{"", "", "sshd.service", "slurmrestd.service"}},
		{config.RoleWorker, []string{"container", "systemd", "sshd", "slurmd"}, []string{"", "", "sshd.service", "slurmd.service"}},
		{config.RoleSubmitter, []string{"container", "systemd", "sshd"}, []string{"", "", "sshd.service"}},
		{"unknown", []string{"container", "systemd", "sshd"}, []string{"", "", "sshd.service"}},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			probes := NodeProbes(tt.role)
			var names, units []string
			for _, p := range probes {
				names = append(names, p.Name)
				units = append(units, p.Unit)
			}
			assert.Equal(t, tt.names, names)
			assert.Equal(t, tt.units, units)
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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
		{Name: "container", Check: ContainerRunning},
		{Name: "systemd", Check: SystemdReady},
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
		{Name: "container", Check: ContainerRunning},
		{Name: "systemd", Check: SystemdReady},
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
		{Name: "container", Check: ContainerRunning},
		{Name: "systemd", Check: SystemdReady},
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

func TestUntilReady_NonPositiveInterval(t *testing.T) {
	// time.NewTicker panics on a non-positive interval; the waits use
	// DefaultInterval instead.
	for _, interval := range []time.Duration{0, -time.Second} {
		var m mock.Executor
		m.AddResult(inspectJSON("running"), "", nil)
		m.AddResult(inspectJSON("running"), "", nil)
		c := docker.NewClient(&m)
		probes := []Probe{{Name: "container", Check: ContainerRunning}}
		require.NoError(t, UntilReady(t.Context(), c, testContainer, probes, interval))
		require.NoError(t, UntilReadyWithEvents(t.Context(), c, testContainer, probes, interval, nil))
	}
	assert.Equal(t, DefaultInterval, orDefault(0))
	assert.Equal(t, DefaultInterval, orDefault(-time.Second))
	assert.Equal(t, time.Second, orDefault(time.Second))
}

func TestUntilReady_ContextCanceled(t *testing.T) {
	var m mock.Executor
	for i := 0; i < 100; i++ {
		m.AddResult(inspectJSON("created"), "", nil)
	}
	c := docker.NewClient(&m)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already canceled

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
	err := UntilReady(ctx, c, testContainer, probes, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
	assert.Contains(t, err.Error(), "exited")
	// Only one call — no retries for terminal state.
	assert.Len(t, m.Calls, 1)
}

// A wait that ends while a probe runs kills the probe's docker call, which
// then fails with "signal: killed": the wait reports the probe that failed
// before, not the killed call. A probe that tells the node will never be
// ready still ends it with its error.
func TestUntilReady_EndKeepsTheFailingProbe(t *testing.T) {
	for _, tc := range []struct {
		name, killed, want string
	}{
		{"killed", "", "node sind-dev-controller not ready: context canceled; last probe error: probe munge: munge not ready"},
		{"terminal", inspectJSON("exited"), "node sind-dev-controller not ready: probe container: container sind-dev-controller is exited (exit code 0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			inspects := 0
			m := mock.Executor{OnCall: func([]string, string) mock.Result {
				inspects++
				if inspects == 1 {
					return mock.Result{Stdout: inspectJSON("running")}
				}
				cancel()
				if tc.killed == "" {
					return mock.Result{Err: errors.New("signal: killed")}
				}
				return mock.Result{Stdout: tc.killed}
			}}
			probes := []Probe{
				{Name: "container", Check: ContainerRunning},
				{Name: "munge", Check: func(context.Context, *docker.Client, docker.ContainerName) error {
					return errors.New("munge not ready")
				}},
			}

			err := UntilReady(ctx, docker.NewClient(&m), testContainer, probes, time.Millisecond)

			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
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
	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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

	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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
	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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
	probes := []Probe{{Name: "container", Check: ContainerRunning}}
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
	probes := []Probe{{Name: "container", Check: ContainerRunning}}
	err := UntilReadyWithEvents(ctx, c, testContainer, probes, time.Millisecond, events)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited")
	assert.Len(t, m.Calls, 1)
}

// --- skipping probes that passed ---

// probeCalls names the probe each docker call of a wait ran: the container
// probe's inspect, or the exec's command.
func probeCalls(calls []mock.Call) []string {
	names := make([]string, len(calls))
	for i, c := range calls {
		switch {
		case c.Args[0] == "inspect":
			names[i] = "container"
		case c.Args[2] == "sh":
			names[i] = "systemd"
		case c.Args[2] == "bash":
			names[i] = "sshd"
		case c.Args[2] == "journalctl":
			names[i] = "journal " + c.Args[4]
		default:
			names[i] = c.Args[len(c.Args)-1]
		}
	}
	return names
}

// baseProbes are the probes a node's base readiness wait runs.
var baseProbes = []Probe{
	{Name: "container", Check: ContainerRunning},
	{Name: "systemd", Check: SystemdReady},
	ForService(ServiceSSHD),
	ForService(ServiceMunge),
}

// queueBaseProbeResults queues the results of a base readiness wait whose
// systemd probe fails rounds times first.
func queueBaseProbeResults(m *mock.Executor, rounds int, container bool) {
	for i := range rounds {
		if i == 0 || container {
			m.AddResult(inspectJSON("running"), "", nil)
		}
		m.AddResult("starting\n", "", nil)
	}
	if container {
		m.AddResult(inspectJSON("running"), "", nil)
	}
	m.AddResult("running\n", "", nil)
	m.AddResult("SSH-2.0-OpenSSH_9.9\n", "", nil)
	m.AddResult("active\n", "", nil)
}

func TestUntilReadyWithEvents_SkipsPassedProbes(t *testing.T) {
	// The container probe passed in the first round: the rounds while
	// systemd boots run the systemd probe alone.
	var m mock.Executor
	queueBaseProbeResults(&m, 3, false)
	c := docker.NewClient(&m)

	events := make(chan monitor.Event, 64)
	err := UntilReadyWithEvents(t.Context(), c, testContainer, baseProbes, time.Millisecond, events)

	require.NoError(t, err)
	assert.Equal(t, []string{"container", "systemd", "systemd", "systemd", "systemd", "sshd", "munge"}, probeCalls(m.Calls))
}

func TestUntilReady_RunsEveryProbeEachRound(t *testing.T) {
	// Without events nothing tells the wait that a passed probe changed:
	// every round starts again from the first probe.
	var m mock.Executor
	queueBaseProbeResults(&m, 3, true)
	c := docker.NewClient(&m)

	err := UntilReady(t.Context(), c, testContainer, baseProbes, time.Millisecond)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"container", "systemd", "container", "systemd", "container", "systemd", "container", "systemd", "sshd", "munge",
	}, probeCalls(m.Calls))
}

func TestUntilReadyWithEvents_UnitEventRerunsItsProbe(t *testing.T) {
	// munge is still starting; an event of sshd.service runs the passed
	// sshd probe again, one of another unit runs none.
	for _, tt := range []struct {
		unit string
		want []string
	}{
		{"sshd.service", []string{"container", "systemd", "sshd", "munge", "sshd", "munge"}},
		{"multi-user.target", []string{"container", "systemd", "sshd", "munge", "munge"}},
	} {
		t.Run(tt.unit, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(inspectJSON("running"), "", nil)
			m.AddResult("running\n", "", nil)
			m.AddResult("SSH-2.0-OpenSSH_9.9\n", "", nil)
			m.AddResult("activating\n", "", nil)
			if tt.unit == "sshd.service" {
				m.AddResult("SSH-2.0-OpenSSH_9.9\n", "", nil)
			}
			m.AddResult("active\n", "", nil)
			c := docker.NewClient(&m)

			events := make(chan monitor.Event, 64)
			events <- monitor.Event{Kind: monitor.EventUnitActive, Container: testContainer, Unit: tt.unit}
			// A long interval: only the event starts the second round.
			err := UntilReadyWithEvents(t.Context(), c, testContainer, baseProbes, time.Minute, events)

			require.NoError(t, err)
			assert.Equal(t, tt.want, probeCalls(m.Calls))
		})
	}
}

func TestUntilReadyWithEvents_FailedUnitEndsTheWait(t *testing.T) {
	// munge passed, then failed while sshd was not up yet: its event runs
	// the munge probe again, which finds the unit failed.
	probes := []Probe{ForService(ServiceMunge), ForService(ServiceSSHD)}
	var m mock.Executor
	m.AddResult("active\n", "", nil)
	m.AddResult("", "", fmt.Errorf("connection refused"))
	m.AddResult("failed\n", "", &exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("munge: fatal: bad key\n", "", nil)
	c := docker.NewClient(&m)

	events := make(chan monitor.Event, 64)
	events <- monitor.Event{Kind: monitor.EventUnitFailed, Container: testContainer, Unit: "munge.service"}
	err := UntilReadyWithEvents(t.Context(), c, testContainer, probes, time.Minute, events)

	var te *TerminalError
	require.ErrorAs(t, err, &te)
	assert.Equal(t, "munge failed:\nmunge: fatal: bad key", te.Msg)
	assert.Equal(t, []string{"munge", "sshd", "munge", "journal munge"}, probeCalls(m.Calls))
}

func TestUntilReadyWithEvents_ContainerEventRerunsEveryProbe(t *testing.T) {
	probes := []Probe{{Name: "container", Check: ContainerRunning}, {Name: "systemd", Check: SystemdReady}}
	var m mock.Executor
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("starting\n", "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	events := make(chan monitor.Event, 64)
	events <- monitor.Event{Kind: monitor.EventContainerUnpause, Container: testContainer}
	err := UntilReadyWithEvents(t.Context(), c, testContainer, probes, time.Minute, events)

	require.NoError(t, err)
	assert.Equal(t, []string{"container", "systemd", "container", "systemd"}, probeCalls(m.Calls))
}

func TestUntilReadyWithEvents_UntrustedEventsRunEveryProbe(t *testing.T) {
	// Once the events can no longer tell what changed, every round runs
	// every probe, as UntilReady does.
	for _, tt := range []struct {
		name   string
		events func() chan monitor.Event
	}{
		{"monitor error of the node", func() chan monitor.Event {
			ch := make(chan monitor.Event, 64)
			ch <- monitor.Event{Kind: monitor.EventMonitorError, Container: testContainer, Detail: "systemd monitor stream ended"}
			return ch
		}},
		{"monitor error of the watcher", func() chan monitor.Event {
			ch := make(chan monitor.Event, 64)
			ch <- monitor.Event{Kind: monitor.EventMonitorError, Detail: "docker events stream failed"}
			return ch
		}},
		{"closed", func() chan monitor.Event {
			ch := make(chan monitor.Event, 64)
			close(ch)
			return ch
		}},
		{"closed while taking", func() chan monitor.Event {
			ch := make(chan monitor.Event, 64)
			ch <- monitor.Event{Kind: monitor.EventUnitActive, Container: testContainer, Unit: "x.service"}
			close(ch)
			return ch
		}},
		{"unbuffered", func() chan monitor.Event { return make(chan monitor.Event) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			queueBaseProbeResults(&m, 3, true)
			c := docker.NewClient(&m)

			err := UntilReadyWithEvents(t.Context(), c, testContainer, baseProbes, time.Millisecond, tt.events())

			require.NoError(t, err)
			assert.Equal(t, []string{
				"container", "systemd", "container", "systemd", "container", "systemd", "container", "systemd", "sshd", "munge",
			}, probeCalls(m.Calls))
		})
	}
}

func TestUntilReadyWithEvents_FullBufferRerunsEveryProbe(t *testing.T) {
	// The watcher drops what a full subscriber has no room for: after a
	// full buffer, the passed probes run again. Events of another
	// container do not count, and with room left nothing was dropped.
	probes := []Probe{{Name: "container", Check: ContainerRunning}, {Name: "systemd", Check: SystemdReady}}
	other := monitor.Event{Kind: monitor.EventUnitActive, Container: testContainer, Unit: "x.target"}
	for _, tt := range []struct {
		name   string
		size   int
		queued int
		want   []string
	}{
		{"full", 3, 3, []string{"container", "systemd", "container", "systemd"}},
		{"room left", 4, 2, []string{"container", "systemd", "systemd"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(inspectJSON("running"), "", nil)
			m.AddResult("starting\n", "", nil)
			if tt.name == "full" {
				m.AddResult(inspectJSON("running"), "", nil)
			}
			m.AddResult("running\n", "", nil)
			c := docker.NewClient(&m)

			events := make(chan monitor.Event, tt.size)
			events <- monitor.Event{Kind: monitor.EventUnitActive, Container: "sind-dev-worker-0", Unit: "x.target"}
			for range tt.queued - 1 {
				events <- other
			}
			err := UntilReadyWithEvents(t.Context(), c, testContainer, probes, time.Minute, events)

			require.NoError(t, err)
			assert.Equal(t, tt.want, probeCalls(m.Calls))
		})
	}
}

func TestProgress_TakeQueued(t *testing.T) {
	// A tick takes the events queued meanwhile before its round, without
	// waiting for one.
	munge := ForService(ServiceMunge)
	probes := []Probe{{Name: "container", Check: ContainerRunning}, munge}
	passedAll := func() *progress {
		p := newProgress(probes, make(chan monitor.Event, 1))
		p.pass(0)
		p.pass(1)
		return p
	}

	t.Run("nothing queued", func(t *testing.T) {
		p := passedAll()
		events := make(chan monitor.Event, 4)
		got := p.takeQueued(testContainer, events)
		assert.Equal(t, (<-chan monitor.Event)(events), got)
		assert.Equal(t, []bool{true, true}, p.passed)
	})
	t.Run("unit event", func(t *testing.T) {
		p := passedAll()
		events := make(chan monitor.Event, 4)
		events <- monitor.Event{Kind: monitor.EventUnitActive, Container: testContainer, Unit: munge.Unit}
		got := p.takeQueued(testContainer, events)
		assert.Equal(t, (<-chan monitor.Event)(events), got)
		assert.Equal(t, []bool{true, false}, p.passed)
		assert.True(t, p.trusted)
	})
	t.Run("closed", func(t *testing.T) {
		p := passedAll()
		events := make(chan monitor.Event, 4)
		close(events)
		got := p.takeQueued(testContainer, events)
		assert.Nil(t, got)
		assert.Equal(t, []bool{false, false}, p.passed)
		assert.False(t, p.trusted)
	})
	t.Run("die", func(t *testing.T) {
		p := passedAll()
		events := make(chan monitor.Event, 4)
		events <- monitor.Event{Kind: monitor.EventContainerDie, Container: testContainer, Detail: "exit 1"}
		p.takeQueued(testContainer, events)
		var te *TerminalError
		require.ErrorAs(t, p.died, &te)
		assert.Contains(t, p.died.Error(), "container sind-dev-controller died: exit 1")
	})
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

// endsOnDemand is a wait whose deadline passes when end is called, not at
// a time, so that a test ends it at the point it means to, however slow
// the machine runs.
type endsOnDemand struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newEndsOnDemand(parent context.Context) *endsOnDemand {
	return &endsOnDemand{Context: parent, done: make(chan struct{})}
}

func (c *endsOnDemand) Done() <-chan struct{} { return c.done }

func (c *endsOnDemand) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *endsOnDemand) end() { c.once.Do(func() { close(c.done) }) }

// hangsUntilEnd returns a probe that ends the wait ctx as it starts, and
// runs until the wait has ended, as a docker call that hangs does, and
// then fails as the killed call does.
func hangsUntilEnd(ctx *endsOnDemand) Func {
	return func(probeCtx context.Context, _ *docker.Client, _ docker.ContainerName) error {
		ctx.end()
		<-probeCtx.Done()
		return fmt.Errorf("signal: killed")
	}
}

// A probe that hangs until the wait ends, after another probe that failed
// before has passed, is the one reported, with its own error: the error of
// the probe that passed says nothing of why the node is not ready.
func TestUntilReady_EndReportsTheProbeThatHangs(t *testing.T) {
	ctx := newEndsOnDemand(t.Context())
	var m mock.Executor
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	probes := []Probe{
		{Name: "container", Check: ContainerRunning},
		{Name: "munge", Check: hangsUntilEnd(ctx)},
	}

	err := UntilReady(ctx, docker.NewClient(&m), testContainer, probes, time.Millisecond)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, "node sind-dev-controller not ready: context deadline exceeded; last probe error: probe munge: signal: killed", err.Error())
}
