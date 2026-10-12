// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// update rewrites the golden files: go test ./pkg/observability -update.
var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// golden compares got with testdata/name, or writes it there with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	file := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.WriteFile(file, []byte(got), 0o644))
	}
	want, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// testTargets are the nodes of a cluster with each kind of node: managed
// controller, worker and db node, and an unmanaged worker, out of order.
var testTargets = []Target{
	{Cluster: "dev", Node: "worker-0", Role: config.RoleWorker, Managed: true, Container: "sind-dev-worker-0", ContainerID: "bbbb"},
	{Cluster: "dev", Node: "controller", Role: config.RoleController, Managed: true, Container: "sind-dev-controller", ContainerID: "aaaa"},
	{Cluster: "dev", Node: "db", Role: config.RoleDB, Managed: true, Container: "sind-dev-db", ContainerID: "cccc"},
	{Cluster: "dev", Node: "worker-1", Role: config.RoleWorker, Container: "sind-dev-worker-1", ContainerID: "dddd"},
	{Cluster: "2nd", Node: "submitter", Role: config.RoleSubmitter, Managed: true, Container: "sind-2nd-submitter", ContainerID: "eeee"},
}

func TestGenerateAlloyConfig(t *testing.T) {
	golden(t, "sind.alloy", GenerateAlloyConfig("sind", "systemd", testTargets))
}

func TestGenerateAlloyConfig_Cgroupfs(t *testing.T) {
	got := GenerateAlloyConfig("sind", "cgroupfs", testTargets[:1])
	assert.Contains(t, got, `"/docker/bbbb", // sind-dev-worker-0`)
	assert.Contains(t, got, `regex         = "/docker/bbbb(/.*)?"`)
}

func TestGenerateAlloyConfig_NoTargets(t *testing.T) {
	golden(t, "empty.alloy", GenerateAlloyConfig("sind", "systemd", nil))
}

func TestGenerateAlloyConfig_NoSlurm(t *testing.T) {
	// An unmanaged worker gets its journal and cgroups collected, and no
	// Slurm scrape or probe.
	got := GenerateAlloyConfig("sind", "systemd", testTargets[3:4])
	assert.NotContains(t, got, "prometheus.scrape \"slurm")
	assert.NotContains(t, got, "blackbox")
	assert.Contains(t, got, `loki.source.journal "n_dev__worker_1"`)
	assert.Contains(t, got, `prometheus.exporter.cadvisor "nodes"`)
}

func TestSortTargets(t *testing.T) {
	sorted := sortTargets(testTargets)
	var names []string
	for _, s := range sorted {
		names = append(names, s.Cluster+"/"+s.Node)
	}
	assert.Equal(t, []string{"2nd/submitter", "dev/controller", "dev/db", "dev/worker-0", "dev/worker-1"}, names)
	assert.Equal(t, "worker-0", testTargets[0].Node, "the input stays as it is")
}

func TestComponentName(t *testing.T) {
	assert.Equal(t, "n_my_cluster__worker_0", componentName("my-cluster", "worker-0"))
	assert.Equal(t, "n_2nd__controller", componentName("2nd", "controller"))
}

func TestCgroupPath(t *testing.T) {
	assert.Equal(t, "/system.slice/docker-abc.scope", CgroupPath("systemd", "abc"))
	assert.Equal(t, "/docker/abc", CgroupPath("cgroupfs", "abc"))
}

func TestCollect(t *testing.T) {
	assert.Equal(t, Collection{Journal: true, Cgroups: true}, Collect(config.RoleController, false))
	assert.Equal(t, Collection{Journal: true, Cgroups: true}, Collect(config.RoleSubmitter, true))
	ctld := Collect(config.RoleController, true)
	assert.True(t, ctld.SlurmMetrics)
	assert.Len(t, ctld.Checks, 2)
	assert.Equal(t, []Check{{Service: "slurmd", Check: "ready", Module: "http", Port: "6818", Path: "/readyz"}}, Collect(config.RoleWorker, true).Checks)
	assert.Equal(t, []Check{{Service: "slurmdbd", Check: "tcp", Module: "tcp", Port: "6819"}}, Collect(config.RoleDB, true).Checks)
}
