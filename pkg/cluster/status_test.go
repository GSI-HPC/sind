// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statusInspectJSON(name, status, ip string) string {
	return fmt.Sprintf(`[{
  "Id": "abc123",
  "Name": "/%s",
  "State": {"Status": %q},
  "Config": {"Labels": {}},
  "NetworkSettings": {"Networks": {"sind-dev-net": {"IPAddress": %q}}}
}]`, name, status, ip)
}

// healthyOnCall returns a mock dispatcher where all checks pass.
// Failed checks can be overridden by wrapping this function.
func healthyOnCall(containerName, ip string) func([]string, string) docker.MockResult {
	return func(args []string, _ string) docker.MockResult {
		if len(args) >= 2 && args[0] == "inspect" {
			return docker.MockResult{Stdout: statusInspectJSON(containerName, "running", ip)}
		}
		if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
			// munge or slurmd
			return docker.MockResult{Stdout: "active\n"}
		}
		if len(args) >= 3 && args[2] == "sh" {
			return docker.MockResult{Stdout: "running\n"}
		}
		if len(args) >= 3 && args[2] == "bash" {
			return docker.MockResult{Stdout: "SSH-2.0-OpenSSH_9.8\n"}
		}
		if len(args) >= 3 && args[2] == "scontrol" {
			return docker.MockResult{Stdout: "Slurmctld(primary) at controller is UP\n"}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

func TestGetNodeHealth_Controller(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = healthyOnCall("sind-dev-controller", "172.18.0.2")
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-controller", "controller")

	require.NoError(t, err)
	assert.Equal(t, "running", health.Container)
	assert.Equal(t, "172.18.0.2", health.IP)
	assert.True(t, health.Munge)
	assert.True(t, health.SSHD)
	require.Contains(t, health.Services, "slurmctld")
	assert.True(t, health.Services["slurmctld"])
}

func TestGetNodeHealth_Compute(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = healthyOnCall("sind-dev-compute-0", "172.18.0.3")
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-compute-0", "compute")

	require.NoError(t, err)
	assert.Equal(t, "running", health.Container)
	assert.Equal(t, "172.18.0.3", health.IP)
	assert.True(t, health.Munge)
	assert.True(t, health.SSHD)
	require.Contains(t, health.Services, "slurmd")
	assert.True(t, health.Services["slurmd"])
}

func TestGetNodeHealth_Submitter(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = healthyOnCall("sind-dev-submitter", "172.18.0.4")
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-submitter", "submitter")

	require.NoError(t, err)
	assert.Equal(t, "running", health.Container)
	assert.True(t, health.Munge)
	assert.True(t, health.SSHD)
	assert.Empty(t, health.Services)
}

func TestGetNodeHealth_ContainerNotRunning(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if len(args) >= 2 && args[0] == "inspect" {
			return docker.MockResult{Stdout: statusInspectJSON("sind-dev-controller", "exited", "")}
		}
		return docker.MockResult{Err: fmt.Errorf("container not running")}
	}
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-controller", "controller")

	require.NoError(t, err)
	assert.Equal(t, "exited", health.Container)
	assert.False(t, health.Munge)
	assert.False(t, health.SSHD)
	assert.False(t, health.Services["slurmctld"])
}

func TestGetNodeHealth_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)

	_, err := GetNodeHealth(context.Background(), c, "sind-dev-controller", "controller")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting container")
}

func TestGetNodeHealth_ServiceFailing(t *testing.T) {
	var m docker.MockExecutor
	base := healthyOnCall("sind-dev-compute-0", "172.18.0.3")
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// slurmd fails
		if len(args) >= 5 && args[2] == "systemctl" && args[4] == "slurmd" {
			return docker.MockResult{Err: fmt.Errorf("exit status 1")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-compute-0", "compute")

	require.NoError(t, err)
	assert.Equal(t, "running", health.Container)
	assert.True(t, health.Munge)
	assert.True(t, health.SSHD)
	assert.False(t, health.Services["slurmd"])
}

func TestGetNodeHealth_MungeFailing(t *testing.T) {
	var m docker.MockExecutor
	base := healthyOnCall("sind-dev-controller", "172.18.0.2")
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// munge fails
		if len(args) >= 5 && args[2] == "systemctl" && args[4] == "munge" {
			return docker.MockResult{Err: fmt.Errorf("exit status 1")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-controller", "controller")

	require.NoError(t, err)
	assert.Equal(t, "running", health.Container)
	assert.False(t, health.Munge)
	assert.True(t, health.SSHD)
	assert.True(t, health.Services["slurmctld"])
}

func TestGetNodeHealth_SSHDFailing(t *testing.T) {
	var m docker.MockExecutor
	base := healthyOnCall("sind-dev-controller", "172.18.0.2")
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		if len(args) >= 3 && args[2] == "bash" {
			return docker.MockResult{Err: fmt.Errorf("exit status 1")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-controller", "controller")

	require.NoError(t, err)
	assert.Equal(t, "running", health.Container)
	assert.True(t, health.Munge)
	assert.False(t, health.SSHD)
	assert.True(t, health.Services["slurmctld"])
}

func TestGetNodeHealth_MultipleIPs(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		if len(args) >= 2 && args[0] == "inspect" {
			return docker.MockResult{Stdout: `[{
  "Id": "abc123",
  "Name": "/sind-dev-controller",
  "State": {"Status": "running"},
  "Config": {"Labels": {}},
  "NetworkSettings": {"Networks": {
    "sind-dev-net": {"IPAddress": "172.18.0.2"},
    "sind-mesh": {"IPAddress": "172.19.0.5"}
  }}
}]`}
		}
		return healthyOnCall("sind-dev-controller", "")(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-controller", "controller")

	require.NoError(t, err)
	assert.NotEmpty(t, health.IP)
}
