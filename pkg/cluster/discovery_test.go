// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ListClusterResources ---

// resourcesOnCall serves ListClusterResources, whose three lookups run
// concurrently: docker ps gets ps, the network inspect network and the
// volume listing volumes.
func resourcesOnCall(ps, network, volumes mock.Result) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "ps":
			return ps
		case args[0] == "network" && args[1] == "inspect":
			return network
		case args[0] == "volume" && args[1] == "ls":
			return volumes
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

// volumeLs is docker volume ls output listing the named, unlabeled volumes.
func volumeLs(names ...string) mock.Result {
	entries := make([]volumeEntry, len(names))
	for i, name := range names {
		entries[i] = volumeEntry{Name: name, Driver: "local"}
	}
	return mock.Result{Stdout: testutil.NDJSON(entries...)}
}

func TestListClusterResources(t *testing.T) {
	var m mock.Executor
	m.OnCall = resourcesOnCall(
		mock.Result{Stdout: testutil.NDJSON(
			testutil.PsEntry{ID: "abc123", Names: "sind-dev-controller", State: "running", Image: "sind-node:latest"},
			testutil.PsEntry{ID: "def456", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:latest"},
		)},
		mock.Result{}, // sind-dev-net exists
		volumeLs("sind-dev-home", "sind-dev-state", "sind-dev-data", "sind-dev-munge", "sind-dev-config"),
	)
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)

	// Containers
	require.Len(t, res.Containers, 2)
	assert.Equal(t, docker.ContainerName("sind-dev-controller"), res.Containers[0].Name)
	assert.Equal(t, docker.ContainerName("sind-dev-worker-0"), res.Containers[1].Name)

	// Network
	assert.Equal(t, docker.NetworkName("sind-dev-net"), res.Network)
	assert.True(t, res.NetworkExists)

	// Volumes, in creation order whatever order docker lists them in.
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-munge", "sind-dev-data", "sind-dev-state", "sind-dev-home"}, res.Volumes)

	// One call per kind of resource.
	require.Len(t, m.Calls, 3)
	var calls [][]string
	for _, call := range m.Calls {
		calls = append(calls, call.Args)
	}
	assert.ElementsMatch(t, [][]string{
		{"ps", "-a", "--no-trunc", "--format", "json", "--filter", "label=sind.realm=sind", "--filter", "label=sind.cluster=dev"},
		{"network", "inspect", "sind-dev-net", "--format", "{{json .Labels}}"},
		{"volume", "ls", "--format", "json", "--filter", "name=sind-dev-"},
	}, calls)
}

func TestListClusterResources_NoResources(t *testing.T) {
	var m mock.Executor
	m.OnCall = resourcesOnCall(
		mock.Result{}, // no containers
		mock.Result{Stderr: testutil.NoSuchNetwork("sind-nonexistent-net"), Err: testutil.ExitCode1(t)},
		mock.Result{}, // no volumes
	)
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "nonexistent")

	require.NoError(t, err)
	assert.Empty(t, res.Containers)
	assert.False(t, res.NetworkExists)
	assert.Empty(t, res.Volumes)
}

func TestListClusterResources_PartialVolumes(t *testing.T) {
	var m mock.Executor
	m.OnCall = resourcesOnCall(mock.Result{}, mock.Result{}, volumeLs("sind-dev-config", "sind-dev-data"))
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.True(t, res.NetworkExists)
	assert.Equal(t, []docker.VolumeName{"sind-dev-config", "sind-dev-data"}, res.Volumes)
}

// TestListClusterResources_SiblingClusterVolumes checks that the volumes of
// cluster "dev-2", which the name filter "sind-dev-" also lists, and other
// volumes with that prefix are not taken for cluster dev's.
func TestListClusterResources_SiblingClusterVolumes(t *testing.T) {
	var m mock.Executor
	m.OnCall = resourcesOnCall(mock.Result{}, mock.Result{},
		volumeLs("sind-dev-2-config", "sind-dev-2-data", "sind-dev-config", "sind-dev-scratch"))
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.Equal(t, []docker.VolumeName{"sind-dev-config"}, res.Volumes)
}

func TestListClusterResources_ListContainersError(t *testing.T) {
	var m mock.Executor
	m.OnCall = resourcesOnCall(mock.Result{Err: fmt.Errorf("docker ps failed")}, mock.Result{}, mock.Result{})
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

func TestListClusterResources_NetworkCheckError(t *testing.T) {
	var m mock.Executor
	m.OnCall = resourcesOnCall(mock.Result{}, mock.Result{Err: fmt.Errorf("network inspect failed")}, mock.Result{})
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking network sind-dev-net: network inspect failed")
}

func TestListClusterResources_VolumeListError(t *testing.T) {
	var m mock.Executor
	m.OnCall = resourcesOnCall(mock.Result{}, mock.Result{}, mock.Result{Err: fmt.Errorf("daemon gone")})
	c := docker.NewClient(&m)

	_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing volumes: daemon gone")
}

// TestListClusterResources_ErrorOrder checks that when every lookup fails,
// as with the daemon down, the error does not depend on which of the
// concurrent calls returns first.
func TestListClusterResources_ErrorOrder(t *testing.T) {
	for range 20 {
		var m mock.Executor
		m.OnCall = func(_ []string, _ string) mock.Result {
			return mock.Result{Err: fmt.Errorf("daemon gone")}
		}
		c := docker.NewClient(&m)

		_, err := ListClusterResources(t.Context(), c, mesh.DefaultRealm, "dev")

		require.Error(t, err)
		assert.Equal(t, "listing containers: daemon gone", err.Error())
	}
}

func TestListClusterResources_SkipsAnotherRealmsResources(t *testing.T) {
	// Realm "ci-42" with cluster "dev" and realm "ci" with cluster "42-dev"
	// both name their network and volumes "ci-42-dev-*". Deleting ci/42-dev
	// must leave ci-42/dev's alone.
	other := "sind.cluster=dev,sind.realm=ci-42"
	own := "sind.cluster=42-dev,sind.realm=ci"
	var m mock.Executor
	m.OnCall = resourcesOnCall(
		mock.Result{}, // no containers
		mock.Result{Stdout: `{"sind.cluster":"dev","sind.realm":"ci-42"}`},
		mock.Result{Stdout: testutil.NDJSON(
			volumeEntry{Name: "ci-42-dev-config", Driver: "local", Labels: other},
			volumeEntry{Name: "ci-42-dev-munge", Driver: "local", Labels: own},
			volumeEntry{Name: "ci-42-dev-data", Driver: "local"}, // unlabelled
		)},
	)
	c := docker.NewClient(&m)

	res, err := ListClusterResources(t.Context(), c, "ci", "42-dev")

	require.NoError(t, err)
	assert.False(t, res.NetworkExists)
	assert.Equal(t, []docker.VolumeName{"ci-42-dev-munge", "ci-42-dev-data"}, res.Volumes)
}

// --- concurrently ---

func TestConcurrently(t *testing.T) {
	var ran [3]bool
	err := concurrently(
		func() error { ran[0] = true; return nil },
		func() error { ran[1] = true; return nil },
		func() error { ran[2] = true; return nil },
	)
	require.NoError(t, err)
	assert.Equal(t, [3]bool{true, true, true}, ran)
}

func TestConcurrently_FirstErrorInArgumentOrder(t *testing.T) {
	second := make(chan struct{})
	var ranLast bool
	err := concurrently(
		func() error { return nil },
		func() error {
			// Fails after the third function has failed.
			<-second
			return fmt.Errorf("second")
		},
		func() error {
			defer close(second)
			return fmt.Errorf("third")
		},
		func() error { ranLast = true; return nil },
	)
	require.EqualError(t, err, "second")
	assert.True(t, ranLast, "a failure does not stop the others")
}

func TestOwnedBy(t *testing.T) {
	tests := []struct {
		name   string
		labels docker.Labels
		want   bool
	}{
		{"own", docker.Labels{LabelRealm: "ci", LabelCluster: "42-dev"}, true},
		{"unlabelled", nil, true},
		{"compose labels only", docker.Labels{"com.docker.compose.project": "ci-42-dev"}, true},
		{"other realm", docker.Labels{LabelRealm: "ci-42", LabelCluster: "dev"}, false},
		{"other cluster", docker.Labels{LabelRealm: "ci", LabelCluster: "42"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ownedBy(tt.labels, "ci", "42-dev"))
		})
	}
}

// --- HasOtherClusters ---

func TestHasOtherClusters(t *testing.T) {
	entry := func(name, labels string) testutil.PsEntry {
		return testutil.PsEntry{ID: name, Names: name, State: "running", Image: "img", Labels: labels}
	}
	for _, tt := range []struct {
		name string
		ps   []testutil.PsEntry
		want bool
	}{
		{"two clusters", []testutil.PsEntry{
			entry("sind-dev-controller", "sind.realm=sind,sind.cluster=dev"),
			entry("sind-prod-controller", "sind.realm=sind,sind.cluster=prod"),
		}, true},
		{"only this cluster", []testutil.PsEntry{
			entry("sind-dev-controller", "sind.realm=sind,sind.cluster=dev"),
			entry("sind-dev-worker-0", "sind.realm=sind,sind.cluster=dev"),
		}, false},
		{"no containers", nil, false},
		// Cluster "dev" must not match container "sind-dev2-controller".
		{"prefix ambiguity", []testutil.PsEntry{
			entry("sind-dev-controller", "sind.realm=sind,sind.cluster=dev"),
			entry("sind-dev2-controller", "sind.realm=sind,sind.cluster=dev2"),
		}, true},
		// After "sind delete cluster dev", the containers of cluster "dev-2"
		// share the name prefix "sind-dev-" but are another cluster: the
		// mesh must stay.
		{"hyphenated sibling", []testutil.PsEntry{
			entry("sind-dev-2-controller", "sind.realm=sind,sind.cluster=dev-2"),
			entry("sind-dev-2-worker-0", "sind.realm=sind,sind.cluster=dev-2"),
		}, true},
		// A realm container sind cannot attribute to a cluster keeps the mesh.
		{"container without cluster label", []testutil.PsEntry{entry("sind-dev-controller", "sind.realm=sind")}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult(testutil.NDJSON(tt.ps...), "", nil)

			has, err := HasOtherClusters(t.Context(), docker.NewClient(&m), mesh.DefaultRealm, "dev")

			require.NoError(t, err)
			assert.Equal(t, tt.want, has)
			assert.Equal(t, []string{"label=" + LabelRealm + "=" + mesh.DefaultRealm}, testutil.ArgValues(m.Calls[0].Args, "--filter"))
		})
	}
}

func TestHasOtherClusters_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := HasOtherClusters(t.Context(), c, mesh.DefaultRealm, "dev")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing containers")
}

// --- DiscoverClusterNames ---

// listsOnCall serves DiscoverClusterNames, whose two listings run
// concurrently.
func listsOnCall(networks, volumes mock.Result) func([]string, string) mock.Result {
	return func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "network" && args[1] == "ls":
			return networks
		case args[0] == "volume" && args[1] == "ls":
			return volumes
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}
}

func TestDiscoverClusterNames(t *testing.T) {
	for _, tt := range []struct {
		name              string
		networks, volumes mock.Result
		want              []string
		wantErr           string
	}{
		// The filter leaves out the mesh network, which has no cluster label.
		{name: "from networks", networks: mock.Result{Stdout: `{"Name":"sind-default-net","Driver":"bridge","Labels":"sind.realm=sind,sind.cluster=default"}`},
			want: []string{"default"}},
		// Cluster-scoped volumes carry the cluster label; the filter leaves
		// out the mesh volumes.
		{name: "from volumes", volumes: mock.Result{Stdout: `{"Name":"sind-default-config","Driver":"local","Labels":"sind.realm=sind,sind.cluster=default"}
{"Name":"sind-default-munge","Driver":"local","Labels":"sind.realm=sind,sind.cluster=default"}`},
			want: []string{"default"}},
		// Two cluster networks, volumes of dev and an orphaned one of test.
		{name: "multiple clusters", networks: mock.Result{Stdout: `{"Name":"sind-dev-net","Driver":"bridge","Labels":"sind.realm=sind,sind.cluster=dev"}
{"Name":"sind-prod-net","Driver":"bridge","Labels":"sind.realm=sind,sind.cluster=prod"}`},
			volumes: mock.Result{Stdout: `{"Name":"sind-dev-config","Driver":"local","Labels":"sind.realm=sind,sind.cluster=dev"}
{"Name":"sind-test-data","Driver":"local","Labels":"sind.realm=sind,sind.cluster=test"}`},
			want: []string{"dev", "prod", "test"}},
		{name: "empty"},
		{name: "network error", networks: mock.Result{Err: fmt.Errorf("docker daemon unreachable")}, wantErr: "listing networks"},
		{name: "volume error", volumes: mock.Result{Err: fmt.Errorf("docker daemon unreachable")}, wantErr: "listing volumes"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.OnCall = listsOnCall(tt.networks, tt.volumes)

			names, err := DiscoverClusterNames(t.Context(), docker.NewClient(&m), mesh.DefaultRealm)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, names)
		})
	}
}
