// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ListClusterResources ---

func TestListClusterResources(t *testing.T) {
	var m docker.MockExecutor
	// ListContainers: returns 2 containers
	m.AddResult(ndjson(
		psEntry{ID: "abc123", Names: "sind-dev-controller", State: "running", Image: "sind-node:latest"},
		psEntry{ID: "def456", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:latest"},
	), "", nil)
	// NetworkExists: sind-dev-net exists
	m.AddResult("", "", nil)
	// VolumeExists: config, munge, data
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, "dev")

	require.NoError(t, err)

	// Containers
	require.Len(t, res.Containers, 2)
	assert.Equal(t, docker.ContainerName("sind-dev-controller"), res.Containers[0].Name)
	assert.Equal(t, docker.ContainerName("sind-dev-worker-0"), res.Containers[1].Name)

	// Network
	assert.Equal(t, docker.NetworkName("sind-dev-net"), res.Network)
	assert.True(t, res.NetworkExists)

	// Volumes
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}, res.Volumes)
}

func TestListClusterResources_NoResources(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	addNotFound(t, &m, 1)    // NetworkExists: not found
	addNotFound(t, &m, 3)    // VolumeExists: config, munge, data
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, "nonexistent")

	require.NoError(t, err)
	assert.Empty(t, res.Containers)
	assert.False(t, res.NetworkExists)
	assert.Empty(t, res.Volumes)
}

func TestListClusterResources_PartialVolumes(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	m.AddResult("", "", nil) // NetworkExists: exists
	m.AddResult("", "", nil) // VolumeExists: config exists
	addNotFound(t, &m, 1)    // VolumeExists: munge missing
	m.AddResult("", "", nil) // VolumeExists: data exists
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, "dev")

	require.NoError(t, err)
	assert.True(t, res.NetworkExists)
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-data"}, res.Volumes)
}

func TestListClusterResources_ListContainersError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker ps failed"))
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestListClusterResources_NetworkCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                  // ListContainers: empty
	m.AddResult("", "", fmt.Errorf("network inspect failed")) // non-exit error
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking network")
}

func TestListClusterResources_VolumeCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                 // ListContainers: empty
	m.AddResult("", "", nil)                                 // NetworkExists: exists
	m.AddResult("", "", fmt.Errorf("volume inspect failed")) // config check fails
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume")
}

func TestListClusterResources_LabelFilter(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	addNotFound(t, &m, 4)    // network + 3 volumes
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, "myCluster")

	require.NoError(t, err)
	require.Len(t, m.Calls, 5)
	// First call is docker ps with label filter
	args := m.Calls[0].Args
	assert.Contains(t, args, "--filter")
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label=sind.cluster=myCluster", args[filterIdx+1])
}

// --- DeleteContainers ---

func TestDeleteContainers(t *testing.T) {
	var m docker.MockExecutor
	// Container 1: stop + rm
	m.AddResult("", "", nil) // stop
	m.AddResult("", "", nil) // rm
	// Container 2: stop + rm
	m.AddResult("", "", nil) // stop
	m.AddResult("", "", nil) // rm
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
		{Name: "sind-dev-worker-0"},
	}
	err := DeleteContainers(t.Context(), c, containers)

	require.NoError(t, err)
	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"stop", "sind-dev-controller"}, m.Calls[0].Args)
	assert.Equal(t, []string{"rm", "sind-dev-controller"}, m.Calls[1].Args)
	assert.Equal(t, []string{"stop", "sind-dev-worker-0"}, m.Calls[2].Args)
	assert.Equal(t, []string{"rm", "sind-dev-worker-0"}, m.Calls[3].Args)
}

func TestDeleteContainers_StopErrorIgnored(t *testing.T) {
	var m docker.MockExecutor
	// stop fails (already stopped) but rm succeeds
	m.AddResult("", "Error\n", fmt.Errorf("container already stopped"))
	m.AddResult("", "", nil) // rm
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeleteContainers(t.Context(), c, containers)

	require.NoError(t, err)
	assert.Len(t, m.Calls, 2)
}

func TestDeleteContainers_RemoveError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                            // stop
	m.AddResult("", "", fmt.Errorf("container in use")) // rm fails
	c := docker.NewClient(&m)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeleteContainers(t.Context(), c, containers)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing container sind-dev-controller")
}

func TestDeleteContainers_Empty(t *testing.T) {
	var m docker.MockExecutor
	c := docker.NewClient(&m)

	err := DeleteContainers(t.Context(), c, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- DeleteNetwork ---

func TestDeleteNetwork(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // network rm
	c := docker.NewClient(&m)

	err := DeleteNetwork(t.Context(), c, docker.NetworkName("sind-dev-net"))

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "rm", "sind-dev-net"}, m.Calls[0].Args)
}

func TestDeleteNetwork_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("network has active endpoints"))
	c := docker.NewClient(&m)

	err := DeleteNetwork(t.Context(), c, docker.NetworkName("sind-dev-net"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing network sind-dev-net")
}

// --- DeleteVolumes ---

func TestDeleteVolumes(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // config
	m.AddResult("", "", nil) // munge
	m.AddResult("", "", nil) // data
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)

	require.NoError(t, err)
	require.Len(t, m.Calls, 3)
	assert.Equal(t, []string{"volume", "rm", "sind-dev-config"}, m.Calls[0].Args)
	assert.Equal(t, []string{"volume", "rm", "sind-dev-munge"}, m.Calls[1].Args)
	assert.Equal(t, []string{"volume", "rm", "sind-dev-data"}, m.Calls[2].Args)
}

func TestDeleteVolumes_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                         // config OK
	m.AddResult("", "", fmt.Errorf("volume in use")) // munge fails
	c := docker.NewClient(&m)

	volumes := []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data"}
	err := DeleteVolumes(t.Context(), c, volumes)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing volume sind-dev-munge")
}

func TestDeleteVolumes_Empty(t *testing.T) {
	var m docker.MockExecutor
	c := docker.NewClient(&m)

	err := DeleteVolumes(t.Context(), c, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- DeregisterMesh ---

func TestDeregisterMesh(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = meshDeregisterOnCall(
		"controller.dev.sind.local ssh-ed25519 AAAA1\nworker-0.dev.sind.local ssh-ed25519 AAAA2\n",
	)
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
		{Name: "sind-dev-worker-0"},
	}
	err := DeregisterMesh(t.Context(), mgr, "dev", containers)

	require.NoError(t, err)
}

func TestDeregisterMesh_Empty(t *testing.T) {
	var m docker.MockExecutor
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := DeregisterMesh(t.Context(), mgr, "dev", nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

func TestDeregisterMesh_DNSError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		// CopyFromContainer (read Corefile) fails
		if len(args) > 0 && args[0] == "cp" {
			return docker.MockResult{Err: fmt.Errorf("DNS container not running")}
		}
		return docker.MockResult{}
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeregisterMesh(t.Context(), mgr, "dev", containers)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing DNS record for controller")
}

func TestDeregisterMesh_KnownHostError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// CopyFromContainer (read Corefile) → return valid Corefile
		if len(args) >= 2 && args[0] == "cp" && strings.Contains(args[1], "sind-dns") {
			return docker.MockResult{Stdout: tarArchive("Corefile", emptyCorefileContent())}
		}
		// CopyToContainer (write Corefile) → success
		if len(args) >= 2 && args[0] == "cp" && args[1] == "-" {
			return docker.MockResult{}
		}
		// Signal DNS → success
		if len(args) >= 2 && args[0] == "kill" {
			return docker.MockResult{}
		}
		// ReadFile (known_hosts via exec cat) → fail
		if len(args) >= 2 && args[0] == "exec" {
			return docker.MockResult{Err: fmt.Errorf("SSH container not running")}
		}
		return docker.MockResult{}
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeregisterMesh(t.Context(), mgr, "dev", containers)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing known host for controller")
}

// --- HasOtherClusters ---

func TestHasOtherClusters_True(t *testing.T) {
	var m docker.MockExecutor
	// ListContainers returns containers from two clusters
	m.AddResult(ndjson(
		psEntry{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		psEntry{ID: "b", Names: "sind-prod-controller", State: "running", Image: "img"},
	), "", nil)
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, "dev")

	require.NoError(t, err)
	assert.True(t, has)
}

func TestHasOtherClusters_False(t *testing.T) {
	var m docker.MockExecutor
	// Only containers from the same cluster
	m.AddResult(ndjson(
		psEntry{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		psEntry{ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img"},
	), "", nil)
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, "dev")

	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasOtherClusters_NoContainers(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // empty list
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, "dev")

	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasOtherClusters_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := HasOtherClusters(t.Context(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing sind containers")
}

func TestHasOtherClusters_LabelFilter(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, _ = HasOtherClusters(t.Context(), c, "dev")

	require.Len(t, m.Calls, 1)
	args := m.Calls[0].Args
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label=sind.cluster", args[filterIdx+1])
}

func TestHasOtherClusters_PrefixAmbiguity(t *testing.T) {
	// Cluster "dev" must not match container "sind-dev2-controller".
	// The prefix includes the trailing dash: "sind-dev-".
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		psEntry{ID: "b", Names: "sind-dev2-controller", State: "running", Image: "img"},
	), "", nil)
	c := docker.NewClient(&m)

	has, err := HasOtherClusters(t.Context(), c, "dev")

	require.NoError(t, err)
	assert.True(t, has, "sind-dev2-controller should not match cluster dev")
}

// --- Delete Orchestrator ---

func TestDelete_FullCluster(t *testing.T) {
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []psEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
			{ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img"},
		},
		networkExists: true,
		volumes:       []string{"config", "munge", "data"},
		otherClusters: false,
		knownHosts:    "controller.dev.sind.local ssh-ed25519 K1\nworker-0.dev.sind.local ssh-ed25519 K2\n",
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.NoError(t, err)
}

func TestDelete_NonExistent(t *testing.T) {
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "gone", deleteOnCallOpts{
		containers:    nil,
		networkExists: false,
		volumes:       nil,
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "gone")

	require.NoError(t, err)
}

func TestDelete_Partial(t *testing.T) {
	// Partial cluster: containers gone, but network and some volumes remain
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:    nil,
		networkExists: true,
		volumes:       []string{"config", "data"}, // munge already gone
		otherClusters: true,                       // other clusters exist
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.NoError(t, err)
}

func TestDelete_PreserveMesh(t *testing.T) {
	// Other clusters exist, mesh should be preserved
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []psEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists: true,
		volumes:       []string{"config", "munge", "data"},
		otherClusters: true,
		knownHosts:    "controller.dev.sind.local ssh-ed25519 K1\n",
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.NoError(t, err)
}

func TestDelete_ListResourcesError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, _ string) docker.MockResult {
		return docker.MockResult{Err: fmt.Errorf("docker ps failed")}
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestDelete_DeregisterMeshError(t *testing.T) {
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []psEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists:  true,
		volumes:        []string{"config", "munge", "data"},
		deregisterFail: true,
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing DNS record")
}

func TestDelete_ContainerRemoveError(t *testing.T) {
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers: []psEntry{
			{ID: "a", Names: "sind-dev-controller", State: "running", Image: "img"},
		},
		networkExists:       true,
		volumes:             []string{"config", "munge", "data"},
		containerRemoveFail: true,
		knownHosts:          "controller.dev.sind.local ssh-ed25519 K1\n",
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing container")
}

func TestDelete_NetworkRemoveError(t *testing.T) {
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:        nil,
		networkExists:     true,
		volumes:           nil,
		networkRemoveFail: true,
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing network")
}

func TestDelete_HasOtherClustersError(t *testing.T) {
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:    nil,
		networkExists: false,
		volumes:       []string{"config"},
	})
	// Override OnCall to make HasOtherClusters fail.
	inner := m.OnCall
	callCount := 0
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// The second "ps" call is HasOtherClusters (the first is ListClusterResources).
		if args[0] == "ps" {
			callCount++
			if callCount == 2 {
				return docker.MockResult{Err: fmt.Errorf("docker daemon unreachable")}
			}
		}
		return inner(args, stdin)
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing sind containers")
}

func TestDelete_VolumeRemoveError(t *testing.T) {
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:       nil,
		networkExists:    false,
		volumes:          []string{"config"},
		volumeRemoveFail: true,
	})
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing volume")
}

func TestDelete_CleanupMeshError(t *testing.T) {
	// No other clusters → CleanupMesh runs and fails.
	var m docker.MockExecutor
	exitErr := notFoundErr(t)
	m.OnCall = deleteOnCall(t, exitErr, "dev", deleteOnCallOpts{
		containers:    nil,
		networkExists: false,
		volumes:       []string{"config"},
		otherClusters: false,
	})
	// Override: make CleanupMesh's ContainerExists (container inspect sind-ssh) fail.
	inner := m.OnCall
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		if args[0] == "container" && len(args) >= 3 && args[1] == "inspect" {
			return docker.MockResult{Err: fmt.Errorf("docker daemon unreachable")}
		}
		return inner(args, stdin)
	}
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c, mesh.DefaultRealm)

	err := Delete(t.Context(), c, mgr, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing SSH container")
}

// --- helpers ---

type psEntry struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	State  string `json:"State"`
	Image  string `json:"Image"`
	Labels string `json:"Labels,omitempty"`
}

// ndjson builds newline-delimited JSON from the given entries.
func ndjson(entries ...psEntry) string {
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
}

// indexOf returns the index of s in slice, or -1 if not found.
func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}

// meshDeregisterOnCall returns a mock OnCall that handles RemoveDNSRecord
// and RemoveKnownHost operations for DeregisterMesh tests.
func meshDeregisterOnCall(knownHostsContent string) func([]string, string) docker.MockResult {
	return func(args []string, stdin string) docker.MockResult {
		if len(args) == 0 {
			return docker.MockResult{}
		}
		switch {
		// CopyFromContainer: docker cp sind-dns:/Corefile -
		case args[0] == "cp" && len(args) >= 2 && strings.Contains(args[1], "sind-dns"):
			return docker.MockResult{Stdout: tarArchive("Corefile", emptyCorefileContent())}
		// CopyToContainer: docker cp - sind-dns:/
		case args[0] == "cp" && len(args) >= 2 && args[1] == "-":
			return docker.MockResult{}
		// Signal: docker kill -s HUP sind-dns
		case args[0] == "kill":
			return docker.MockResult{}
		// ReadFile: docker exec sind-ssh cat /root/.ssh/known_hosts
		case args[0] == "exec" && len(args) >= 3 && args[2] == "cat":
			return docker.MockResult{Stdout: knownHostsContent}
		// WriteFile: docker exec -i sind-ssh sh -c 'cat > ...'
		case args[0] == "exec" && len(args) >= 2 && args[1] == "-i":
			return docker.MockResult{}
		}
		return docker.MockResult{}
	}
}

// deleteOnCallOpts configures the behavior of deleteOnCall.
type deleteOnCallOpts struct {
	containers          []psEntry
	networkExists       bool
	volumes             []string // volume type suffixes that exist, e.g. ["config", "munge", "data"]
	otherClusters       bool
	knownHosts          string
	deregisterFail      bool
	containerRemoveFail bool
	networkRemoveFail   bool
	volumeRemoveFail    bool
}

// deleteOnCall returns a mock OnCall handler for the Delete orchestrator tests.
// It dispatches based on docker command arguments to simulate the full delete flow.
func deleteOnCall(t *testing.T, exitErr *exec.ExitError, clusterName string, opts deleteOnCallOpts) func([]string, string) docker.MockResult {
	t.Helper()
	containerJSON := ""
	if len(opts.containers) > 0 {
		containerJSON = ndjson(opts.containers...)
	}
	// Build set of existing volumes for quick lookup.
	existingVolumes := map[string]bool{}
	for _, v := range opts.volumes {
		existingVolumes["sind-"+clusterName+"-"+v] = true
	}

	// Track which phase we're in based on calls.
	listClusterDone := false

	return func(args []string, stdin string) docker.MockResult {
		if len(args) == 0 {
			return docker.MockResult{}
		}

		switch {
		// docker ps -a ... (ListContainers or HasOtherClusters)
		case args[0] == "ps":
			filterVal := ""
			for i, a := range args {
				if a == "--filter" && i+1 < len(args) {
					filterVal = args[i+1]
				}
			}

			if !listClusterDone && strings.Contains(filterVal, "="+clusterName) {
				// ListClusterResources: return cluster containers
				listClusterDone = true
				return docker.MockResult{Stdout: containerJSON}
			}
			// HasOtherClusters: return other cluster containers
			if opts.otherClusters {
				return docker.MockResult{Stdout: ndjson(
					psEntry{ID: "x", Names: "sind-other-controller", State: "running", Image: "img"},
				)}
			}
			return docker.MockResult{Stdout: ""}

		// docker network inspect (NetworkExists check)
		case args[0] == "network" && args[1] == "inspect":
			if opts.networkExists {
				return docker.MockResult{}
			}
			return docker.MockResult{Stderr: "Error: No such network\n", Err: exitErr}

		// docker network rm (DeleteNetwork)
		case args[0] == "network" && args[1] == "rm":
			if opts.networkRemoveFail {
				return docker.MockResult{Err: fmt.Errorf("network has active endpoints")}
			}
			return docker.MockResult{}

		// docker volume inspect (VolumeExists check)
		case args[0] == "volume" && args[1] == "inspect":
			volName := args[2]
			if existingVolumes[volName] {
				return docker.MockResult{}
			}
			return docker.MockResult{Stderr: "Error: No such volume\n", Err: exitErr}

		// docker volume rm (DeleteVolumes)
		case args[0] == "volume" && args[1] == "rm":
			if opts.volumeRemoveFail {
				return docker.MockResult{Err: fmt.Errorf("volume in use")}
			}
			return docker.MockResult{}

		// docker stop (DeleteContainers - best effort)
		case args[0] == "stop":
			return docker.MockResult{}

		// docker rm (DeleteContainers or CleanupMesh)
		case args[0] == "rm":
			if opts.containerRemoveFail {
				return docker.MockResult{Err: fmt.Errorf("removal failed")}
			}
			return docker.MockResult{}

		// docker cp (DNS Corefile read/write for DeregisterMesh)
		case args[0] == "cp":
			if opts.deregisterFail && strings.Contains(args[1], "sind-dns") {
				return docker.MockResult{Err: fmt.Errorf("DNS container not running")}
			}
			if len(args) >= 2 && strings.Contains(args[1], "sind-dns") {
				return docker.MockResult{Stdout: tarArchive("Corefile", emptyCorefileContent())}
			}
			return docker.MockResult{}

		// docker kill -s HUP (DNS reload)
		case args[0] == "kill":
			return docker.MockResult{}

		// docker exec (known_hosts read/write for DeregisterMesh or CleanupMesh)
		case args[0] == "exec":
			if len(args) >= 3 && args[2] == "cat" {
				return docker.MockResult{Stdout: opts.knownHosts}
			}
			return docker.MockResult{}

		// docker container inspect (ContainerExists for CleanupMesh)
		case args[0] == "container" && args[1] == "inspect":
			if !opts.otherClusters {
				// Mesh containers exist during cleanup
				return docker.MockResult{}
			}
			return docker.MockResult{Stderr: "Error\n", Err: exitErr}
		}

		t.Logf("unhandled mock call: %v", args)
		return docker.MockResult{}
	}
}
