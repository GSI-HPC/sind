// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statusInspectJSON(name, status, ip string) string {
	return "[" + statusInspectEntry(name, status, ip) + "]"
}

func statusInspectEntry(name, status, ip string) string {
	return statusInspectEntryLabels(name, status, ip, docker.Labels{})
}

func statusInspectEntryLabels(name, status, ip string, labels docker.Labels) string {
	labelsJSON, _ := json.Marshal(labels)
	return fmt.Sprintf(`{
  "Id": "abc123",
  "Name": "/%s",
  "State": {"Status": %q},
  "Config": {"Labels": %s},
  "NetworkSettings": {"Networks": {"sind-dev-net": {"IPAddress": %q}}}
}`, name, status, labelsJSON, ip)
}

// statusInspectJSONBatch builds a docker inspect JSON response covering every
// container name in inspectArgs[1:]. resolver maps each name to (status, ip).
// Used by mock dispatchers when GetStatus issues a batched inspect.
func statusInspectJSONBatch(inspectArgs []string, resolver func(name string) (status, ip string)) string {
	entries := make([]string, 0, len(inspectArgs)-1)
	for _, name := range inspectArgs[1:] {
		status, ip := resolver(name)
		entries = append(entries, statusInspectEntry(name, status, ip))
	}
	return "[" + strings.Join(entries, ",") + "]"
}

// healthyOnCall returns a mock dispatcher where all checks pass.
// Failed checks can be overridden by wrapping this function.
func healthyOnCall(containerName, ip string) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		if len(args) >= 2 && args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSON(containerName, "running", ip)}
		}
		// probe.Snapshot uses a fused "systemctl is-active <units...>"
		// call whose stdout is one state line per unit.
		if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
			var b strings.Builder
			for range args[4:] {
				b.WriteString("active\n")
			}
			return mock.Result{Stdout: b.String()}
		}
		if len(args) >= 3 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

// fusedIsActiveResponse emits one state line per unit in args[4:].
// Units listed in failing produce "inactive" lines and the response carries
// a non-zero exit error (matching real systemctl behaviour when any unit
// is inactive). All other units emit "active".
func fusedIsActiveResponse(t *testing.T, args []string, failing ...string) mock.Result {
	t.Helper()
	fail := make(map[string]bool, len(failing))
	for _, u := range failing {
		fail[u] = true
	}
	var b strings.Builder
	anyFailed := false
	for _, u := range args[4:] {
		if fail[u] {
			b.WriteString("inactive\n")
			anyFailed = true
			continue
		}
		b.WriteString("active\n")
	}
	res := mock.Result{Stdout: b.String()}
	if anyFailed {
		res.Err = testutil.ExitCode1(t)
	}
	return res
}

// inspectedNodeHealth inspects the container, as sind get node does, and
// runs GetNodeHealth on the result for cluster dev in the default realm.
func inspectedNodeHealth(t *testing.T, c *docker.Client, containerName string, role config.Role) (*NodeHealth, error) {
	t.Helper()
	info, err := c.InspectContainer(t.Context(), docker.ContainerName(containerName))
	require.NoError(t, err)
	return GetNodeHealth(t.Context(), c, info, role, mesh.DefaultRealm, "dev")
}

func TestGetNodeHealth_Controller(t *testing.T) {
	var m mock.Executor
	m.OnCall = healthyOnCall("sind-dev-controller", "172.18.0.2")
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-controller", config.RoleController)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.Equal(t, "172.18.0.2", health.IP)
	assert.True(t, health.Services[probe.ServiceMunge])
	assert.True(t, health.Services[probe.ServiceSSHD])
	require.Contains(t, health.Services, probe.ServiceSlurmctld)
	assert.True(t, health.Services[probe.ServiceSlurmctld])
}

func TestGetNodeHealth_Compute(t *testing.T) {
	var m mock.Executor
	m.OnCall = healthyOnCall("sind-dev-worker-0", "172.18.0.3")
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-worker-0", config.RoleWorker)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.Equal(t, "172.18.0.3", health.IP)
	assert.True(t, health.Services[probe.ServiceMunge])
	assert.True(t, health.Services[probe.ServiceSSHD])
	require.Contains(t, health.Services, probe.ServiceSlurmd)
	assert.True(t, health.Services[probe.ServiceSlurmd])
}

func TestGetNodeHealth_Submitter(t *testing.T) {
	var m mock.Executor
	m.OnCall = healthyOnCall("sind-dev-submitter", "172.18.0.4")
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-submitter", config.RoleSubmitter)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.True(t, health.Services[probe.ServiceMunge])
	assert.True(t, health.Services[probe.ServiceSSHD])
	// Submitters have no role-specific Slurm service (only munge + sshd).
	assert.NotContains(t, health.Services, probe.ServiceSlurmctld)
	assert.NotContains(t, health.Services, probe.ServiceSlurmd)
}

func TestGetNodeHealth_UnmanagedWorker(t *testing.T) {
	var m mock.Executor
	base := healthyOnCall("sind-dev-worker-0", "172.18.0.3")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) >= 2 && args[0] == "inspect" {
			return mock.Result{Stdout: "[" + statusInspectEntryLabels("sind-dev-worker-0", "running", "172.18.0.3",
				docker.Labels{LabelRole: "worker", LabelManaged: "false"}) + "]"}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-worker-0", config.RoleWorker)

	require.NoError(t, err)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true}, health.Services,
		"slurmd is not sind's on an unmanaged worker")
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "systemctl", "is-active", "munge", "sshd"}, m.Calls[1].Args)
}

func TestGetNodeHealth_DB(t *testing.T) {
	var m mock.Executor
	m.OnCall = healthyOnCall("sind-dev-db", "172.18.0.5")
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-db", config.RoleDB)

	require.NoError(t, err)
	assert.Equal(t, ServiceHealth{
		probe.ServiceMunge: true, probe.ServiceSSHD: true,
		probe.ServiceMariadb: true, probe.ServiceSlurmdbd: true,
	}, health.Services)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"exec", "sind-dev-db", "systemctl", "is-active", "munge", "sshd", "mariadb", "slurmdbd"}, m.Calls[1].Args)
}

func TestGetNodeHealth_UnmanagedDB(t *testing.T) {
	var m mock.Executor
	base := healthyOnCall("sind-dev-db", "172.18.0.5")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) >= 2 && args[0] == "inspect" {
			return mock.Result{Stdout: "[" + statusInspectEntryLabels("sind-dev-db", "running", "172.18.0.5",
				docker.Labels{LabelRole: "db", LabelManaged: "false"}) + "]"}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-db", config.RoleDB)

	require.NoError(t, err)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true}, health.Services,
		"mariadb and slurmdbd are not sind's on an unmanaged db node")
}

func TestGetNodeHealth_UnmanagedNotRunning(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) >= 2 && args[0] == "inspect" {
			return mock.Result{Stdout: "[" + statusInspectEntryLabels("sind-dev-worker-0", "exited", "",
				docker.Labels{LabelManaged: "false"}) + "]"}
		}
		return mock.Result{Err: fmt.Errorf("container not running")}
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-worker-0", config.RoleWorker)

	require.NoError(t, err)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: false, probe.ServiceSSHD: false}, health.Services)
}

func TestGetNodeHealth_ContainerNotRunning(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) >= 2 && args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSON("sind-dev-controller", "exited", "")}
		}
		return mock.Result{Err: fmt.Errorf("container not running")}
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-controller", config.RoleController)

	require.NoError(t, err)
	assert.Equal(t, docker.StateExited, health.State)
	assert.False(t, health.Services[probe.ServiceMunge])
	assert.False(t, health.Services[probe.ServiceSSHD])
	assert.False(t, health.Services[probe.ServiceSlurmctld])
}

func TestGetNodeHealth_ServiceFailing(t *testing.T) {
	var m mock.Executor
	base := healthyOnCall("sind-dev-worker-0", "172.18.0.3")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
			return fusedIsActiveResponse(t, args, "slurmd")
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-worker-0", config.RoleWorker)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.True(t, health.Services[probe.ServiceMunge])
	assert.True(t, health.Services[probe.ServiceSSHD])
	assert.False(t, health.Services[probe.ServiceSlurmd])
}

func TestGetNodeHealth_SlurmctldFailing(t *testing.T) {
	var m mock.Executor
	base := healthyOnCall("sind-dev-controller", "172.18.0.2")
	m.OnCall = func(args []string, stdin string) mock.Result {
		// scontrol ping fails
		if len(args) >= 3 && args[2] == "scontrol" {
			return mock.Result{Err: fmt.Errorf("exit status 1")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-controller", config.RoleController)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.True(t, health.Services[probe.ServiceMunge])
	assert.True(t, health.Services[probe.ServiceSSHD])
	assert.False(t, health.Services[probe.ServiceSlurmctld])
}

func TestGetNodeHealth_ComputeNotRunning(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if len(args) >= 2 && args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSON("sind-dev-worker-0", "exited", "")}
		}
		return mock.Result{Err: fmt.Errorf("container not running")}
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-worker-0", config.RoleWorker)

	require.NoError(t, err)
	assert.Equal(t, docker.StateExited, health.State)
	assert.False(t, health.Services[probe.ServiceMunge])
	assert.False(t, health.Services[probe.ServiceSSHD])
	require.Contains(t, health.Services, probe.ServiceSlurmd)
	assert.False(t, health.Services[probe.ServiceSlurmd])
}

func TestGetNodeHealth_MungeFailing(t *testing.T) {
	var m mock.Executor
	base := healthyOnCall("sind-dev-controller", "172.18.0.2")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
			return fusedIsActiveResponse(t, args, "munge")
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-controller", config.RoleController)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.False(t, health.Services[probe.ServiceMunge])
	assert.True(t, health.Services[probe.ServiceSSHD])
	assert.True(t, health.Services[probe.ServiceSlurmctld])
}

func TestGetNodeHealth_SSHDFailing(t *testing.T) {
	var m mock.Executor
	base := healthyOnCall("sind-dev-controller", "172.18.0.2")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
			return fusedIsActiveResponse(t, args, "sshd")
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-controller", config.RoleController)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.True(t, health.Services[probe.ServiceMunge])
	assert.False(t, health.Services[probe.ServiceSSHD])
	assert.True(t, health.Services[probe.ServiceSlurmctld])
}

// TestGetNodeHealth_ProbeError covers a readiness exec that fails outright,
// e.g. when docker cannot reach the daemon: every service reads unhealthy.
func TestGetNodeHealth_ProbeError(t *testing.T) {
	var m mock.Executor
	base := healthyOnCall("sind-dev-worker-0", "172.18.0.3")
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "exec" {
			return mock.Result{Err: fmt.Errorf("docker daemon unreachable")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	health, err := inspectedNodeHealth(t, c, "sind-dev-worker-0", config.RoleWorker)

	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, health.State)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: false, probe.ServiceSSHD: false, probe.ServiceSlurmd: false}, health.Services)
}

func TestGetNodeHealth_MultipleIPs(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, stdin string) mock.Result {
		if len(args) >= 2 && args[0] == "inspect" {
			return mock.Result{Stdout: `[{
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

	health, err := inspectedNodeHealth(t, c, "sind-dev-controller", config.RoleController)

	require.NoError(t, err)
	assert.Equal(t, "172.18.0.2", health.IP)
}

// --- GetNetworkHealth ---

func netInspect(name, subnet, gw string) string {
	return fmt.Sprintf(`[{"Name":%q,"Driver":"bridge","IPAM":{"Config":[{"Subnet":%q,"Gateway":%q}]}}]`, name, subnet, gw)
}

func TestGetNetworkHealth_AllHealthy(t *testing.T) {
	var m mock.Executor
	m.AddResult(netInspect("sind-mesh", "172.19.0.0/16", "172.19.0.1"), "", nil)    // InspectNetwork: mesh
	m.AddResult("[{}]\n", "", nil)                                                  // InspectContainer: sind-dns
	m.AddResult(netInspect("sind-dev-net", "172.18.0.0/16", "172.18.0.1"), "", nil) // InspectNetwork: cluster
	c := docker.NewClient(&m)

	health, err := GetNetworkHealth(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.True(t, health.Mesh)
	assert.Equal(t, "sind-mesh", health.MeshName)
	assert.Equal(t, "bridge", health.MeshDriver)
	assert.Equal(t, "172.19.0.0/16", health.MeshSubnet)
	assert.Equal(t, "172.19.0.1", health.MeshGateway)
	assert.True(t, health.DNS)
	assert.Equal(t, "sind-dns", health.DNSName)
	assert.True(t, health.Cluster)
	assert.Equal(t, "sind-dev-net", health.ClusterName)
	assert.Equal(t, "bridge", health.ClusterDriver)
	assert.Equal(t, "172.18.0.0/16", health.ClusterSubnet)
	assert.Equal(t, "172.18.0.1", health.ClusterGateway)
}

func TestGetNetworkHealth_NoneExist(t *testing.T) {
	var m mock.Executor
	notFound := testutil.ExitCode1(t)
	m.AddResult("", "Error: No such network\n", notFound)   // mesh
	m.AddResult("", "Error: No such container\n", notFound) // dns
	m.AddResult("", "Error: No such network\n", notFound)   // cluster net
	c := docker.NewClient(&m)

	health, err := GetNetworkHealth(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.False(t, health.Mesh)
	assert.Equal(t, "sind-mesh", health.MeshName)
	assert.False(t, health.DNS)
	assert.Equal(t, "sind-dns", health.DNSName)
	assert.False(t, health.Cluster)
	assert.Equal(t, "sind-dev-net", health.ClusterName)
}

func TestGetNetworkHealth_PartialHealth(t *testing.T) {
	var m mock.Executor
	notFound := testutil.ExitCode1(t)
	m.AddResult(netInspect("sind-mesh", "172.19.0.0/16", "172.19.0.1"), "", nil) // inspect mesh
	m.AddResult("[{}]\n", "", nil)                                               // inspect dns
	m.AddResult("", "Error: No such network\n", notFound)                        // cluster net missing
	c := docker.NewClient(&m)

	health, err := GetNetworkHealth(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.True(t, health.Mesh)
	assert.True(t, health.DNS)
	assert.False(t, health.Cluster)
}

func TestGetNetworkHealth_MeshCheckError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestGetNetworkHealth_DNSCheckError(t *testing.T) {
	var m mock.Executor
	m.AddResult(netInspect("sind-mesh", "172.19.0.0/16", "172.19.0.1"), "", nil) // inspect mesh
	m.AddResult("", "", fmt.Errorf("docker daemon error"))                       // dns error
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking DNS container")
}

func TestGetNetworkHealth_ClusterNetCheckError(t *testing.T) {
	var m mock.Executor
	m.AddResult(netInspect("sind-mesh", "172.19.0.0/16", "172.19.0.1"), "", nil) // inspect mesh
	m.AddResult("[{}]\n", "", nil)                                               // inspect dns
	m.AddResult("", "", fmt.Errorf("docker daemon error"))                       // cluster net error
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking cluster network")
}

func TestGetNetworkHealth_DefaultCluster(t *testing.T) {
	var m mock.Executor
	m.AddResult(netInspect("sind-mesh", "172.19.0.0/16", "172.19.0.1"), "", nil)        // inspect mesh
	m.AddResult("[{}]\n", "", nil)                                                      // inspect dns
	m.AddResult(netInspect("sind-default-net", "172.18.0.0/16", "172.18.0.1"), "", nil) // inspect cluster
	c := docker.NewClient(&m)

	_, err := GetNetworkHealth(t.Context(), c, mesh.DefaultRealm, "default")

	require.NoError(t, err)
	// Verify cluster network name uses default.
	assert.Equal(t, []string{"network", "inspect", "sind-default-net"}, m.Calls[2].Args)
}

// --- GetMountPoints ---

// addVolumeLs queues docker volume ls output listing the named volumes.
func addVolumeLs(m *mock.Executor, names ...string) {
	m.AddResult(volumeLs(names...).Stdout, "", nil)
}

func TestGetMountPoints_AllVolumes(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-config", "sind-dev-munge", "sind-dev-data")
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller", Labels: docker.Labels{"sind.role": "controller"}},
	}
	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, "/etc/slurm", mounts[0].Path)
	assert.Equal(t, "sind-dev-config", mounts[0].Source)
	assert.Equal(t, config.StorageVolume, mounts[0].Type)
	assert.True(t, mounts[0].OK)
	assert.Equal(t, "/etc/munge", mounts[1].Path)
	assert.True(t, mounts[1].OK)
	assert.Equal(t, "/data", mounts[2].Path)
	assert.Equal(t, "sind-dev-data", mounts[2].Source)
	assert.Equal(t, config.StorageVolume, mounts[2].Type)
	assert.True(t, mounts[2].OK)

	// One listing of the cluster's volumes instead of an inspect per volume.
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"volume", "ls", "--format", "json", "--filter", "name=sind-dev-"}, m.Calls[0].Args)
}

func TestGetMountPoints_BackupControllerState(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-config", "sind-dev-munge", "sind-dev-data", "sind-dev-state")
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller", Labels: docker.Labels{"sind.role": "controller"}},
		{Name: "sind-dev-controller-backup", Labels: docker.Labels{"sind.role": "controller"}},
	}
	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

	require.NoError(t, err)
	require.Len(t, mounts, 4)
	assert.Equal(t, MountPoint{Path: "/var/spool/slurmctld", Source: "sind-dev-state", Type: config.StorageVolume, OK: true}, mounts[3])
}

func TestGetMountPoints_HostPath(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-config", "sind-dev-munge")
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller", Labels: docker.Labels{
			"sind.role":          "controller",
			"sind.data.hostpath": "/home/user/project",
		}},
	}
	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, "/data", mounts[2].Path)
	assert.Equal(t, "/home/user/project", mounts[2].Source)
	assert.Equal(t, config.StorageHostPath, mounts[2].Type)
	assert.True(t, mounts[2].OK)
	assert.Len(t, m.Calls, 1)
}

func TestGetMountPoints_CVMFSHostPath(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-config", "sind-dev-munge", "sind-dev-data")
	// no check for the host's /cvmfs
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller", Labels: docker.Labels{"sind.role": "controller", "sind.cvmfs": "hostPath"}},
		{Name: "sind-dev-worker-0", Labels: docker.Labels{"sind.role": "worker", "sind.cvmfs": "hostPath"}},
	}
	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

	require.NoError(t, err)
	require.Len(t, mounts, 4)
	assert.Equal(t, MountPoint{Path: "/cvmfs", Source: "/cvmfs", Type: config.StorageHostPath, OK: true}, mounts[3])
	assert.Len(t, m.Calls, 1)
}

func TestGetMountPoints_CVMFSVolume(t *testing.T) {
	tests := []struct {
		name   string
		result mock.Result
		wantOK bool
	}{
		{"exists", mock.Result{Stdout: "[{}]\n"}, true},
		{"missing", mock.Result{Stderr: "Error: No such volume: cvmfs\n", Err: testutil.ExitCode1(t)}, false},
		// A lookup the daemon cannot answer, e.g. with the plugin
		// disabled, marks the mount missing instead of failing.
		{"lookup error", mock.Result{Stderr: "plugin \"cvmfs\" not found\n", Err: fmt.Errorf("exit status 1")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			addVolumeLs(&m, "sind-dev-config", "sind-dev-munge", "sind-dev-data")
			m.AddResult(tt.result.Stdout, tt.result.Stderr, tt.result.Err)
			c := docker.NewClient(&m)

			containers := []docker.ContainerListEntry{
				{Name: "sind-dev-controller", Labels: docker.Labels{"sind.role": "controller", "sind.cvmfs": "volume"}},
			}
			mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

			require.NoError(t, err)
			require.Len(t, mounts, 4)
			assert.Equal(t, MountPoint{Path: "/cvmfs", Source: "cvmfs", Type: config.StorageVolume, OK: tt.wantOK}, mounts[3])
			assert.Equal(t, []string{"volume", "inspect", "cvmfs"}, m.Calls[1].Args)
		})
	}
}

func TestGetMountPoints_CustomMountPath(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-config", "sind-dev-munge", "sind-dev-data")
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller", Labels: docker.Labels{
			"sind.role":           "controller",
			"sind.data.mountpath": "/shared",
		}},
	}
	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", containers)

	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.Equal(t, "/shared", mounts[2].Path)
	assert.Equal(t, "sind-dev-data", mounts[2].Source)
}

func TestGetMountPoints_NoneExist(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m)
	c := docker.NewClient(&m)

	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", nil)

	require.NoError(t, err)
	assert.False(t, mounts[0].OK)
	assert.False(t, mounts[1].OK)
	assert.False(t, mounts[2].OK)
}

// TestGetMountPoints_ExactNames checks that only the cluster's own volume
// names count: the name filter also lists those of cluster "dev-2".
func TestGetMountPoints_ExactNames(t *testing.T) {
	var m mock.Executor
	addVolumeLs(&m, "sind-dev-2-config", "sind-dev-2-munge", "sind-dev-2-data", "sind-dev-munge")
	c := docker.NewClient(&m)

	mounts, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", nil)

	require.NoError(t, err)
	require.Len(t, mounts, 3)
	assert.False(t, mounts[0].OK, "config")
	assert.True(t, mounts[1].OK, "munge")
	assert.False(t, mounts[2].OK, "data")
}

func TestGetMountPoints_CheckError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("docker daemon error"))
	c := docker.NewClient(&m)

	_, err := GetMountPoints(t.Context(), c, mesh.DefaultRealm, "dev", nil)

	require.Error(t, err)
	assert.Equal(t, "checking volumes: docker daemon error", err.Error())
}

// --- GetStatus ---

func TestGetStatus_UnmanagedNodes(t *testing.T) {
	labels := map[string]docker.Labels{
		"sind-dev-controller": {LabelRole: "controller", LabelManaged: "true"},
		"sind-dev-worker-0":   {LabelRole: "worker", LabelManaged: "false"},
	}
	var m mock.Executor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		switch args[0] {
		case "ps":
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller,sind.managed=true"},
				testutil.PsEntry{ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=worker,sind.managed=false"},
			)}
		case "inspect":
			entries := make([]string, 0, len(args)-1)
			for _, name := range args[1:] {
				entries = append(entries, statusInspectEntryLabels(name, "running", "172.18.0.9", labels[name]))
			}
			return mock.Result{Stdout: "[" + strings.Join(entries, ",") + "]"}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	status, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, status.Nodes, 2)
	assert.True(t, status.Nodes[0].Managed)
	assert.Contains(t, status.Nodes[0].Health.Services, probe.ServiceSlurmctld)
	assert.False(t, status.Nodes[1].Managed)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true}, status.Nodes[1].Health.Services)
}

// fullStatusOnCall returns a mock dispatcher for GetStatus with all healthy nodes.
func fullStatusOnCall(t *testing.T) func([]string, string) mock.Result {
	t.Helper()
	return func(args []string, _ string) mock.Result {
		if len(args) == 0 {
			return mock.Result{Err: fmt.Errorf("empty args")}
		}

		// docker ps (ListContainers)
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=25.11.8",
				},
				testutil.PsEntry{
					ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=25.11.8",
				},
				testutil.PsEntry{
					ID: "c", Names: "sind-dev-worker-1", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=25.11.8",
				},
			)}
		}

		// docker inspect (container) — batched across all containers.
		if args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSONBatch(args, func(name string) (string, string) {
				switch name {
				case "sind-dev-controller":
					return "running", "172.18.0.2"
				case "sind-dev-worker-0":
					return "running", "172.18.0.3"
				case "sind-dev-worker-1":
					return "running", "172.18.0.4"
				}
				return "running", ""
			})}
		}

		// docker exec: service checks (all pass)
		if args[0] == "exec" {
			if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
				var b strings.Builder
				for range args[4:] {
					b.WriteString("active\n")
				}
				return mock.Result{Stdout: b.String()}
			}
			if len(args) >= 3 && args[2] == "scontrol" {
				return mock.Result{Stdout: "Slurmctld(primary) is UP\n"}
			}
		}

		// docker network inspect / volume ls
		if args[0] == "network" && args[1] == "inspect" {
			return mock.Result{Stdout: "[{}]\n"}
		}
		if args[0] == "volume" && args[1] == "ls" {
			return volumeLs("sind-dev-config", "sind-dev-munge", "sind-dev-data")
		}

		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

func TestGetStatus_Full(t *testing.T) {
	var m mock.Executor
	m.OnCall = fullStatusOnCall(t)
	c := docker.NewClient(&m)

	status, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, "dev", status.Name)
	assert.Equal(t, "25.11.8", status.SlurmVersion)
	assert.Equal(t, StateRunning, status.State)

	// Nodes sorted: controller, worker-0, worker-1.
	require.Len(t, status.Nodes, 3)
	assert.Equal(t, "controller.dev", status.Nodes[0].Name)
	assert.Equal(t, config.RoleController, status.Nodes[0].Role)
	assert.Equal(t, docker.StateRunning, status.Nodes[0].Health.State)
	assert.Equal(t, "172.18.0.2", status.Nodes[0].Health.IP)
	assert.True(t, status.Nodes[0].Health.Services[probe.ServiceMunge])
	assert.True(t, status.Nodes[0].Health.Services[probe.ServiceSSHD])
	assert.True(t, status.Nodes[0].Health.Services[probe.ServiceSlurmctld])

	assert.Equal(t, "worker-0.dev", status.Nodes[1].Name)
	assert.Equal(t, config.RoleWorker, status.Nodes[1].Role)
	assert.True(t, status.Nodes[1].Health.Services[probe.ServiceSlurmd])

	assert.Equal(t, "worker-1.dev", status.Nodes[2].Name)

	// Network
	assert.True(t, status.Network.Mesh)
	assert.True(t, status.Network.DNS)
	assert.True(t, status.Network.Cluster)

	// Mounts
	require.Len(t, status.Mounts, 3)
	assert.Equal(t, "/etc/slurm", status.Mounts[0].Path)
	assert.True(t, status.Mounts[0].OK)
	assert.Equal(t, "/etc/munge", status.Mounts[1].Path)
	assert.True(t, status.Mounts[1].OK)
	assert.Equal(t, "/data", status.Mounts[2].Path)
	assert.True(t, status.Mounts[2].OK)
}

// TestGetStatus_EmptyButClusterNetworkExists covers the partial-teardown case:
// no cluster containers remain, but the cluster network still exists. The
// status reports StateEmpty rather than surfacing a not-found error so that
// operators can inspect what's left over.
func TestGetStatus_EmptyButClusterNetworkExists(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		if args[0] == "network" && args[1] == "inspect" {
			return mock.Result{Stdout: "[{}]\n"}
		}
		if args[0] == "volume" && args[1] == "ls" {
			return mock.Result{}
		}
		// InspectContainer used by GetNetworkHealth for the DNS container.
		if args[0] == "inspect" {
			return mock.Result{Stdout: "[{}]\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	c := docker.NewClient(&m)

	status, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, "dev", status.Name)
	assert.Equal(t, StateEmpty, status.State)
	assert.Empty(t, status.Nodes)
}

// TestGetStatus_ClusterNotFound covers the typo/unknown-cluster case: no
// containers exist AND the cluster network is absent. GetStatus must return
// a clear error rather than synthesize a plausible-looking empty report.
func TestGetStatus_ClusterNotFound(t *testing.T) {
	exitErr := notFoundErr(t)
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		if args[0] == "network" && args[1] == "inspect" {
			return mock.Result{Stderr: "Error: No such network: sind-dev-net\n", Err: exitErr}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `cluster "dev" not found`)
	assert.Contains(t, err.Error(), `realm "sind"`)
}

// TestGetStatus_NetworkInspectError covers the branch where no containers
// exist AND the cluster-network inspect fails with something other than
// "not found". The error must be surfaced, not swallowed into a misleading
// "cluster not found".
func TestGetStatus_NetworkInspectError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: ""}
		}
		if args[0] == "network" && args[1] == "inspect" {
			return mock.Result{Err: fmt.Errorf("docker daemon not running")}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking cluster network")
	assert.Contains(t, err.Error(), "docker daemon not running")
}

func TestGetStatus_ListError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestGetStatus_NodeHealthError(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller",
				},
			)}
		}
		// Inspect fails
		if args[0] == "inspect" {
			return mock.Result{Err: fmt.Errorf("inspect failed")}
		}
		return mock.Result{Err: fmt.Errorf("unexpected: %v", args)}
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting cluster containers")
}

func TestGetStatus_NetworkHealthError(t *testing.T) {
	var m mock.Executor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		// Mesh network check fails.
		if args[0] == "network" && args[1] == "inspect" {
			return mock.Result{Err: fmt.Errorf("docker daemon error")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestGetStatus_VolumeHealthError(t *testing.T) {
	var m mock.Executor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		// Volume check fails.
		if args[0] == "volume" && args[1] == "ls" {
			return mock.Result{Err: fmt.Errorf("docker daemon error")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volumes")
}

// TestGetStatus_NetworkErrorBeforeVolumeError checks that when both
// concurrent checks fail, the network error is reported, whichever returns
// first.
func TestGetStatus_NetworkErrorBeforeVolumeError(t *testing.T) {
	var m mock.Executor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if (args[0] == "network" && args[1] == "inspect") || (args[0] == "volume" && args[1] == "ls") {
			return mock.Result{Err: fmt.Errorf("docker daemon error")}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestGetStatus_SortOrder(t *testing.T) {
	var m mock.Executor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		// Return nodes in non-sorted order including submitter.
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{
					ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=worker",
				},
				testutil.PsEntry{
					ID: "c", Names: "sind-dev-submitter", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=submitter",
				},
				testutil.PsEntry{
					ID: "d", Names: "sind-dev-db", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=db",
				},
				testutil.PsEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller",
				},
			)}
		}
		if args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSONBatch(args, func(name string) (string, string) {
				switch name {
				case "sind-dev-controller":
					return "running", "172.18.0.2"
				case "sind-dev-db":
					return "running", "172.18.0.5"
				case "sind-dev-submitter":
					return "running", "172.18.0.4"
				case "sind-dev-worker-0":
					return "running", "172.18.0.3"
				}
				return "running", ""
			})}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	status, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, status.Nodes, 4)
	assert.Equal(t, config.RoleController, status.Nodes[0].Role)
	assert.Equal(t, config.RoleDB, status.Nodes[1].Role)
	assert.Equal(t, config.RoleSubmitter, status.Nodes[2].Role)
	assert.Equal(t, config.RoleWorker, status.Nodes[3].Role)
}

func TestGetStatus_MixedStates(t *testing.T) {
	var m mock.Executor
	base := fullStatusOnCall(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller",
				},
				testutil.PsEntry{
					ID: "b", Names: "sind-dev-worker-0", State: "exited", Image: "img",
					Labels: "sind.cluster=dev,sind.role=worker",
				},
			)}
		}
		if args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSONBatch(args, func(name string) (string, string) {
				switch name {
				case "sind-dev-controller":
					return "running", "172.18.0.2"
				case "sind-dev-worker-0":
					return "exited", ""
				}
				return "running", ""
			})}
		}
		return base(args, stdin)
	}
	c := docker.NewClient(&m)

	status, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, StateMixed, status.State)
	require.Len(t, status.Nodes, 2)
	assert.Equal(t, docker.StateRunning, status.Nodes[0].Health.State)
	assert.Equal(t, docker.StateExited, status.Nodes[1].Health.State)
}

// TestGetStatus_Parallelism uses more nodes than statusNodeConcurrency to
// exercise the bounded fan-out under the race detector, and confirms that
// output ordering is deterministic despite non-deterministic probe
// completion order.
func TestGetStatus_Parallelism(t *testing.T) {
	const nodeCount = statusNodeConcurrency + 4 // force at least one queued batch

	// Build a stable list of worker containers; one controller at the head.
	pss := make([]testutil.PsEntry, 0, nodeCount)
	pss = append(pss, testutil.PsEntry{
		ID: "c", Names: "sind-dev-controller", State: "running", Image: "img",
		Labels: "sind.cluster=dev,sind.role=controller",
	})
	for i := 0; i < nodeCount-1; i++ {
		pss = append(pss, testutil.PsEntry{
			ID:     fmt.Sprintf("w%d", i),
			Names:  fmt.Sprintf("sind-dev-worker-%d", i),
			State:  "running",
			Image:  "img",
			Labels: "sind.cluster=dev,sind.role=worker",
		})
	}

	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(pss...)}
		}
		if args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSONBatch(args, func(string) (string, string) {
				return "running", "172.18.0.1"
			})}
		}
		if args[0] == "exec" && len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
			var b strings.Builder
			for range args[4:] {
				b.WriteString("active\n")
			}
			return mock.Result{Stdout: b.String()}
		}
		if args[0] == "exec" && len(args) >= 3 && args[2] == "scontrol" {
			return mock.Result{Stdout: "Slurmctld(primary) is UP\n"}
		}
		if args[0] == "network" && args[1] == "inspect" {
			return mock.Result{Stdout: "[{}]\n"}
		}
		if args[0] == "volume" && args[1] == "ls" {
			return mock.Result{}
		}
		if args[0] == "inspect" {
			return mock.Result{Stdout: "[{}]\n"}
		}
		return mock.Result{Err: fmt.Errorf("unexpected: %v", args)}
	}
	c := docker.NewClient(&m)

	status, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")
	require.NoError(t, err)
	require.Len(t, status.Nodes, nodeCount)

	// Controller first, workers sorted naturally (worker-0, worker-1, …).
	assert.Equal(t, config.RoleController, status.Nodes[0].Role)
	for i := 1; i < nodeCount; i++ {
		assert.Equal(t, config.RoleWorker, status.Nodes[i].Role)
		assert.Equal(t, fmt.Sprintf("worker-%d.dev", i-1), status.Nodes[i].Name)
	}
}

// TestGetStatus_InspectMissingEntry exercises the defensive branch where the
// batched docker inspect returns fewer entries than requested. In real docker
// this should not happen, but we guard against it to avoid a silent nil deref.
func TestGetStatus_InspectMissingEntry(t *testing.T) {
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		if args[0] == "ps" {
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{
					ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=controller",
				},
				testutil.PsEntry{
					ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img",
					Labels: "sind.cluster=dev,sind.role=worker",
				},
			)}
		}
		// Return only the controller, omitting worker-0 entirely.
		if args[0] == "inspect" {
			return mock.Result{Stdout: statusInspectJSON("sind-dev-controller", "running", "172.18.0.2")}
		}
		return mock.Result{Err: fmt.Errorf("unexpected: %v", args)}
	}
	c := docker.NewClient(&m)

	_, err := GetStatus(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspect returned no entry")
	assert.Len(t, m.Calls, 2, "no probe or check starts")
}

// TestNodeHealth_JSONStatusKey locks in the JSON schema: container state is
// exposed as "status", matching Summary / Status / NodeSummary / NodeDetail.
func TestNodeHealth_JSONStatusKey(t *testing.T) {
	h := NodeHealth{State: docker.StateRunning, IP: "10.0.0.1"}
	data, err := json.Marshal(h)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"status":"running"`)
	assert.NotContains(t, string(data), `"container":`)
}

// TestNodeHealth_JSONServicesMap locks in that munge and sshd live inside the
// services map rather than as flat top-level keys. Flat fields reappearing
// would be a schema regression for get node / get cluster consumers.
func TestNodeHealth_JSONServicesMap(t *testing.T) {
	h := NodeHealth{
		State: docker.StateRunning,
		IP:    "10.0.0.1",
		Services: ServiceHealth{
			probe.ServiceMunge:     true,
			probe.ServiceSSHD:      true,
			probe.ServiceSlurmctld: false,
		},
	}
	data, err := json.Marshal(h)
	require.NoError(t, err)

	// Decode into a dynamic top-level map to verify the shape of the outer
	// object: it must have exactly {status, ip, services} and no flat
	// munge/sshd keys at the top level.
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &top))
	assert.ElementsMatch(t, []string{"status", "ip", "services"}, keys(top))

	// And that munge/sshd live under services.
	var svc map[string]bool
	require.NoError(t, json.Unmarshal(top["services"], &svc))
	assert.True(t, svc["munge"])
	assert.True(t, svc["sshd"])
	assert.False(t, svc["slurmctld"])
}

// TestNodeStatus_JSONManaged locks in that managed is always present, also
// when false.
func TestNodeStatus_JSONManaged(t *testing.T) {
	data, err := json.Marshal(NodeStatus{Name: "worker-0.dev", Role: config.RoleWorker})
	require.NoError(t, err)
	assert.Contains(t, string(data), `"managed":false`)
}

// keys returns the keys of a map sorted for stable assertion errors.
func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
