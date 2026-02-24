// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		psEntry{ID: "def456", Names: "sind-dev-compute-0", State: "running", Image: "sind-node:latest"},
	), "", nil)
	// NetworkExists: sind-dev-net exists
	m.AddResult("", "", nil)
	// VolumeExists: config, munge, data
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	res, err := ListClusterResources(context.Background(), c, "dev")

	require.NoError(t, err)

	// Containers
	require.Len(t, res.Containers, 2)
	assert.Equal(t, docker.ContainerName("sind-dev-controller"), res.Containers[0].Name)
	assert.Equal(t, docker.ContainerName("sind-dev-compute-0"), res.Containers[1].Name)

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

	res, err := ListClusterResources(context.Background(), c, "nonexistent")

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

	res, err := ListClusterResources(context.Background(), c, "dev")

	require.NoError(t, err)
	assert.True(t, res.NetworkExists)
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-data"}, res.Volumes)
}

func TestListClusterResources_ListContainersError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("docker ps failed"))
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestListClusterResources_NetworkCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                  // ListContainers: empty
	m.AddResult("", "", fmt.Errorf("network inspect failed")) // non-exit error
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking network")
}

func TestListClusterResources_VolumeCheckError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil)                                 // ListContainers: empty
	m.AddResult("", "", nil)                                 // NetworkExists: exists
	m.AddResult("", "", fmt.Errorf("volume inspect failed")) // config check fails
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking volume")
}

func TestListClusterResources_LabelFilter(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // ListContainers: empty
	addNotFound(t, &m, 4)    // network + 3 volumes
	c := docker.NewClient(&m)

	_, err := ListClusterResources(context.Background(), c, "myCluster")

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
		{Name: "sind-dev-compute-0"},
	}
	err := DeleteContainers(context.Background(), c, containers)

	require.NoError(t, err)
	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"stop", "sind-dev-controller"}, m.Calls[0].Args)
	assert.Equal(t, []string{"rm", "sind-dev-controller"}, m.Calls[1].Args)
	assert.Equal(t, []string{"stop", "sind-dev-compute-0"}, m.Calls[2].Args)
	assert.Equal(t, []string{"rm", "sind-dev-compute-0"}, m.Calls[3].Args)
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
	err := DeleteContainers(context.Background(), c, containers)

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
	err := DeleteContainers(context.Background(), c, containers)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing container sind-dev-controller")
}

func TestDeleteContainers_Empty(t *testing.T) {
	var m docker.MockExecutor
	c := docker.NewClient(&m)

	err := DeleteContainers(context.Background(), c, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- DeleteNetwork ---

func TestDeleteNetwork(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", nil) // network rm
	c := docker.NewClient(&m)

	err := DeleteNetwork(context.Background(), c, docker.NetworkName("sind-dev-net"))

	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "rm", "sind-dev-net"}, m.Calls[0].Args)
}

func TestDeleteNetwork_Error(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("network has active endpoints"))
	c := docker.NewClient(&m)

	err := DeleteNetwork(context.Background(), c, docker.NetworkName("sind-dev-net"))

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
	err := DeleteVolumes(context.Background(), c, volumes)

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
	err := DeleteVolumes(context.Background(), c, volumes)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing volume sind-dev-munge")
}

func TestDeleteVolumes_Empty(t *testing.T) {
	var m docker.MockExecutor
	c := docker.NewClient(&m)

	err := DeleteVolumes(context.Background(), c, nil)

	require.NoError(t, err)
	assert.Empty(t, m.Calls)
}

// --- DeregisterMesh ---

func TestDeregisterMesh(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = meshDeregisterOnCall(
		"controller.dev.sind.local ssh-ed25519 AAAA1\ncompute-0.dev.sind.local ssh-ed25519 AAAA2\n",
	)
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
		{Name: "sind-dev-compute-0"},
	}
	err := DeregisterMesh(context.Background(), mgr, "dev", containers)

	require.NoError(t, err)
}

func TestDeregisterMesh_Empty(t *testing.T) {
	var m docker.MockExecutor
	c := docker.NewClient(&m)
	mgr := mesh.NewManager(c)

	err := DeregisterMesh(context.Background(), mgr, "dev", nil)

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
	mgr := mesh.NewManager(c)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeregisterMesh(context.Background(), mgr, "dev", containers)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing DNS record for controller")
}

func TestDeregisterMesh_KnownHostError(t *testing.T) {
	var m docker.MockExecutor
	m.OnCall = func(args []string, stdin string) docker.MockResult {
		// CopyFromContainer (read Corefile) → return valid Corefile
		if len(args) >= 2 && args[0] == "cp" && strings.Contains(args[1], "sind-dns") {
			return docker.MockResult{Stdout: tarFile("Corefile", emptyCorefileContent())}
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
	mgr := mesh.NewManager(c)

	containers := []docker.ContainerListEntry{
		{Name: "sind-dev-controller"},
	}
	err := DeregisterMesh(context.Background(), mgr, "dev", containers)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing known host for controller")
}

// --- helpers ---

type psEntry struct {
	ID    string `json:"ID"`
	Names string `json:"Names"`
	State string `json:"State"`
	Image string `json:"Image"`
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
			return docker.MockResult{Stdout: tarFile("Corefile", emptyCorefileContent())}
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

// tarFile creates a tar archive containing a single file.
func tarFile(name, content string) string {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0644})
	tw.Write([]byte(content))
	tw.Close()
	return buf.String()
}

// emptyCorefileContent returns a Corefile with no host entries.
func emptyCorefileContent() string {
	return "sind.local:53 {\n    hosts {\n        fallthrough\n    }\n    log\n    errors\n}\n\n.:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"
}
