// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
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
		rec.AddResult("6f02052f\n", "", nil)                                                                                    // create
		rec.AddResult("[{}]\n", "", nil)                                                                                        // exists → true
		rec.AddResult(n+"\n", "", nil)                                                                                          // remove
		rec.AddResult("", "Error response from daemon: network "+n+" not found\n", &exec.ExitError{ProcessState: exitCode1(t)}) // exists → false
		rec.AddResult("6f02052f\n", "", nil)                                                                                    // re-create
		rec.AddResult(`{"Name":"`+n+`","Driver":"bridge","ID":"x","Scope":"local"}`+"\n", "", nil)                              // list
		rec.AddResult("", "Error: network already exists\n", fmt.Errorf("exit status 1"))                                       // create duplicate (error)
		rec.AddResult("", "", nil)                                                                                              // list no matches (empty)
		rec.AddResult(n+"\n", "", nil)                                                                                          // remove (ok)
		rec.AddResult("", "Error: No such network\n", fmt.Errorf("exit status 1"))                                              // remove again (error)
	}
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), name) })

	// Create.
	id, err := c.CreateNetwork(ctx, name, nil)
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
	_, err = c.CreateNetwork(ctx, name, nil)
	require.NoError(t, err)

	entries, err := c.ListNetworks(ctx, "name="+n)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, name, entries[0].Name)

	// Create duplicate → error.
	_, err = c.CreateNetwork(ctx, name, nil)
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

// TestInspectNetworksLifecycle checks against docker that a batched inspect
// naming a missing network fails as not found but still returns the
// networks that exist.
func TestInspectNetworksLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	name := itNetworkName("inspect")
	missing := itNetworkName("inspect-missing")

	if !rec.IsIntegration() {
		rec.AddResult("net-id\n", "", nil) // create
		rec.AddResult(`[{"Name":"`+string(name)+`","Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.30.0.0/16","Gateway":"172.30.0.1"}]}}]`+"\n",
			"Error response from daemon: network "+string(missing)+" not found\n",
			&exec.ExitError{ProcessState: exitCode1(t)}) // inspect
		rec.AddResult(string(name)+"\n", "", nil) // remove (cleanup)
	}
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), name) })

	_, err := c.CreateNetwork(ctx, name, nil)
	require.NoError(t, err)

	infos, err := c.InspectNetworks(ctx, name, missing)
	require.Error(t, err)
	assert.True(t, IsNotFound(err), "missing network reported as not found: %v", err)
	require.Len(t, infos, 1)
	assert.Equal(t, name, infos[0].Name)
	assert.Equal(t, "bridge", infos[0].Driver)
	assert.NotEmpty(t, infos[0].Subnet)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// TestConfigOnlyNetworkLifecycle checks against docker what the realm lock
// relies on: a configuration-only network has no driver and no subnet, a
// second create with its name fails as already existing, and it is removed
// by ID.
func TestConfigOnlyNetworkLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	name := itNetworkName("config-only")
	labels := Labels{"sind.lock.pid": "42"}

	const id = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"
	if !rec.IsIntegration() {
		n := string(name)
		// create
		rec.AddResult(id+"\n", "", nil)
		// inspect meta
		rec.AddResult(`{"Name":"`+n+`","Id":"`+id+`","Created":"2026-10-04T10:02:03.5Z","Driver":"null","ConfigOnly":true,"Labels":{"sind.lock.pid":"42"}}`+"\n", "", nil)
		// inspect
		rec.AddResult(`[{"Name":"`+n+`","Driver":"null","IPAM":{"Driver":"default","Config":[]}}]`+"\n", "", nil)
		// create again
		rec.AddResult("", "Error response from daemon: network with name "+n+" already exists\n", &exec.ExitError{ProcessState: exitCode1(t)})
		// remove by ID
		rec.AddResult(id+"\n", "", nil)
		// inspect meta
		rec.AddResult("", "Error: No such network: "+n+"\n", &exec.ExitError{ProcessState: exitCode1(t)})
	}
	t.Cleanup(func() { _ = c.RemoveNetwork(context.Background(), name) })

	created, err := c.CreateConfigOnlyNetwork(ctx, name, labels)
	require.NoError(t, err)

	meta, err := c.InspectNetworkMeta(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, created, meta.ID)
	assert.Equal(t, name, meta.Name)
	assert.True(t, meta.ConfigOnly)
	assert.False(t, meta.Created.IsZero())
	assert.Equal(t, "42", meta.Labels["sind.lock.pid"])

	// No driver, so no bridge device, and no subnet from the address pools.
	info, err := c.InspectNetwork(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "null", info.Driver)
	assert.Empty(t, info.Subnet)

	_, err = c.CreateConfigOnlyNetwork(ctx, name, labels)
	require.Error(t, err)
	assert.True(t, IsAlreadyExists(err), "second create: %v", err)

	require.NoError(t, c.RemoveNetworkByID(ctx, created))
	_, err = c.InspectNetworkMeta(ctx, name)
	require.Error(t, err)
	assert.True(t, IsNotFound(err), "removed network reported as not found: %v", err)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestCreateConfigOnlyNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult("net-id\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateConfigOnlyNetwork(t.Context(), "sind-lock", Labels{"b": "2", "a": "1"})

	require.NoError(t, err)
	assert.Equal(t, NetworkID("net-id"), id)
	assert.Equal(t, []string{"network", "create", "--config-only", "--label", "a=1", "--label", "b=2", "sind-lock"}, m.Calls[0].Args)
}

func TestCreateConfigOnlyNetwork_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	id, err := c.CreateConfigOnlyNetwork(t.Context(), "sind-lock", nil)

	require.Error(t, err)
	assert.Empty(t, id)
}

func TestRemoveNetworkByID(t *testing.T) {
	var m mock.Executor
	m.AddResult("net-id\n", "", nil)
	c := NewClient(&m)

	require.NoError(t, c.RemoveNetworkByID(t.Context(), "net-id"))
	assert.Equal(t, []string{"network", "rm", "net-id"}, m.Calls[0].Args)
}

func TestInspectNetworkMeta(t *testing.T) {
	var m mock.Executor
	m.AddResult(`{"Name":"sind-lock","Id":"net-id","Created":"2026-10-04T10:02:03.123456789Z","ConfigOnly":true,"Labels":{"sind.realm":"sind"}}`+"\n", "", nil)
	c := NewClient(&m)

	meta, err := c.InspectNetworkMeta(t.Context(), "sind-lock")

	require.NoError(t, err)
	assert.Equal(t, &NetworkMeta{
		ID:         "net-id",
		Name:       "sind-lock",
		Created:    time.Date(2026, 10, 4, 10, 2, 3, 123456789, time.UTC),
		ConfigOnly: true,
		Labels:     Labels{"sind.realm": "sind"},
	}, meta)
	assert.Equal(t, []string{"network", "inspect", "sind-lock", "--format", "{{json .}}"}, m.Calls[0].Args)
}

func TestInspectNetworkMeta_NotFound(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such network: sind-lock\n", &exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	_, err := c.InspectNetworkMeta(t.Context(), "sind-lock")

	require.Error(t, err)
	assert.True(t, IsNotFound(err))
}

func TestInspectNetworkMeta_BadJSON(t *testing.T) {
	var m mock.Executor
	m.AddResult("[]\n", "", nil)
	c := NewClient(&m)

	_, err := c.InspectNetworkMeta(t.Context(), "sind-lock")

	require.ErrorContains(t, err, "parsing network inspect output")
}

func TestNetworkConnectDisconnectLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	net := itNetworkName("conn")
	ctr := itContainerName("conn")

	if !rec.IsIntegration() {
		rec.AddResult("net-id\n", "", nil)                  // create network
		rec.AddResult("ctr-id\n", "", nil)                  // run container
		rec.AddResult("", "", nil)                          // connect
		rec.AddResult(inspectRunning(string(ctr)), "", nil) // inspect (has net)
		rec.AddResult("", "", nil)                          // disconnect
		rec.AddResult(inspectRunning(string(ctr)), "", nil) // inspect (no net)
		rec.AddResult(string(ctr)+"\n", "", nil)            // kill (cleanup)
		rec.AddResult(string(ctr)+"\n", "", nil)            // rm (cleanup)
		rec.AddResult(string(net)+"\n", "", nil)            // rm network (cleanup)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctr)
		_ = c.RemoveContainer(bg, ctr)
		_ = c.RemoveNetwork(bg, net)
	})

	// Create network + container.
	_, err := c.CreateNetwork(ctx, net, nil)
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

func TestNetworkLabels(t *testing.T) {
	var m mock.Executor
	m.AddResult(`{"sind.cluster":"dev","sind.realm":"sind"}`+"\n", "", nil)
	c := NewClient(&m)

	labels, exists, err := c.NetworkLabels(t.Context(), testNetworkName)

	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, Labels{"sind.cluster": "dev", "sind.realm": "sind"}, labels)
	assert.Equal(t, []string{"network", "inspect", string(testNetworkName), "--format", "{{json .Labels}}"}, m.Calls[0].Args)
}

func TestNetworkLabels_NoLabels(t *testing.T) {
	for _, out := range []string{"", "null\n", "{}\n"} {
		var m mock.Executor
		m.AddResult(out, "", nil)
		c := NewClient(&m)

		labels, exists, err := c.NetworkLabels(t.Context(), testNetworkName)

		require.NoError(t, err, out)
		assert.True(t, exists, out)
		assert.Empty(t, labels, out)
	}
}

func TestNetworkLabels_NotFound(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such network: "+string(testNetworkName)+"\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	labels, exists, err := c.NetworkLabels(t.Context(), testNetworkName)

	require.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, labels)
}

func TestNetworkLabels_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	_, _, err := c.NetworkLabels(t.Context(), testNetworkName)

	require.Error(t, err)
}

func TestNetworkLabels_BadJSON(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil)
	c := NewClient(&m)

	_, _, err := c.NetworkLabels(t.Context(), testNetworkName)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing labels")
}

func TestNetworkExists_True(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil)
	c := NewClient(&m)

	exists, err := c.NetworkExists(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.True(t, exists)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", string(testNetworkName)}, m.Calls[0].Args)
}

func TestNetworkExists_False(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such network: "+string(testNetworkName)+"\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	exists, err := c.NetworkExists(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestNetworkExists_OtherError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	exists, err := c.NetworkExists(t.Context(), testNetworkName)
	assert.Error(t, err)
	assert.False(t, exists)
}

func TestCreateNetwork(t *testing.T) {
	const networkID NetworkID = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"

	var m mock.Executor
	m.AddResult(string(networkID)+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateNetwork(t.Context(), testNetworkName, nil)
	require.NoError(t, err)
	assert.Equal(t, networkID, id)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "create", string(testNetworkName)}, m.Calls[0].Args)
}

func TestCreateNetwork_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error response from daemon: network with name "+string(testNetworkName)+" already exists\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	id, err := c.CreateNetwork(t.Context(), testNetworkName, nil)
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestCreateNetworkWithSubnet(t *testing.T) {
	var m mock.Executor
	m.AddResult("net-id\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateNetworkWithSubnet(t.Context(), testNetworkName, Labels{"sind.realm": "sind"}, NetworkIPAM{
		Subnet: "172.18.0.0/16", Gateway: "172.18.0.1", IPRange: "172.18.0.0/17",
	})
	require.NoError(t, err)
	assert.Equal(t, NetworkID("net-id"), id)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"network", "create",
		"--subnet", "172.18.0.0/16", "--gateway", "172.18.0.1", "--ip-range", "172.18.0.0/17",
		"--label", "sind.realm=sind",
		string(testNetworkName),
	}, m.Calls[0].Args)
}

func TestCreateNetworkWithSubnet_SubnetOnly(t *testing.T) {
	var m mock.Executor
	m.AddResult("net-id\n", "", nil)
	c := NewClient(&m)

	_, err := c.CreateNetworkWithSubnet(t.Context(), testNetworkName, nil, NetworkIPAM{Subnet: "172.18.0.0/16"})
	require.NoError(t, err)
	assert.Equal(t, []string{"network", "create", "--subnet", "172.18.0.0/16", string(testNetworkName)}, m.Calls[0].Args)
}

func TestCreateNetworkWithSubnet_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error response from daemon: Pool overlaps with other one on this address space\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	id, err := c.CreateNetworkWithSubnet(t.Context(), testNetworkName, nil, NetworkIPAM{Subnet: "172.18.0.0/16"})
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestRemoveNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult(string(testNetworkName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveNetwork(t.Context(), testNetworkName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "rm", string(testNetworkName)}, m.Calls[0].Args)
}

func TestRemoveNetwork_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such network: "+string(testNetworkName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveNetwork(t.Context(), testNetworkName)
	assert.Error(t, err)
}

func TestConnectNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.ConnectNetwork(t.Context(), testNetworkName, testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "connect", string(testNetworkName), string(testContainerName)}, m.Calls[0].Args)
}

func TestConnectNetwork_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error response from daemon: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.ConnectNetwork(t.Context(), testNetworkName, testContainerName)
	assert.Error(t, err)
}

func TestDisconnectNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.DisconnectNetwork(t.Context(), testNetworkName, testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "disconnect", string(testNetworkName), string(testContainerName)}, m.Calls[0].Args)
}

func TestDisconnectNetwork_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error response from daemon: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.DisconnectNetwork(t.Context(), testNetworkName, testContainerName)
	assert.Error(t, err)
}

// --- InspectNetwork ---

func TestInspectNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Name":"sind-dev-net","Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.18.0.0/16","Gateway":"172.18.0.1"}]}}]`, "", nil)
	c := NewClient(&m)

	info, err := c.InspectNetwork(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.Equal(t, testNetworkName, info.Name)
	assert.Equal(t, "bridge", info.Driver)
	assert.Equal(t, "172.18.0.0/16", info.Subnet)
	assert.Equal(t, "172.18.0.1", info.Gateway)
}

func TestInspectNetwork_IPRangeAndLabels(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Name":"sind-mesh","Driver":"bridge","Labels":{"sind.dns.ip":"172.18.255.254"},`+
		`"IPAM":{"Config":[{"Subnet":"172.18.0.0/16","IPRange":"172.18.0.0/17","Gateway":"172.18.0.1"}]}}]`, "", nil)
	c := NewClient(&m)

	info, err := c.InspectNetwork(t.Context(), "sind-mesh")
	require.NoError(t, err)
	assert.Equal(t, "172.18.0.0/16", info.Subnet)
	assert.Equal(t, "172.18.0.0/17", info.IPRange)
	assert.Equal(t, Labels{"sind.dns.ip": "172.18.255.254"}, info.Labels)
}

func TestInspectNetwork_NoIPAM(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Name":"sind-dev-net","IPAM":{"Config":[]}}]`, "", nil)
	c := NewClient(&m)

	info, err := c.InspectNetwork(t.Context(), testNetworkName)
	require.NoError(t, err)
	assert.Empty(t, info.Subnet)
	assert.Empty(t, info.Gateway)
}

func TestInspectNetwork_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	_, err := c.InspectNetwork(t.Context(), testNetworkName)
	assert.Error(t, err)
}

func TestInspectNetwork_EmptyResult(t *testing.T) {
	var m mock.Executor
	m.AddResult("[]", "", nil)
	c := NewClient(&m)

	_, err := c.InspectNetwork(t.Context(), testNetworkName)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no results")
}

func TestInspectNetwork_InvalidJSON(t *testing.T) {
	var m mock.Executor
	m.AddResult("not-json", "", nil)
	c := NewClient(&m)

	_, err := c.InspectNetwork(t.Context(), testNetworkName)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing")
}

// --- InspectNetworks ---

func TestInspectNetworks(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Name":"sind-dev-net","Driver":"bridge","IPAM":{"Config":[{"Subnet":"172.18.0.0/16","Gateway":"172.18.0.1"}]}},`+
		`{"Name":"sind-mesh","Driver":"bridge","IPAM":{"Config":[]}}]`, "", nil)
	c := NewClient(&m)

	infos, err := c.InspectNetworks(t.Context(), "sind-dev-net", "sind-mesh")
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, &NetworkInfo{Name: "sind-dev-net", Driver: "bridge", Subnet: "172.18.0.0/16", Gateway: "172.18.0.1"}, infos[0])
	assert.Equal(t, &NetworkInfo{Name: "sind-mesh", Driver: "bridge"}, infos[1])

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", "sind-dev-net", "sind-mesh"}, m.Calls[0].Args)
}

func TestInspectNetworks_NoNames(t *testing.T) {
	var m mock.Executor
	c := NewClient(&m)

	infos, err := c.InspectNetworks(t.Context())
	require.NoError(t, err)
	assert.Nil(t, infos)
	assert.Empty(t, m.Calls)
}

// TestInspectNetworks_Partial covers a network removed between listing and
// inspecting: docker exits 1 but still prints the networks it found.
func TestInspectNetworks_Partial(t *testing.T) {
	var m mock.Executor
	m.AddResult(`[{"Name":"sind-mesh","Driver":"bridge","IPAM":{"Config":[]}}]`+"\n",
		"Error response from daemon: network sind-dev-net not found\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	infos, err := c.InspectNetworks(t.Context(), "sind-dev-net", "sind-mesh")
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
	require.Len(t, infos, 1)
	assert.Equal(t, NetworkName("sind-mesh"), infos[0].Name)
}

func TestInspectNetworks_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Cannot connect to the Docker daemon\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	infos, err := c.InspectNetworks(t.Context(), "sind-dev-net", "sind-mesh")
	require.Error(t, err)
	assert.Nil(t, infos)
}

const networkLsJSON = `{"Name":"sind-dev-net","Driver":"bridge","ID":"abc123","Scope":"local"}
{"Name":"sind-mesh","Driver":"bridge","ID":"def456","Scope":"local"}
{"Name":"sind-prod-net","Driver":"bridge","ID":"ghi789","Scope":"local"}`

func TestListNetworks(t *testing.T) {
	var m mock.Executor
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
	var m mock.Executor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	entries, err := c.ListNetworks(t.Context(), "name=nonexistent")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestListNetworks_InvalidJSON(t *testing.T) {
	var m mock.Executor
	m.AddResult("not json\n", "", nil)
	c := NewClient(&m)

	entries, err := c.ListNetworks(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
	assert.Contains(t, err.Error(), "parsing network ls output")
}

func TestListNetworks_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	entries, err := c.ListNetworks(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
}
