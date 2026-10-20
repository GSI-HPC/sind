// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetClusters(t *testing.T) {
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
		testutil.PsEntry{
			ID: "c", Names: "sind-dev-worker-1", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "d", Names: "sind-prod-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=controller,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "h", Names: "sind-prod-db", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=db,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "e", Names: "sind-prod-submitter", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=submitter,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "f", Names: "sind-prod-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=worker,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "g", Names: "sind-prod-worker-1", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=worker,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "i", Names: "sind-prod-api", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=api,sind.slurm.version=25.11.0",
		},
	), "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	require.Len(t, clusters, 2)

	// Results should be sorted by name.
	assert.Equal(t, "dev", clusters[0].Name)
	assert.Equal(t, "25.11.0", clusters[0].SlurmVersion)
	assert.Equal(t, StateRunning, clusters[0].State)
	assert.Equal(t, 3, clusters[0].NodeCount)
	assert.Equal(t, 0, clusters[0].Submitters)
	assert.Equal(t, 1, clusters[0].Controllers)
	assert.Equal(t, 0, clusters[0].DBs)
	assert.Equal(t, 0, clusters[0].APIs)
	assert.Equal(t, 2, clusters[0].Workers)

	assert.Equal(t, "prod", clusters[1].Name)
	assert.Equal(t, "25.11.0", clusters[1].SlurmVersion)
	assert.Equal(t, StateRunning, clusters[1].State)
	assert.Equal(t, 6, clusters[1].NodeCount)
	assert.Equal(t, 1, clusters[1].Submitters)
	assert.Equal(t, 1, clusters[1].Controllers)
	assert.Equal(t, 1, clusters[1].DBs)
	assert.Equal(t, 1, clusters[1].APIs)
	assert.Equal(t, 2, clusters[1].Workers)
}

func TestGetClusters_LabelFilter(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, _ = GetClusters(t.Context(), c, mesh.DefaultRealm)

	require.Len(t, m.Calls, 1)
	args := m.Calls[0].Args
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label="+LabelRealm+"="+mesh.DefaultRealm, args[filterIdx+1])
}

func TestGetClusters_NoSlurmVersion(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
	), "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, "", clusters[0].SlurmVersion)
}

func TestGetClusters_EmptyClusterLabel(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-orphan", State: "running", Image: "img",
			Labels: "sind.cluster=,sind.role=worker",
		},
	), "", nil)
	c := docker.NewClient(&m)

	clusters, err := GetClusters(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	require.Len(t, clusters, 1)
	assert.Equal(t, "dev", clusters[0].Name)
	assert.Equal(t, 1, clusters[0].NodeCount)
}

// --- GetNodes ---

func TestGetNodes(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-1", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
		testutil.PsEntry{
			ID: "c", Names: "sind-dev-worker-0", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
	), "", nil)
	m.AddResult(inspectJSONBatch(t,
		inspectEntry{Name: "sind-dev-controller", Status: "running", Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.0.2"}},
		inspectEntry{Name: "sind-dev-worker-1", Status: "running", Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.0.4"}},
		inspectEntry{Name: "sind-dev-worker-0", Status: "running", Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.0.3"}},
	), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, nodes, 3)

	// Sorted: controller first, then worker by name.
	assert.Equal(t, "sind-dev-controller", nodes[0].Container)
	assert.Equal(t, "dev", nodes[0].Cluster)
	assert.Equal(t, config.RoleController, nodes[0].Role)
	assert.Equal(t, "controller.dev.sind.sind", nodes[0].FQDN)
	assert.Equal(t, "10.0.0.2", nodes[0].IP)
	assert.Equal(t, StateRunning, nodes[0].State)

	assert.Equal(t, "sind-dev-worker-0", nodes[1].Container)
	assert.Equal(t, config.RoleWorker, nodes[1].Role)
	assert.Equal(t, "10.0.0.3", nodes[1].IP)

	assert.Equal(t, "sind-dev-worker-1", nodes[2].Container)
	assert.Equal(t, config.RoleWorker, nodes[2].Role)
	assert.Equal(t, "10.0.0.4", nodes[2].IP)
}

func TestGetNodes_WithStatus(t *testing.T) {
	net := map[docker.NetworkName]string{"sind-dev-net": "10.0.0.2"}
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "exited", Image: "img",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
		testutil.PsEntry{
			ID: "c", Names: "sind-dev-worker-1", State: "paused", Image: "img",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
	), "", nil)
	m.AddResult(inspectJSONBatch(t,
		inspectEntry{Name: "sind-dev-controller", Status: "running", Networks: net},
		inspectEntry{Name: "sind-dev-worker-0", Status: "exited", Networks: net},
		inspectEntry{Name: "sind-dev-worker-1", Status: "paused", Networks: net},
	), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, nodes, 3)
	assert.Equal(t, StateRunning, nodes[0].State)
	assert.Equal(t, StateStopped, nodes[1].State)
	assert.Equal(t, StatePaused, nodes[2].State)
}

// TestGetNodes_Empty checks that a cluster whose containers are gone but
// whose network is left (a partial teardown) lists no nodes, as [] in JSON.
func TestGetNodes_Empty(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	m.AddResult(`[{"Id":"abc","Name":"sind-dev-net"}]`, "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	assert.NotNil(t, nodes, "empty result must be [] not nil so json emits []")
	assert.Empty(t, nodes)
}

func TestGetNodes_ClusterNotFound(t *testing.T) {
	c := docker.NewClient(notFoundExecutor(t, mock.Result{}))

	nodes, err := GetNodes(t.Context(), c, "ci-42", "dev")

	require.EqualError(t, err, `cluster "dev" not found in realm "ci-42"`)
	assert.ErrorIs(t, err, ErrClusterNotFound)
	assert.Nil(t, nodes)
}

func TestGetNodes_NetworkError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	_, err := GetNodes(t.Context(), c, mesh.DefaultRealm, "dev")

	require.ErrorContains(t, err, "checking cluster network")
}

func TestGetNodes_LabelFilter(t *testing.T) {
	var m mock.Executor
	m.AddResult(psLine("sind", "myCluster", "controller"), "", nil)
	c := docker.NewClient(&m)

	_, _ = GetNodes(t.Context(), c, mesh.DefaultRealm, "myCluster")

	require.NotEmpty(t, m.Calls)
	args := m.Calls[0].Args
	assert.Contains(t, args, "label=sind.realm=sind")
	assert.Contains(t, args, "label=sind.cluster=myCluster")
}

// TestGetNodes_InspectError verifies that a failed batched inspect degrades
// gracefully: summaries are still returned with empty IPs, and the error is
// surfaced via logging (not propagated) so listings and shell completion
// keep working even when the Docker daemon hiccups on inspect.
func TestGetNodes_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
	), "", nil)
	m.AddResult("", "", fmt.Errorf("docker daemon busy"))
	c := docker.NewClient(&m)

	nodes, err := GetNodes(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "sind-dev-controller", nodes[0].Container)
	assert.Empty(t, nodes[0].IP)
}

// --- GetAllNodes ---

func TestGetAllNodes(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-prod-controller", State: "running", Image: "img",
			Labels: "sind.cluster=prod,sind.role=controller",
		},
	), "", nil)
	m.AddResult(inspectJSONBatch(t,
		inspectEntry{Name: "sind-dev-controller", Status: "running", Networks: map[docker.NetworkName]string{"sind-dev-net": "10.0.0.2"}},
		inspectEntry{Name: "sind-prod-controller", Status: "running", Networks: map[docker.NetworkName]string{"sind-prod-net": "10.1.0.2"}},
	), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetAllNodes(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "dev", nodes[0].Cluster)
	assert.Equal(t, "prod", nodes[1].Cluster)
}

func TestGetAllNodes_SortOrder(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "a", Names: "sind-prod-worker-10", State: "running", Image: "img",
			Labels: "sind.cluster=prod,sind.role=worker"},
		testutil.PsEntry{ID: "b", Names: "sind-prod-worker-2", State: "running", Image: "img",
			Labels: "sind.cluster=prod,sind.role=worker"},
		testutil.PsEntry{ID: "c", Names: "sind-dev-worker-0", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=worker"},
		testutil.PsEntry{ID: "d", Names: "sind-prod-controller", State: "running", Image: "img",
			Labels: "sind.cluster=prod,sind.role=controller"},
		testutil.PsEntry{ID: "e", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller"},
		testutil.PsEntry{ID: "f", Names: "sind-dev-mystery", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=mystery"},
		testutil.PsEntry{ID: "g", Names: "sind-dev-submitter", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=submitter"},
		testutil.PsEntry{ID: "h", Names: "sind-dev-api", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=api"},
		testutil.PsEntry{ID: "i", Names: "sind-dev-db", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=db"},
	), "", nil)
	m.AddResult("[]", "", nil) // inspect: IPs are not under test
	c := docker.NewClient(&m)

	nodes, err := GetAllNodes(t.Context(), c, mesh.DefaultRealm)
	require.NoError(t, err)
	require.Len(t, nodes, 9)

	// Expected order: dev cluster first (alphabetically), within a cluster
	// controller, db, api, submitter, worker, then unknown roles, and
	// worker-2 before worker-10 (natural order). Each node has the role of
	// its label, an unknown one too.
	got := make([]string, len(nodes))
	for i, n := range nodes {
		got[i] = n.Container + " " + string(n.Role)
	}
	assert.Equal(t, []string{
		"sind-dev-controller controller",
		"sind-dev-db db",
		"sind-dev-api api",
		"sind-dev-submitter submitter",
		"sind-dev-worker-0 worker",
		"sind-dev-mystery mystery",
		"sind-prod-controller controller",
		"sind-prod-worker-2 worker",
		"sind-prod-worker-10 worker",
	}, got)
}

func TestGetAllNodes_OnlyOrphanContainers(t *testing.T) {
	// Containers exist but none carry the sind.cluster label, so they should
	// all be skipped and the result is an empty slice.
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "a", Names: "orphan-1", State: "running", Image: "img"},
		testutil.PsEntry{ID: "b", Names: "orphan-2", State: "running", Image: "img"},
	), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetAllNodes(t.Context(), c, mesh.DefaultRealm)
	require.NoError(t, err)
	assert.NotNil(t, nodes, "empty result must be [] not nil so json emits []")
	assert.Empty(t, nodes)
}

// --- GetNodeNames ---

func TestGetNodeNames(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{ID: "a", Names: "sind-prod-worker-10", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=prod,sind.role=worker"},
		testutil.PsEntry{ID: "b", Names: "sind-prod-worker-2", State: "exited", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=prod,sind.role=worker"},
		testutil.PsEntry{ID: "c", Names: "sind-dev-10-worker-0", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=dev-10,sind.role=worker"},
		testutil.PsEntry{ID: "d", Names: "sind-dns", State: "running", Image: "img",
			Labels: "sind.realm=sind"},
		testutil.PsEntry{ID: "e", Names: "sind-dev-2-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=dev-2,sind.role=controller"},
		testutil.PsEntry{ID: "f", Names: "sind-prod-db", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=prod,sind.role=db"},
		testutil.PsEntry{ID: "g", Names: "sind-prod-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=prod,sind.role=controller"},
	), "", nil)
	c := docker.NewClient(&m)

	names, err := GetNodeNames(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	// Clusters in string order, as the cluster discovery sorted them, then
	// role and natural name order within each cluster. The mesh DNS
	// container has no cluster and is left out.
	assert.Equal(t, []string{
		"worker-0.dev-10",
		"controller.dev-2",
		"controller.prod",
		"db.prod",
		"worker-2.prod",
		"worker-10.prod",
	}, names)

	// One listing of the realm's containers, no inspect.
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"ps", "-a", "--no-trunc", "--format", "json", "--filter", "label=sind.realm=sind"}, m.Calls[0].Args)
}

func TestGetNodes_SkipsEmptyClusterLabel(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "orphan", State: "running", Image: "img",
		},
	), "", nil)
	m.AddResult(inspectJSON(t, "sind-dev-controller", "running", map[docker.NetworkName]string{"sind-dev-net": "10.0.0.2"}), "", nil)
	c := docker.NewClient(&m)

	nodes, err := GetNodes(t.Context(), c, mesh.DefaultRealm, "dev")

	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "sind-dev-controller", nodes[0].Container)
}

func TestNaturalSortKey(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "worker numeric suffix",
			input: []string{"worker-10", "worker-2", "worker-1", "worker-0", "worker-11"},
			want:  []string{"worker-0", "worker-1", "worker-2", "worker-10", "worker-11"},
		},
		{
			name:  "plain strings unchanged",
			input: []string{"controller", "beta", "alpha"},
			want:  []string{"alpha", "beta", "controller"},
		},
		{
			name:  "mixed digit runs",
			input: []string{"a1b20c3", "a1b3c30", "a1b3c3"},
			want:  []string{"a1b3c3", "a1b3c30", "a1b20c3"},
		},
		{
			name:  "empty string",
			input: []string{"b", "", "a"},
			want:  []string{"", "a", "b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := append([]string(nil), tc.input...)
			sort.Slice(got, func(i, j int) bool {
				return naturalSortKey(got[i]) < naturalSortKey(got[j])
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestContainerStateToState(t *testing.T) {
	tests := []struct {
		state  docker.ContainerState
		status State
	}{
		{docker.StateRunning, StateRunning},
		{docker.StatePaused, StatePaused},
		{docker.StateExited, StateStopped},
		{docker.StateDead, StateStopped},
		{docker.StateCreated, StateStopped},
		{"restarting", StateUnknown},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			assert.Equal(t, tt.status, containerStateToState(tt.state))
		})
	}
}

// --- GetNetworks ---

type networkEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
}

// networkInspectEntry is one network of docker network inspect output.
func networkInspectEntry(name, subnet, gateway string) string {
	return fmt.Sprintf(`{"Name":%q,"IPAM":{"Config":[{"Subnet":%q,"Gateway":%q}]}}`, name, subnet, gateway)
}

func TestGetNetworks(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		networkEntry{Name: "sind-prod-net", Driver: "bridge"},
		networkEntry{Name: "sind-lock", Driver: "null"}, // the realm lock, held by a command
		networkEntry{Name: "sind-dev-net", Driver: "bridge"},
		networkEntry{Name: "sind-mesh", Driver: "bridge"},
	), "", nil)
	m.AddResult("["+strings.Join([]string{
		networkInspectEntry("sind-prod-net", "172.20.0.0/16", "172.20.0.1"),
		networkInspectEntry("sind-dev-net", "172.18.0.0/16", "172.18.0.1"),
		networkInspectEntry("sind-mesh", "172.19.0.0/16", "172.19.0.1"),
	}, ",")+"]", "", nil)
	c := docker.NewClient(&m)

	networks, err := GetNetworks(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	// Sorted by name.
	assert.Equal(t, []*NetworkSummary{
		{Name: "sind-dev-net", Driver: "bridge", Subnet: "172.18.0.0/16", Gateway: "172.18.0.1"},
		{Name: "sind-mesh", Driver: "bridge", Subnet: "172.19.0.0/16", Gateway: "172.19.0.1"},
		{Name: "sind-prod-net", Driver: "bridge", Subnet: "172.20.0.0/16", Gateway: "172.20.0.1"},
	}, networks)

	// One inspect for all networks; the lock is left out.
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", "sind-prod-net", "sind-dev-net", "sind-mesh"}, m.Calls[1].Args)
}

// TestGetNetworks_InspectPartial covers a network removed between listing
// and inspecting: it is still listed, without IPAM details.
func TestGetNetworks_InspectPartial(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		networkEntry{Name: "sind-dev-net", Driver: "bridge"},
		networkEntry{Name: "sind-mesh", Driver: "bridge"},
	), "", nil)
	m.AddResult("["+networkInspectEntry("sind-mesh", "172.19.0.0/16", "172.19.0.1")+"]\n",
		testutil.NoSuchNetwork("sind-dev-net"), testutil.ExitCode1(t))
	c := docker.NewClient(&m)

	networks, err := GetNetworks(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	assert.Equal(t, []*NetworkSummary{
		{Name: "sind-dev-net", Driver: "bridge"},
		{Name: "sind-mesh", Driver: "bridge", Subnet: "172.19.0.0/16", Gateway: "172.19.0.1"},
	}, networks)
}

// TestGetNetworks_InspectError checks that IPAM details are best-effort:
// a failed inspect leaves them empty instead of failing the listing.
func TestGetNetworks_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(networkEntry{Name: "sind-dev-net", Driver: "bridge"}), "", nil)
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	c := docker.NewClient(&m)

	networks, err := GetNetworks(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	assert.Equal(t, []*NetworkSummary{{Name: "sind-dev-net", Driver: "bridge"}}, networks)
}

// --- GetVolumes ---

type volumeEntry struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Labels string `json:"Labels,omitempty"`
}

func TestGetVolumes(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		volumeEntry{Name: "sind-dev-data", Driver: "local"},
		volumeEntry{Name: "sind-dev-config", Driver: "local"},
		volumeEntry{Name: "sind-dev-munge", Driver: "local"},
		volumeEntry{Name: "sind-ssh-config", Driver: "local"},
	), "", nil)
	c := docker.NewClient(&m)

	volumes, err := GetVolumes(t.Context(), c, mesh.DefaultRealm)

	require.NoError(t, err)
	require.Len(t, volumes, 4)
	// Sorted by name.
	assert.Equal(t, "sind-dev-config", volumes[0].Name)
	assert.Equal(t, "local", volumes[0].Driver)
	assert.Equal(t, "sind-dev-data", volumes[1].Name)
	assert.Equal(t, "sind-dev-munge", volumes[2].Name)
	assert.Equal(t, "sind-ssh-config", volumes[3].Name)
}

// --- GetAuthKey ---

func TestGetAuthKey(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "c1", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller",
	}), "", nil)
	keyData := "secret-munge-key-bytes"
	m.AddResult(testutil.TarArchive("munge.key", keyData), "", nil)
	c := docker.NewClient(&m)

	key, err := GetAuthKey(t.Context(), c, mesh.DefaultRealm, "dev", "")

	require.NoError(t, err)
	assert.Equal(t, &AuthKey{Type: AuthMunge, Key: []byte(keyData)}, key)
	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"cp", "sind-dev-controller:/etc/munge/munge.key", "-"}, m.Calls[1].Args)
}

func TestGetAuthKey_NoContainers(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, err := GetAuthKey(t.Context(), c, mesh.DefaultRealm, "dev", "")

	require.Error(t, err)
	assert.EqualError(t, err, `cluster "dev" not found in realm "sind"`)
	assert.ErrorIs(t, err, ErrClusterNotFound)
}

func TestGetAuthKey_CopyError(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "c1", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller",
	}), "", nil)
	m.AddResult("", "", fmt.Errorf("cp failed"))
	c := docker.NewClient(&m)

	_, err := GetAuthKey(t.Context(), c, mesh.DefaultRealm, "dev", "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading munge key")
}

// --- GetRealms ---

func TestGetRealms(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=dev,sind.role=worker",
		},
		testutil.PsEntry{
			ID: "c", Names: "sind-prod-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=prod,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "d", Names: "ci-42-default-controller", State: "running", Image: "img",
			Labels: "sind.realm=ci-42,sind.cluster=default,sind.role=controller",
		},
	), "", nil)
	c := docker.NewClient(&m)

	realms, err := GetRealms(t.Context(), c)

	require.NoError(t, err)
	require.Len(t, realms, 2)
	assert.Equal(t, "ci-42", realms[0].Name)
	assert.Equal(t, 1, realms[0].Clusters)
	assert.Equal(t, "sind", realms[1].Name)
	assert.Equal(t, 2, realms[1].Clusters)
}

func TestGetRealms_LabelFilter(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)

	_, _ = GetRealms(t.Context(), c)

	require.Len(t, m.Calls, 1)
	args := m.Calls[0].Args
	filterIdx := indexOf(args, "--filter")
	require.Greater(t, filterIdx, -1)
	assert.Equal(t, "label="+LabelRealm, args[filterIdx+1])
}

func TestGetRealms_SkipsEmptyRealmLabel(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "orphan", State: "running", Image: "img",
			Labels: "sind.cluster=dev",
		},
	), "", nil)
	c := docker.NewClient(&m)

	realms, err := GetRealms(t.Context(), c)

	require.NoError(t, err)
	require.Len(t, realms, 1)
	assert.Equal(t, "sind", realms[0].Name)
}

// TestGet_Listing checks that each Get fails with the error of its docker
// listing, and that an empty listing gives [] rather than nil, so that
// JSON emits [].
func TestGet_Listing(t *testing.T) {
	realm := mesh.DefaultRealm
	tests := []struct {
		name  string
		get   func(context.Context, *docker.Client) (any, error)
		want  string // error of a failed listing
		empty bool   // whether an empty listing is a result
	}{
		{"GetClusters", func(ctx context.Context, c *docker.Client) (any, error) { return GetClusters(ctx, c, realm) }, "listing containers", true},
		{"GetAllNodes", func(ctx context.Context, c *docker.Client) (any, error) { return GetAllNodes(ctx, c, realm) }, "listing containers", true},
		{"GetNodeNames", func(ctx context.Context, c *docker.Client) (any, error) { return GetNodeNames(ctx, c, realm) }, "listing containers", true},
		{"GetNetworks", func(ctx context.Context, c *docker.Client) (any, error) { return GetNetworks(ctx, c, realm) }, "listing networks", true},
		{"GetVolumes", func(ctx context.Context, c *docker.Client) (any, error) { return GetVolumes(ctx, c, realm) }, "listing volumes", true},
		{"GetRealms", func(ctx context.Context, c *docker.Client) (any, error) { return GetRealms(ctx, c) }, "listing containers", true},
		{"GetNodes", func(ctx context.Context, c *docker.Client) (any, error) { return GetNodes(ctx, c, realm, "dev") }, "listing containers", false},
		{"GetAuthKey", func(ctx context.Context, c *docker.Client) (any, error) { return GetAuthKey(ctx, c, realm, "dev", "") }, "listing containers", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			m.AddResult("", "", fmt.Errorf("docker daemon not running"))
			_, err := tt.get(t.Context(), docker.NewClient(&m))
			require.ErrorContains(t, err, tt.want)
			if !tt.empty {
				return
			}
			m = mock.Executor{}
			m.AddResult("", "", nil)
			got, err := tt.get(t.Context(), docker.NewClient(&m))
			require.NoError(t, err)
			assert.NotNil(t, got)
			assert.Empty(t, got)
		})
	}
}

func TestAggregateState(t *testing.T) {
	running, exited := docker.StateRunning, docker.StateExited
	assert.Equal(t, StateEmpty, aggregateState(nil))
	assert.Equal(t, StateRunning, aggregateState([]docker.ContainerState{running}))
	assert.Equal(t, StateStopped, aggregateState([]docker.ContainerState{exited, exited}))
	assert.Equal(t, StateMixed, aggregateState([]docker.ContainerState{running, exited}))
}
