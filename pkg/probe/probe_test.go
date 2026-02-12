// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"
	"testing"

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

func TestCheckContainerRunning(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(inspectJSON("running"), "", nil)
	c := docker.NewClient(&m)

	err := CheckContainerRunning(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"inspect", string(testContainer)}, m.Calls[0].Args)
}

func TestCheckContainerRunning_NotRunning(t *testing.T) {
	for _, status := range []string{"exited", "created", "paused", "dead"} {
		t.Run(status, func(t *testing.T) {
			var m docker.MockExecutor
			m.AddResult(inspectJSON(status), "", nil)
			c := docker.NewClient(&m)

			err := CheckContainerRunning(context.Background(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), status)
			assert.Contains(t, err.Error(), "expected running")
		})
	}
}

func TestCheckContainerRunning_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := CheckContainerRunning(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting container")
}

func TestCheckSystemdReady_Running(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("running\n", "", nil)
	c := docker.NewClient(&m)

	err := CheckSystemdReady(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "sh", "-c", "systemctl is-system-running 2>/dev/null || true"},
		m.Calls[0].Args)
}

func TestCheckSystemdReady_Degraded(t *testing.T) {
	var m docker.MockExecutor
	// The sh wrapper always exits 0, so stdout contains "degraded".
	m.AddResult("degraded\n", "", nil)
	c := docker.NewClient(&m)

	err := CheckSystemdReady(context.Background(), c, testContainer)
	require.NoError(t, err)
}

func TestCheckSystemdReady_NotReady(t *testing.T) {
	for _, state := range []string{"starting", "initializing", "stopping"} {
		t.Run(state, func(t *testing.T) {
			var m docker.MockExecutor
			m.AddResult(state+"\n", "", nil)
			c := docker.NewClient(&m)

			err := CheckSystemdReady(context.Background(), c, testContainer)
			require.Error(t, err)
			assert.Contains(t, err.Error(), state)
		})
	}
}

func TestCheckSystemdReady_ExecError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)

	err := CheckSystemdReady(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking systemd state")
}

func TestCheckSSHD(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("SSH-2.0-OpenSSH_9.8\n", "", nil)
	c := docker.NewClient(&m)

	err := CheckSSHD(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t,
		[]string{"exec", string(testContainer), "bash", "-c", "read -t1 line < /dev/tcp/localhost/22 && echo \"$line\""},
		m.Calls[0].Args)
}

func TestCheckSSHD_UnexpectedBanner(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("HTTP/1.1 400 Bad Request\n", "", nil)
	c := docker.NewClient(&m)

	err := CheckSSHD(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected banner")
}

func TestCheckSSHD_ConnectionRefused(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := CheckSSHD(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sshd not ready")
}

func TestCheckSlurmctld(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("Slurmctld(primary) at controller is UP\n", "", nil)
	c := docker.NewClient(&m)

	err := CheckSlurmctld(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "scontrol", "ping"}, m.Calls[0].Args)
}

func TestCheckSlurmctld_NotReady(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "slurm_persist_conn_open_without_init: failed to open persistent connection\n",
		fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := CheckSlurmctld(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmctld not ready")
}

func TestCheckSlurmd(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("active\n", "", nil)
	c := docker.NewClient(&m)

	err := CheckSlurmd(context.Background(), c, testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainer), "systemctl", "is-active", "slurmd"}, m.Calls[0].Args)
}

func TestCheckSlurmd_NotReady(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	err := CheckSlurmd(context.Background(), c, testContainer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slurmd not ready")
}
