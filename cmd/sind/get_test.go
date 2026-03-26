// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// psEntry mirrors docker ps --format json output.
type psEntry struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	State  string `json:"State"`
	Image  string `json:"Image"`
	Labels string `json:"Labels,omitempty"`
}

func ndjson(entries ...psEntry) string {
	var lines []string
	for _, e := range entries {
		data, _ := json.Marshal(e)
		lines = append(lines, string(data))
	}
	return strings.Join(lines, "\n") + "\n"
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
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=25.11.0",
		},
		psEntry{
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
	var m docker.MockExecutor
	m.AddResult(ndjson(
		psEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		psEntry{
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
	var m docker.MockExecutor
	m.AddResult(ndjson(psEntry{
		ID: "a", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller",
	}), "", nil)
	m.AddResult(tarArchive("munge.key", "secret-key"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "munge-key", "dev")
	require.NoError(t, err)
	assert.Equal(t, "c2VjcmV0LWtleQ==\n", stdout)
}

func tarArchive(name, content string) string {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte(content)
	tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0644})
	tw.Write(data)
	tw.Close()
	return buf.String()
}

// --- Integration ---

func TestGetClustersEmpty(t *testing.T) {
	realClient(t)
	stdout, _, err := executeWithDocker("get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, "NAME")
}

func TestGetLifecycle(t *testing.T) {
	c := realClient(t)
	ctx := t.Context()
	t.Setenv("SIND_REALM", testRealm)
	cluster := "cli-get-" + testID

	netName := docker.NetworkName(testRealm + "-" + cluster + "-net")
	ctrName := docker.ContainerName(testRealm + "-" + cluster + "-controller")
	volConfig := docker.VolumeName(testRealm + "-" + cluster + "-config")
	volMunge := docker.VolumeName(testRealm + "-" + cluster + "-munge")
	volData := docker.VolumeName(testRealm + "-" + cluster + "-data")

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
		"--label", "sind.cluster="+cluster,
		"--label", "sind.role=controller",
		"--label", "sind.slurm.version=25.11.0",
		"-v", string(volMunge)+":/etc/munge",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)
	require.NoError(t, c.WriteFile(ctx, ctrName, "/etc/munge/munge.key", "test-munge-key"))

	// get clusters
	stdout, _, err := executeWithDocker("get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "25.11.0")
	assert.Contains(t, stdout, "running")

	// get nodes
	stdout, _, err = executeWithDocker("get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "controller."+cluster)

	// get networks
	stdout, _, err = executeWithDocker("get", "networks")
	require.NoError(t, err)
	assert.Contains(t, stdout, string(netName))

	// get volumes
	stdout, _, err = executeWithDocker("get", "volumes")
	require.NoError(t, err)
	assert.Contains(t, stdout, string(volConfig))
	assert.Contains(t, stdout, string(volMunge))
	assert.Contains(t, stdout, string(volData))

	// get munge-key
	stdout, _, err = executeWithDocker("get", "munge-key", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "dGVzdC1tdW5nZS1rZXk=") // base64("test-munge-key")
}
