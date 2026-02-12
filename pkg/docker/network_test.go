// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testNetworkName NetworkName = "sind-dev-net"

func TestCreateNetwork(t *testing.T) {
	const networkID NetworkID = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"

	var m MockExecutor
	m.AddResult(string(networkID)+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateNetwork(context.Background(), testNetworkName)
	require.NoError(t, err)
	assert.Equal(t, networkID, id)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "create", string(testNetworkName)}, m.Calls[0].Args)
}

func TestCreateNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: network with name "+string(testNetworkName)+" already exists\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	id, err := c.CreateNetwork(context.Background(), testNetworkName)
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestRemoveNetwork(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testNetworkName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveNetwork(context.Background(), testNetworkName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "rm", string(testNetworkName)}, m.Calls[0].Args)
}

func TestRemoveNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such network: "+string(testNetworkName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveNetwork(context.Background(), testNetworkName)
	assert.Error(t, err)
}

func TestConnectNetwork(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.ConnectNetwork(context.Background(), testNetworkName, testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "connect", string(testNetworkName), string(testContainerName)}, m.Calls[0].Args)
}

func TestConnectNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.ConnectNetwork(context.Background(), testNetworkName, testContainerName)
	assert.Error(t, err)
}

func TestDisconnectNetwork(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.DisconnectNetwork(context.Background(), testNetworkName, testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "disconnect", string(testNetworkName), string(testContainerName)}, m.Calls[0].Args)
}

func TestDisconnectNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.DisconnectNetwork(context.Background(), testNetworkName, testContainerName)
	assert.Error(t, err)
}
