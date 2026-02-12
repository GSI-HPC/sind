// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testContainer = "sind-dev-controller"

func TestCreateContainer(t *testing.T) {
	const containerID = "b98dd34e3931dd738dd597ca2ae6fdc30a955be1c0662a321c634a82e5348ee9"

	var m MockExecutor
	m.AddResult(containerID+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateContainer(context.Background(),
		"--name", testContainer,
		"--label", "sind.cluster=dev",
		"alpine",
	)
	require.NoError(t, err)
	assert.Equal(t, containerID, id)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, "docker", m.Calls[0].Name)
	assert.Equal(t, []string{
		"run", "-d",
		"--name", testContainer,
		"--label", "sind.cluster=dev",
		"alpine",
	}, m.Calls[0].Args)
}

func TestCreateContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "docker: Error response from daemon: Conflict.\n", fmt.Errorf("exit status 125"))
	c := NewClient(&m)

	id, err := c.CreateContainer(context.Background(), "alpine")
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestStartContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(testContainer+"\n", "", nil)
	c := NewClient(&m)

	err := c.StartContainer(context.Background(), testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"start", testContainer}, m.Calls[0].Args)
}

func TestStartContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+testContainer+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.StartContainer(context.Background(), testContainer)
	assert.Error(t, err)
}

func TestStopContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(testContainer+"\n", "", nil)
	c := NewClient(&m)

	err := c.StopContainer(context.Background(), testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"stop", testContainer}, m.Calls[0].Args)
}

func TestStopContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+testContainer+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.StopContainer(context.Background(), testContainer)
	assert.Error(t, err)
}

func TestRemoveContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(testContainer+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveContainer(context.Background(), testContainer)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"rm", testContainer}, m.Calls[0].Args)
}

func TestRemoveContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+testContainer+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveContainer(context.Background(), testContainer)
	assert.Error(t, err)
}

const inspectJSON = `[{
  "Id": "94649329a21a97708c8f53c7348adafb926eaef1929b79ae760458a50d78e1ca",
  "Name": "/sind-dev-controller",
  "State": {"Status": "running", "Running": true, "Paused": false},
  "Config": {"Labels": {"sind.cluster": "dev", "sind.role": "controller"}},
  "NetworkSettings": {
    "Networks": {
      "sind-dev-net": {"IPAddress": "172.18.0.2"},
      "sind-mesh":    {"IPAddress": "172.19.0.3"}
    }
  }
}]`

func TestInspectContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(inspectJSON, "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainer)
	require.NoError(t, err)

	assert.Equal(t, "94649329a21a97708c8f53c7348adafb926eaef1929b79ae760458a50d78e1ca", info.ID)
	assert.Equal(t, testContainer, info.Name)
	assert.Equal(t, "running", info.Status)
	assert.Equal(t, map[string]string{
		"sind.cluster": "dev",
		"sind.role":    "controller",
	}, info.Labels)
	assert.Equal(t, map[string]string{
		"sind-dev-net": "172.18.0.2",
		"sind-mesh":    "172.19.0.3",
	}, info.IPs)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"inspect", testContainer}, m.Calls[0].Args)
}

func TestInspectContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such object: "+testContainer+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainer)
	assert.Error(t, err)
	assert.Nil(t, info)
}

func TestInspectContainer_InvalidJSON(t *testing.T) {
	var m MockExecutor
	m.AddResult("not json", "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainer)
	assert.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "parsing inspect output")
}

func TestInspectContainer_EmptyResult(t *testing.T) {
	var m MockExecutor
	m.AddResult("[]", "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainer)
	assert.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "no results")
}

const psJSON = `{"ID":"94649329a21a97708c8f53c7348adafb926eaef1929b79ae760458a50d78e1ca","Names":"sind-dev-controller","State":"running","Image":"ghcr.io/gsi-hpc/sind-node:25.11"}
{"ID":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","Names":"sind-dev-compute-0","State":"running","Image":"ghcr.io/gsi-hpc/sind-node:25.11"}`

func TestListContainers(t *testing.T) {
	var m MockExecutor
	m.AddResult(psJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background(), "label=sind.cluster=dev")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, "sind-dev-controller", entries[0].Name)
	assert.Equal(t, "running", entries[0].State)
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:25.11", entries[0].Image)

	assert.Equal(t, "sind-dev-compute-0", entries[1].Name)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"ps", "-a", "--no-trunc", "--format", "json", "--filter", "label=sind.cluster=dev"}, m.Calls[0].Args)
}

func TestListContainers_Empty(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background(), "label=sind.cluster=none")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestListContainers_MultipleFilters(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	c.ListContainers(context.Background(), "label=sind.cluster=dev", "label=sind.role=controller")

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"ps", "-a", "--no-trunc", "--format", "json",
		"--filter", "label=sind.cluster=dev",
		"--filter", "label=sind.role=controller",
	}, m.Calls[0].Args)
}

func TestListContainers_InvalidJSON(t *testing.T) {
	var m MockExecutor
	m.AddResult("not json\n", "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background())
	assert.Error(t, err)
	assert.Nil(t, entries)
	assert.Contains(t, err.Error(), "parsing ps output")
}

func TestListContainers_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background())
	assert.Error(t, err)
	assert.Nil(t, entries)
}
