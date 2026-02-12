// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testContainer docker.ContainerName = "sind-dev-controller"

func inspectJSON(status string) string {
	return fmt.Sprintf(`[{
  "Id": "abc123",
  "Name": "/%s",
  "State": {"Status": %q},
  "Config": {"Labels": {}},
  "NetworkSettings": {"Networks": {}}
}]`, testContainer, status)
}

func TestContainerRunning(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	err := ContainerRunning(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"inspect", string(testContainer)}, m.Calls[0].Args)
}

func TestContainerRunning_NotRunning(t *testing.T) {
	for _, status := range []string{"exited", "created", "paused", "dead"} {
		t.Run(status, func(t *testing.T) {
			var m docker.MockExecutor
			m.AddResult(inspectJSON(status), "", nil)
			c := docker.NewClient(&m)

			err := ContainerRunning(context.Background(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), status)
			assert.Contains(t, err.Error(), "expected running")
		})
	}
}

func TestContainerRunning_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := ContainerRunning(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting container")
}

func TestSystemdReady_Running(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	err := SystemdReady(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "sh", "-c", "systemctl is-system-running 2>/dev/null || true"},
		m.Calls[0].Args)
}

func TestSystemdReady_Degraded(t *testing.T) {
	var m docker.MockExecutor
	// The sh wrapper always exits 0, so stdout contains "degraded".
	m.AddResult("degraded\n", "", nil)
	c := docker.NewClient(&m)

	err := SystemdReady(context.Background(), c, testContainer)
	require.NoError(t, err)
}

func TestSystemdReady_NotReady(t *testing.T) {
	for _, state := range []string{"starting", "initializing", "stopping"} {
		t.Run(state, func(t *testing.T) {
			var m docker.MockExecutor
			m.AddResult(state+"\n", "", nil)
			c := docker.NewClient(&m)

			err := SystemdReady(context.Background(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), state)
		})
	}
}

func TestSystemdReady_ExecError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)

	err := SystemdReady(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking systemd state")
}

func TestSSHDReady(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("SSH-2.0-OpenSSH_9.8\n", "", nil)
	c := docker.NewClient(&m)

	err := SSHDReady(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "bash", "-c", "read -t1 line < /dev/tcp/localhost/22 && echo \"$line\""},
		m.Calls[0].Args)
}

func TestSSHDReady_UnexpectedBanner(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("HTTP/1.1 400 Bad Request\n", "", nil)
	c := docker.NewClient(&m)

	err := SSHDReady(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected banner")
}

func TestSSHDReady_ConnectionRefused(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SSHDReady(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sshd not ready")
}

func TestSlurmctldReady(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("Slurmctld(primary) at controller is UP\n", "", nil)
	c := docker.NewClient(&m)

	err := SlurmctldReady(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "scontrol", "ping"}, m.Calls[0].Args)
}

func TestSlurmctldReady_NotReady(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "slurm_persist_conn_open_without_init: failed to open persistent connection\n",
		fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SlurmctldReady(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmctld not ready")
}

func TestSlurmdReady(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("active\n", "", nil)
	c := docker.NewClient(&m)

	err := SlurmdReady(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "slurmd"}, m.Calls[0].Args)
}

func TestSlurmdReady_NotReady(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := SlurmdReady(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmd not ready")
}

func TestNodeProbes(t *testing.T) {
	tests := []struct {
		role  string
		names []string
	}{
		{"controller", []string{"container", "systemd", "sshd", "slurmctld"}},
		{"compute", []string{"container", "systemd", "sshd", "slurmd"}},
		{"submitter", []string{"container", "systemd", "sshd"}},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
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
	var m docker.MockExecutor
	// Single probe that succeeds immediately.
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(context.Background(), c, testContainer, probes, time.Second, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 1)
}

func TestUntilReady_RetryThenPass(t *testing.T) {
	var m docker.MockExecutor
	// First attempt: not running. Second attempt: running.
	m.AddResult(inspectJSON("created"), "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(context.Background(), c, testContainer, probes, time.Second, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
}

func TestUntilReady_Timeout(t *testing.T) {
	var m docker.MockExecutor
	// Always fail — queue enough results to cover polling attempts.
	for i := 0; i < 100; i++ {
		m.AddResult(inspectJSON("created"), "", nil)
	}
	c := docker.NewClient(&m)

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(context.Background(), c, testContainer, probes, 50*time.Millisecond, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
	assert.Contains(t, err.Error(), "probe container")
}

func TestUntilReady_MultipleProbes(t *testing.T) {
	var m docker.MockExecutor
	// Two probes, both pass on first attempt.
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	probes := []Probe{
		{"container", ContainerRunning},
		{"systemd", SystemdReady},
	}
	err := UntilReady(context.Background(), c, testContainer, probes, time.Second, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
}

func TestUntilReady_SecondProbeFails(t *testing.T) {
	var m docker.MockExecutor
	// First attempt: container OK, systemd not ready.
	// Second attempt: container OK, systemd ready.
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("starting\n", "", nil)
	m.AddResult(inspectJSON("running"), "", nil)
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	probes := []Probe{
		{"container", ContainerRunning},
		{"systemd", SystemdReady},
	}
	err := UntilReady(context.Background(), c, testContainer, probes, time.Second, time.Millisecond)
	require.NoError(t, err)
	assert.Len(t, m.Calls, 4)
}

func TestUntilReady_ContextCanceled(t *testing.T) {
	var m docker.MockExecutor
	for i := 0; i < 100; i++ {
		m.AddResult(inspectJSON("created"), "", nil)
	}
	c := docker.NewClient(&m)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	probes := []Probe{{"container", ContainerRunning}}
	err := UntilReady(ctx, c, testContainer, probes, time.Second, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}
