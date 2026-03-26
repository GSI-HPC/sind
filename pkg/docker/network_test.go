// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testNetworkName NetworkName = "sind-dev-net"

func TestNetworkLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	name := itNetworkName("net")
	n := string(name)

	if !rec.IsIntegration() {
		rec.AddResult("6f02052f\n", "", nil)                                                       // create
		rec.AddResult("[{}]\n", "", nil)                                                           // exists → true
		rec.AddResult(n+"\n", "", nil)                                                             // remove
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)})                  // exists → false
		rec.AddResult("6f02052f\n", "", nil)                                                       // re-create
		rec.AddResult(`{"Name":"`+n+`","Driver":"bridge","ID":"x","Scope":"local"}`+"\n", "", nil) // list
		rec.AddResult("", "Error: network already exists\n", fmt.Errorf("exit status 1"))          // create duplicate (error)
		rec.AddResult("", "", nil)                                                                 // list no matches (empty)
		rec.AddResult(n+"\n", "", nil)                                                             // remove (ok)
		rec.AddResult("", "Error: No such network\n", fmt.Errorf("exit status 1"))                 // remove again (error)
	}
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), name) })

	// Create.
	id, err := c.CreateNetwork(ctx, name)
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	// Exists → true.
	exists, err := c.NetworkExists(ctx, name)
	require.NoError(t, err)
	assert.True(t, exists)

	// Remove.
	err = c.RemoveNetwork(ctx, name)
	require.NoError(t, err)

	// Exists → false.
	exists, err = c.NetworkExists(ctx, name)
	require.NoError(t, err)
	assert.False(t, exists)

	// List after re-create.
	_, err = c.CreateNetwork(ctx, name)
	require.NoError(t, err)

	entries, err := c.ListNetworks(ctx, "name="+n)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, name, entries[0].Name)

	// Create duplicate → error.
	_, err = c.CreateNetwork(ctx, name)
	assert.Error(t, err)

	// List with no matches → empty.
	entries, err = c.ListNetworks(ctx, "name=sind-nonexistent-xyz")
	require.NoError(t, err)
	assert.Empty(t, entries)

	// Remove twice → second fails.
	err = c.RemoveNetwork(ctx, name)
	require.NoError(t, err)

	err = c.RemoveNetwork(ctx, name)
	assert.Error(t, err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestNetworkConnectDisconnectLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	net := itNetworkName("conn")
	ctr := itContainerName("conn")

	if !rec.IsIntegration() {
		rec.AddResult("net-id\n", "", nil)           // create network
		rec.AddResult("ctr-id\n", "", nil)           // run container
		rec.AddResult("", "", nil)                    // connect
		rec.AddResult(inspectRunning(string(ctr)), "", nil) // inspect (has net)
		rec.AddResult("", "", nil)                    // disconnect
		rec.AddResult(inspectRunning(string(ctr)), "", nil) // inspect (no net)
		rec.AddResult(string(ctr)+"\n", "", nil)      // kill (cleanup)
		rec.AddResult(string(ctr)+"\n", "", nil)      // rm (cleanup)
		rec.AddResult(string(net)+"\n", "", nil)       // rm network (cleanup)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctr)
		_ = c.RemoveContainer(bg, ctr)
		_ = c.RemoveNetwork(bg, net)
	})

	// Create network + container.
	_, err := c.CreateNetwork(ctx, net)
	require.NoError(t, err)

	_, err = c.RunContainer(ctx, "--name", string(ctr), "busybox:latest", "sleep", "60")
	require.NoError(t, err)

	// Connect → container has IP on network.
	err = c.ConnectNetwork(ctx, net, ctr)
	require.NoError(t, err)

	info, err := c.InspectContainer(ctx, ctr)
	require.NoError(t, err)
	if rec.IsIntegration() {
		assert.Contains(t, info.IPs, net, "container should be on network after connect")
	}

	// Disconnect → container no longer on network.
	err = c.DisconnectNetwork(ctx, net, ctr)
	require.NoError(t, err)

	info, err = c.InspectContainer(ctx, ctr)
	require.NoError(t, err)
	if rec.IsIntegration() {
		assert.NotContains(t, info.IPs, net, "container should not be on network after disconnect")
	}

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestNetworkExists_True(t *testing.T) {
	var m MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := NewClient(&m)

	exists, err := c.NetworkExists(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.True(t, exists)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", string(testNetworkName)}, m.Calls[0].Args)
}

func TestNetworkExists_False(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such network: "+string(testNetworkName)+"\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	exists, err := c.NetworkExists(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestNetworkExists_OtherError(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	exists, err := c.NetworkExists(t.Context(), testNetworkName)
	assert.Error(t, err)
	assert.False(t, exists)
}

func TestCreateNetwork(t *testing.T) {
	const networkID NetworkID = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"

	var m MockExecutor
	m.AddResult(string(networkID)+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateNetwork(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.Equal(t, networkID, id)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "create", string(testNetworkName)}, m.Calls[0].Args)
}

func TestCreateNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: network with name "+string(testNetworkName)+" already exists\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	id, err := c.CreateNetwork(t.Context(), testNetworkName)
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestRemoveNetwork(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testNetworkName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveNetwork(t.Context(), testNetworkName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "rm", string(testNetworkName)}, m.Calls[0].Args)
}

func TestRemoveNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such network: "+string(testNetworkName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveNetwork(t.Context(), testNetworkName)
	assert.Error(t, err)
}

func TestConnectNetwork(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.ConnectNetwork(t.Context(), testNetworkName, testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "connect", string(testNetworkName), string(testContainerName)}, m.Calls[0].Args)
}

func TestConnectNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.ConnectNetwork(t.Context(), testNetworkName, testContainerName)
	assert.Error(t, err)
}

func TestDisconnectNetwork(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.DisconnectNetwork(t.Context(), testNetworkName, testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "disconnect", string(testNetworkName), string(testContainerName)}, m.Calls[0].Args)
}

func TestDisconnectNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error response from daemon: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.DisconnectNetwork(t.Context(), testNetworkName, testContainerName)
	assert.Error(t, err)
}

// --- InspectNetwork ---

func TestInspectNetwork(t *testing.T) {
	var m MockExecutor
	m.AddResult(`[{"Name":"sind-dev-net","IPAM":{"Config":[{"Subnet":"172.18.0.0/16","Gateway":"172.18.0.1"}]}}]`, "", nil)
	c := NewClient(&m)

	info, err := c.InspectNetwork(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.Equal(t, testNetworkName, info.Name)
	assert.Equal(t, "172.18.0.0/16", info.Subnet)
	assert.Equal(t, "172.18.0.1", info.Gateway)
}

func TestInspectNetwork_NoIPAM(t *testing.T) {
	var m MockExecutor
	m.AddResult(`[{"Name":"sind-dev-net","IPAM":{"Config":[]}}]`, "", nil)
	c := NewClient(&m)

	info, err := c.InspectNetwork(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.Empty(t, info.Subnet)
	assert.Empty(t, info.Gateway)
}

func TestInspectNetwork_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	_, err := c.InspectNetwork(t.Context(), testNetworkName)
	assert.Error(t, err)
}

const networkLsJSON = `{"Name":"sind-dev-net","Driver":"bridge","ID":"abc123","Scope":"local"}
{"Name":"sind-mesh","Driver":"bridge","ID":"def456","Scope":"local"}
{"Name":"sind-prod-net","Driver":"bridge","ID":"ghi789","Scope":"local"}`

func TestListNetworks(t *testing.T) {
	var m MockExecutor
	m.AddResult(networkLsJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListNetworks(t.Context(), "name=sind-")
	require.NoError(t, err)
	require.Len(t, entries, 3)

	assert.Equal(t, NetworkName("sind-dev-net"), entries[0].Name)
	assert.Equal(t, "bridge", entries[0].Driver)
	assert.Equal(t, NetworkName("sind-mesh"), entries[1].Name)
	assert.Equal(t, NetworkName("sind-prod-net"), entries[2].Name)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "ls", "--format", "json", "--filter", "name=sind-"}, m.Calls[0].Args)
}

func TestListNetworks_Empty(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	entries, err := c.ListNetworks(t.Context(), "name=nonexistent")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestListNetworks_InvalidJSON(t *testing.T) {
	var m MockExecutor
	m.AddResult("not json\n", "", nil)
	c := NewClient(&m)

	entries, err := c.ListNetworks(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
	assert.Contains(t, err.Error(), "parsing network ls output")
}

func TestListNetworks_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	entries, err := c.ListNetworks(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
}
