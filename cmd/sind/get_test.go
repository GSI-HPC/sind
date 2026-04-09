// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSSHConfig_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "ssh-config"})
	require.NoError(t, err)
	assert.Equal(t, "ssh-config", c.Use)
}

func TestGetSSHConfig_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "ssh-config", "extra")
	assert.Error(t, err)
}

func TestGetSSHConfig_Output(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	stdout, _, err := executeCommand("get", "ssh-config")
	require.NoError(t, err)
	assert.Equal(t, "/xdg/state/sind/sind/ssh_config\n", stdout)
}

func TestGetSSHConfig_CustomRealm(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	stdout, _, err := executeCommand("--realm", "ci-42", "get", "ssh-config")
	require.NoError(t, err)
	assert.Equal(t, "/xdg/state/sind/ci-42/ssh_config\n", stdout)
}

func TestGetSSHConfig_XDGFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/user")
	stdout, _, err := executeCommand("get", "ssh-config")
	require.NoError(t, err)
	assert.Equal(t, "/home/user/.local/state/sind/sind/ssh_config\n", stdout)
}

func TestGetClusters_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "clusters"})
	require.NoError(t, err)
	assert.Equal(t, "clusters", c.Use)
}

func TestGetClusters_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "clusters", "extra")
	assert.Error(t, err)
}

func TestGetClusters_Output(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=25.11.0",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "dev")
	assert.Contains(t, stdout, "25.11.0")
	assert.Contains(t, stdout, "running")
}

func TestGetNodes_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "nodes"})
	require.NoError(t, err)
	assert.Equal(t, "nodes [CLUSTER]", c.Use)
}

func TestGetNodes_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("get", "nodes", "a", "b")
	assert.Error(t, err)
}

func TestGetNodes_Output(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "nodes", "dev")
	require.NoError(t, err)
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "controller.dev")
	assert.Contains(t, stdout, "worker-0.dev")
}

func TestGetNetworks_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "networks"})
	require.NoError(t, err)
	assert.Equal(t, "networks", c.Use)
}

func TestGetVolumes_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "volumes"})
	require.NoError(t, err)
	assert.Equal(t, "volumes", c.Use)
}

func TestGetMungeKey_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "munge-key"})
	require.NoError(t, err)
	assert.Equal(t, "munge-key [CLUSTER]", c.Use)
}

func TestGetMungeKey_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("get", "munge-key", "a", "b")
	assert.Error(t, err)
}

func TestGetMungeKey_Output(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "a", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller",
	}), "", nil)
	m.AddResult(testutil.TarArchive("munge.key", "secret-key"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "munge-key", "dev")
	require.NoError(t, err)
	assert.Equal(t, "c2VjcmV0LWtleQ==\n", stdout)
}

func TestGetDNS_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "dns"})
	require.NoError(t, err)
	assert.Equal(t, "dns", c.Use)
}

func TestGetDNS_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "dns", "extra")
	assert.Error(t, err)
}

func TestGetDNS_Output(t *testing.T) {
	corefile := "sind.sind:53 {\n    hosts {\n" +
		"        172.18.0.2 controller.dev.sind.sind\n" +
		"        172.18.0.3 worker-0.dev.sind.sind\n" +
		"        fallthrough\n    }\n    reload\n    log\n    errors\n}\n\n" +
		".:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"

	var m mock.Executor
	m.AddResult(testutil.TarArchive("Corefile", corefile), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "dns")
	require.NoError(t, err)
	assert.Contains(t, stdout, "HOSTNAME")
	assert.Contains(t, stdout, "IP")
	assert.Contains(t, stdout, "controller.dev.sind.sind")
	assert.Contains(t, stdout, "172.18.0.2")
	assert.Contains(t, stdout, "worker-0.dev.sind.sind")
	assert.Contains(t, stdout, "172.18.0.3")
}

func TestGetDNS_Empty(t *testing.T) {
	corefile := "sind.sind:53 {\n    hosts {\n" +
		"        fallthrough\n    }\n    reload\n    log\n    errors\n}\n\n" +
		".:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"

	var m mock.Executor
	m.AddResult(testutil.TarArchive("Corefile", corefile), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "dns")
	require.NoError(t, err)
	assert.Contains(t, stdout, "HOSTNAME")
	assert.NotContains(t, stdout, "sind.sind")
}

func TestGetDNS_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("container not found"))

	_, _, err := executeWithMock(&m, "get", "dns")
	assert.Error(t, err)
}

// --- JSON output ---

func TestGetClusters_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=25.11.0",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "clusters", "--output", "json")
	require.NoError(t, err)

	var got []struct {
		Name         string `json:"name"`
		SlurmVersion string `json:"slurm_version"`
		Status       string `json:"status"`
		Nodes        int    `json:"nodes"`
		Controllers  int    `json:"controllers"`
		Workers      int    `json:"workers"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "dev", got[0].Name)
	assert.Equal(t, "25.11.0", got[0].SlurmVersion)
	assert.Equal(t, "running", got[0].Status)
	assert.Equal(t, 2, got[0].Nodes)
	assert.Equal(t, 1, got[0].Controllers)
	assert.Equal(t, 1, got[0].Workers)
}

func TestGetClusters_JSONEmpty(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)

	stdout, _, err := executeWithMock(&m, "get", "clusters", "--output", "json")
	require.NoError(t, err)
	assert.Equal(t, "null\n", stdout)
}

func TestGetNodes_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "nodes", "dev", "--output", "json")
	require.NoError(t, err)

	var got []struct {
		Name   string `json:"name"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "controller.dev", got[0].Name)
	assert.Equal(t, "controller", got[0].Role)
	assert.Equal(t, "running", got[0].Status)
	assert.Equal(t, "worker-0.dev", got[1].Name)
}

func TestGetDNS_JSON(t *testing.T) {
	corefile := "sind.sind:53 {\n    hosts {\n" +
		"        172.18.0.2 controller.dev.sind.sind\n" +
		"        172.18.0.3 worker-0.dev.sind.sind\n" +
		"        fallthrough\n    }\n    reload\n    log\n    errors\n}\n\n" +
		".:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"

	var m mock.Executor
	m.AddResult(testutil.TarArchive("Corefile", corefile), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "dns", "--output", "json")
	require.NoError(t, err)

	var got []mesh.DNSRecord
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "controller.dev.sind.sind", got[0].Hostname)
	assert.Equal(t, "172.18.0.2", got[0].IP)
}

func TestGetMungeKey_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "a", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller",
	}), "", nil)
	m.AddResult(testutil.TarArchive("munge.key", "secret-key"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "munge-key", "dev", "--output", "json")
	require.NoError(t, err)

	var got struct {
		Key string `json:"key"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "c2VjcmV0LWtleQ==", got.Key)
}

func TestGetSSHConfig_JSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	stdout, _, err := executeCommand("get", "ssh-config", "--output", "json")
	require.NoError(t, err)

	var got struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "/xdg/state/sind/sind/ssh_config", got.Path)
}

// --- Mesh ---

func TestGetMesh_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "mesh"})
	require.NoError(t, err)
	assert.Equal(t, "mesh", c.Use)
}

func TestGetMesh_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "mesh", "extra")
	assert.Error(t, err)
}

func TestGetMesh_Output(t *testing.T) {
	inspectJSON := `[{"Id":"dns1","Name":"/sind-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"10.0.0.2"}}}}]`
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // ContainerExists → true
	m.AddResult(inspectJSON, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "mesh")
	require.NoError(t, err)
	assert.Contains(t, stdout, "PROPERTY")
	assert.Contains(t, stdout, "sind-mesh")
	assert.Contains(t, stdout, "sind-dns")
	assert.Contains(t, stdout, "10.0.0.2")
	assert.Contains(t, stdout, "sind.sind")
	assert.Contains(t, stdout, "sind-ssh")
	assert.Contains(t, stdout, "sind-ssh-config")
}

func TestGetMesh_JSON(t *testing.T) {
	inspectJSON := `[{"Id":"dns1","Name":"/sind-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"10.0.0.2"}}}}]`
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // ContainerExists → true
	m.AddResult(inspectJSON, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "mesh", "--output", "json")
	require.NoError(t, err)

	var got mesh.Info
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "sind-mesh", got.Network)
	assert.Equal(t, "sind-dns", got.DNSContainer)
	assert.Equal(t, "10.0.0.2", got.DNSIP)
	assert.Equal(t, "sind.sind", got.DNSZone)
}

func TestGetMesh_Error(t *testing.T) {
	var m mock.Executor
	// ContainerExists returns a non-exit-code-1 error (daemon unreachable).
	m.AddResult("", "", fmt.Errorf("docker daemon unreachable"))

	_, _, err := executeWithMock(&m, "get", "mesh")
	assert.Error(t, err)
}

// --- Integration ---

func TestGetClustersEmpty(t *testing.T) {
	t.Parallel()
	realClient(t)
	realm := "it-empty-" + testID
	stdout, _, err := executeWithRealm(realm, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, "NAME")
}

func TestGetLifecycle(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	ctx := t.Context()
	realm := "it-get-" + testID
	cluster := "cli-get-" + testID

	netName := docker.NetworkName(realm + "-" + cluster + "-net")
	ctrName := docker.ContainerName(realm + "-" + cluster + "-controller")
	volConfig := docker.VolumeName(realm + "-" + cluster + "-config")
	volMunge := docker.VolumeName(realm + "-" + cluster + "-munge")
	volData := docker.VolumeName(realm + "-" + cluster + "-data")

	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctrName)
		_ = c.RemoveContainer(bg, ctrName)
		_ = c.RemoveVolume(bg, volConfig)
		_ = c.RemoveVolume(bg, volMunge)
		_ = c.RemoveVolume(bg, volData)
		_ = c.RemoveNetwork(bg, netName)
	})

	_, err := c.CreateNetwork(ctx, netName, nil)
	require.NoError(t, err)
	require.NoError(t, c.CreateVolume(ctx, volConfig, nil))
	require.NoError(t, c.CreateVolume(ctx, volMunge, nil))
	require.NoError(t, c.CreateVolume(ctx, volData, nil))

	_, err = c.RunContainer(ctx,
		"--name", string(ctrName),
		"--network", string(netName),
		"--label", "sind.realm="+realm,
		"--label", "sind.cluster="+cluster,
		"--label", "sind.role=controller",
		"--label", "sind.slurm.version=25.11.0",
		"-v", string(volMunge)+":/etc/munge",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)
	require.NoError(t, c.WriteFile(ctx, ctrName, "/etc/munge/munge.key", "test-munge-key"))

	// get clusters
	stdout, _, err := executeWithRealm(realm, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "25.11.0")
	assert.Contains(t, stdout, "running")

	// get nodes
	stdout, _, err = executeWithRealm(realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "controller."+cluster)

	// get networks
	stdout, _, err = executeWithRealm(realm, "get", "networks")
	require.NoError(t, err)
	assert.Contains(t, stdout, string(netName))

	// get volumes
	stdout, _, err = executeWithRealm(realm, "get", "volumes")
	require.NoError(t, err)
	assert.Contains(t, stdout, string(volConfig))
	assert.Contains(t, stdout, string(volMunge))
	assert.Contains(t, stdout, string(volData))

	// get munge-key
	stdout, _, err = executeWithRealm(realm, "get", "munge-key", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "dGVzdC1tdW5nZS1rZXk=") // base64("test-munge-key")
}
