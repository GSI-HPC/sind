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
