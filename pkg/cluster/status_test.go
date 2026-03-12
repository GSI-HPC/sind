// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"os/exec"
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

func TestGetNodeHealth_SlurmctldFailing(t *testing.T) {
	var m docker.MockExecutor
	base := healthyOnCall("sind-dev-controller", "172.18.0.2")
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// scontrol ping fails
		if len(args) >= 3 && args[2] == "scontrol" {
			return docker.MockResult{Err: fmt.Errorf("exit status 1")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-controller", "controller")

	require.NoError(t, err)
	assert.Equal(t, "running", health.Container)
	assert.True(t, health.Munge)
	assert.True(t, health.SSHD)
	assert.False(t, health.Services["slurmctld"])
}

func TestGetNodeHealth_ComputeNotRunning(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if len(args) >= 2 && args[0] == "inspect" {
			return docker.MockResult{Stdout: statusInspectJSON("sind-dev-compute-0", "exited", "")}
		}
		return docker.MockResult{Err: fmt.Errorf("container not running")}
	}
	c := docker.NewClient(&m)

	health, err := GetNodeHealth(context.Background(), c, "sind-dev-compute-0", "compute")

	require.NoError(t, err)
	assert.Equal(t, "exited", health.Container)
	assert.False(t, health.Munge)
	assert.False(t, health.SSHD)
	require.Contains(t, health.Services, "slurmd")
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

// --- GetNetworkHealth ---

// exitCode1 is defined in preflight_test.go.

func TestGetNetworkHealth_AllHealthy(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil) // NetworkExists: sind-mesh
	m.AddResult("[{}]\n", "", nil) // ContainerExists: sind-dns
	m.AddResult("[{}]\n", "", nil) // NetworkExists: sind-dev-net
	c := docker.NewClient(&m)

	health, err := GetNetworkHealth(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.True(t, health.Mesh)
	assert.True(t, health.DNS)
	assert.True(t, health.Cluster)

	// Verify correct resources were checked.
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"network", "inspect", "sind-mesh"}, m.Calls[0].Args)
	assert.Equal(t, []string{"container", "inspect", "sind-dns"}, m.Calls[1].Args)
	assert.Equal(t, []string{"network", "inspect", "sind-dev-net"}, m.Calls[2].Args)
}

func TestGetNetworkHealth_NoneExist(t *testing.T) {
	var m docker.MockExecutor
	notFound := &exec.ExitError{ProcessState: exitCode1(t)}
	m.AddResult("", "Error: No such network\n", notFound)   // mesh
	m.AddResult("", "Error: No such container\n", notFound) // dns
	m.AddResult("", "Error: No such network\n", notFound)   // cluster net
	c := docker.NewClient(&m)

	health, err := GetNetworkHealth(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.False(t, health.Mesh)
	assert.False(t, health.DNS)
	assert.False(t, health.Cluster)
}

func TestGetNetworkHealth_PartialHealth(t *testing.T) {
	var m docker.MockExecutor
	notFound := &exec.ExitError{ProcessState: exitCode1(t)}
	m.AddResult("[{}]\n", "", nil)                        // mesh exists
	m.AddResult("[{}]\n", "", nil)                        // dns exists
	m.AddResult("", "Error: No such network\n", notFound) // cluster net missing
	c := docker.NewClient(&m)

	health, err := GetNetworkHealth(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.True(t, health.Mesh)
	assert.True(t, health.DNS)
	assert.False(t, health.Cluster)
}

func TestGetNetworkHealth_MeshCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestGetNetworkHealth_DNSCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil)                         // mesh OK
	m.AddResult("", "", fmt.Errorf("docker daemon error")) // dns error
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking DNS container")
}

func TestGetNetworkHealth_ClusterNetCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil)                         // mesh OK
	m.AddResult("[{}]\n", "", nil)                         // dns OK
	m.AddResult("", "", fmt.Errorf("docker daemon error")) // cluster net error
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking cluster network")
}

func TestGetNetworkHealth_DefaultCluster(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil) // mesh
	m.AddResult("[{}]\n", "", nil) // dns
	m.AddResult("[{}]\n", "", nil) // cluster net
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(context.Background(), c, "default")

	require.NoError(t, err)
	// Verify cluster network name uses default.
	assert.Equal(t, []string{"network", "inspect", "sind-default-net"}, m.Calls[2].Args)
}

// --- GetVolumeHealth ---

func TestGetVolumeHealth_AllExist(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil) // config
	m.AddResult("[{}]\n", "", nil) // munge
	m.AddResult("[{}]\n", "", nil) // data
	c := docker.NewClient(&m)

	health, err := GetVolumeHealth(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.True(t, health.Config)
	assert.True(t, health.Munge)
	assert.True(t, health.Data)

	// Verify correct volume names were checked.
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"volume", "inspect", "sind-dev-config"}, m.Calls[0].Args)
	assert.Equal(t, []string{"volume", "inspect", "sind-dev-munge"}, m.Calls[1].Args)
	assert.Equal(t, []string{"volume", "inspect", "sind-dev-data"}, m.Calls[2].Args)
}

func TestGetVolumeHealth_NoneExist(t *testing.T) {
	var m docker.MockExecutor
	notFound := &exec.ExitError{ProcessState: exitCode1(t)}
	m.AddResult("", "Error: No such volume\n", notFound) // config
	m.AddResult("", "Error: No such volume\n", notFound) // munge
	m.AddResult("", "Error: No such volume\n", notFound) // data
	c := docker.NewClient(&m)

	health, err := GetVolumeHealth(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.False(t, health.Config)
	assert.False(t, health.Munge)
	assert.False(t, health.Data)
}

func TestGetVolumeHealth_PartialExist(t *testing.T) {
	var m docker.MockExecutor
	notFound := &exec.ExitError{ProcessState: exitCode1(t)}
	m.AddResult("[{}]\n", "", nil)                       // config exists
	m.AddResult("", "Error: No such volume\n", notFound) // munge missing
	m.AddResult("[{}]\n", "", nil)                       // data exists
	c := docker.NewClient(&m)

	health, err := GetVolumeHealth(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.True(t, health.Config)
	assert.False(t, health.Munge)
	assert.True(t, health.Data)
}

func TestGetVolumeHealth_CheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon error"))
	c := docker.NewClient(&m)

	_, err := GetVolumeHealth(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume sind-dev-config")
}

func TestGetVolumeHealth_DefaultCluster(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil) // config
	m.AddResult("[{}]\n", "", nil) // munge
	m.AddResult("[{}]\n", "", nil) // data
	c := docker.NewClient(&m)

	_, err := GetVolumeHealth(context.Background(), c, "default")

	require.NoError(t, err)
	assert.Equal(t, []string{"volume", "inspect", "sind-default-config"}, m.Calls[0].Args)
	assert.Equal(t, []string{"volume", "inspect", "sind-default-munge"}, m.Calls[1].Args)
	assert.Equal(t, []string{"volume", "inspect", "sind-default-data"}, m.Calls[2].Args)
}

// --- GetStatus ---

// fullStatusOnCall returns a mock dispatcher for GetStatus with all healthy nodes.
func fullStatusOnCall(t *testing.T) func([]string, string) docker.MockResult {
	t.Helper()
	return func(args []string, _ string) docker.MockResult {
		if len(args) == 0 {
			return docker.MockResult{Err: fmt.Errorf("empty args")}
		}

		// docker ps (ListContainers)
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ndjson(
				psEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller",
				},
				psEntry{
					ID: "b", Names: "sind-dev-compute-0", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=compute",
				},
				psEntry{
					ID: "c", Names: "sind-dev-compute-1", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=compute",
				},
			)}
		}

		// docker inspect (container)
		if args[0] == "inspect" {
			name := args[1]
			var ip string
			switch name {
			case "sind-dev-controller":
				ip = "172.18.0.2"
			case "sind-dev-compute-0":
				ip = "172.18.0.3"
			case "sind-dev-compute-1":
				ip = "172.18.0.4"
			}
			return docker.MockResult{Stdout: statusInspectJSON(name, "running", ip)}
		}

		// docker exec: service checks (all pass)
		if args[0] == "exec" {
			if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
				return docker.MockResult{Stdout: "active\n"}
			}
			if len(args) >= 3 && args[2] == "sh" {
				return docker.MockResult{Stdout: "running\n"}
			}
			if len(args) >= 3 && args[2] == "bash" {
				return docker.MockResult{Stdout: "SSH-2.0-OpenSSH_9.8\n"}
			}
			if len(args) >= 3 && args[2] == "scontrol" {
				return docker.MockResult{Stdout: "Slurmctld(primary) is UP\n"}
			}
		}

		// docker network inspect / container inspect / volume inspect
		if args[0] == "network" && args[1] == "inspect" {
			return docker.MockResult{Stdout: "[{}]\n"}
		}
		if args[0] == "container" && args[1] == "inspect" {
			return docker.MockResult{Stdout: "[{}]\n"}
		}
		if args[0] == "volume" && args[1] == "inspect" {
			return docker.MockResult{Stdout: "[{}]\n"}
		}

		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

func TestGetStatus_Full(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = fullStatusOnCall(t)
	c := docker.NewClient(&m)

	status, err := GetStatus(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.Equal(t, "dev", status.Name)
	assert.Equal(t, StatusRunning, status.Status)

	// Nodes sorted: controller, compute-0, compute-1.
	require.Len(t, status.Nodes, 3)
	assert.Equal(t, "controller.dev", status.Nodes[0].Name)
	assert.Equal(t, "controller", status.Nodes[0].Role)
	assert.Equal(t, "running", status.Nodes[0].Health.Container)
	assert.Equal(t, "172.18.0.2", status.Nodes[0].Health.IP)
	assert.True(t, status.Nodes[0].Health.Munge)
	assert.True(t, status.Nodes[0].Health.SSHD)
	assert.True(t, status.Nodes[0].Health.Services["slurmctld"])

	assert.Equal(t, "compute-0.dev", status.Nodes[1].Name)
	assert.Equal(t, "compute", status.Nodes[1].Role)
	assert.True(t, status.Nodes[1].Health.Services["slurmd"])

	assert.Equal(t, "compute-1.dev", status.Nodes[2].Name)

	// Network
	assert.True(t, status.Network.Mesh)
	assert.True(t, status.Network.DNS)
	assert.True(t, status.Network.Cluster)

	// Volumes
	assert.True(t, status.Volumes.Config)
	assert.True(t, status.Volumes.Munge)
	assert.True(t, status.Volumes.Data)
}

func TestGetStatus_Empty(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ""}
		}
		if (args[0] == "network" || args[0] == "volume") && args[1] == "inspect" {
			return docker.MockResult{Stdout: "[{}]\n"}
		}
		if args[0] == "container" && args[1] == "inspect" {
			return docker.MockResult{Stdout: "[{}]\n"}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	c := docker.NewClient(&m)

	status, err := GetStatus(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.Equal(t, "dev", status.Name)
	assert.Equal(t, StatusUnknown, status.Status)
	assert.Empty(t, status.Nodes)
}

func TestGetStatus_ListError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetStatus(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestGetStatus_NodeHealthError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ndjson(
				psEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller",
				},
			)}
		}
		// Inspect fails
		if args[0] == "inspect" {
			return docker.MockResult{Err: fmt.Errorf("inspect failed")}
		}
		return docker.MockResult{Err: fmt.Errorf("unexpected: %v", args)}
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking node controller")
}

func TestGetStatus_NetworkHealthError(t *testing.T) {
	var m docker.MockExecutor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// Mesh network check fails.
		if args[0] == "network" && args[1] == "inspect" {
			return docker.MockResult{Err: fmt.Errorf("docker daemon error")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestGetStatus_VolumeHealthError(t *testing.T) {
	var m docker.MockExecutor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// Volume check fails.
		if args[0] == "volume" && args[1] == "inspect" {
			return docker.MockResult{Err: fmt.Errorf("docker daemon error")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume")
}

func TestGetStatus_SortOrder(t *testing.T) {
	var m docker.MockExecutor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// Return nodes in non-sorted order including submitter.
		if args[0] == "ps" {
			return docker.MockResult{Stdout: ndjson(
				psEntry{
					ID: "b", Names: "sind-dev-compute-0", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=compute",
				},
				psEntry{
					ID: "c", Names: "sind-dev-submitter", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=submitter",
				},
				psEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller",
				},
			)}
		}
		if args[0] == "inspect" {
			name := args[1]
			var ip string
			switch name {
			case "sind-dev-controller":
				ip = "172.18.0.2"
			case "sind-dev-submitter":
				ip = "172.18.0.4"
			case "sind-dev-compute-0":
				ip = "172.18.0.3"
			}
			return docker.MockResult{Stdout: statusInspectJSON(name, "running", ip)}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	status, err := GetStatus(context.Background(), c, "dev")

	require.NoError(t, err)
	require.Len(t, status.Nodes, 3)
	assert.Equal(t, "controller", status.Nodes[0].Role)
	assert.Equal(t, "submitter", status.Nodes[1].Role)
	assert.Equal(t, "compute", status.Nodes[2].Role)
}
