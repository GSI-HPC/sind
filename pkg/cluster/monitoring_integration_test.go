// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stackGet runs curl in the realm's Grafana container, which reaches the
// other services by container name, and returns what it printed.
func stackGet(ctx context.Context, c *docker.Client, realm string, args ...string) (string, error) {
	cmd := append([]string{"curl", "-sS", "--max-time", "10"}, args...)
	return c.Exec(ctx, observability.ContainerName(realm, observability.ServiceGrafana), cmd...)
}

// promResult is the part of a Prometheus or Loki query response the test
// reads.
type promResult struct {
	Data struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Stream map[string]string `json:"stream"`
			Value  []any             `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// stackQuery runs a query against Prometheus's or Loki's HTTP API and
// returns its result.
func stackQuery(ctx context.Context, t *testing.T, c *docker.Client, realm string, s observability.Service, path, query string) promResult {
	t.Helper()
	m := observability.NewManager(c, realm)
	out, err := stackGet(ctx, c, realm, "-G", m.URL(s, path), "--data-urlencode", "query="+query)
	var r promResult
	if err == nil {
		_ = json.Unmarshal([]byte(out), &r)
	}
	return r
}

// TestMonitoring creates a monitored cluster with a db node and checks
// that the stack runs and collects its metrics and logs, that Grafana
// serves its datasources and dashboards anonymously without password
// login, and that deleting the cluster removes the stack. With an image of
// Slurm 25.11, which serves no metrics, it checks that the create fails
// before any node exists and skips the rest.
func TestMonitoring(t *testing.T) {
	t.Parallel()
	c, _ := testutil.NewClient(t)
	ctx := t.Context()
	checkPrerequisites(t, c)

	version, err := slurm.DiscoverVersion(ctx, c, testutil.Image())
	require.NoError(t, err)

	name := "it-mon"
	realm := testutil.Realm(name)
	meshMgr := mesh.NewManager(c, realm)
	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, name)
		_ = cleanupRealm(bg, c, meshMgr)
	})
	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg := testConfig(t, name, "monitoring: true\nnodes: [controller, db, worker]\n")

	_, err = Create(ctx, c, meshMgr, cfg, probeInterval)
	if strings.HasPrefix(version, "25.") {
		require.ErrorIs(t, err, ErrImageUnsupported)
		nodes, lerr := c.ListContainers(ctx, "label="+LabelRealm+"="+realm, "label="+LabelCluster+"="+name)
		require.NoError(t, lerr)
		assert.Empty(t, nodes, "the probe fails before any node exists")
		t.Skipf("Slurm %s images serve no metrics; the create failed as it should", version)
	}
	require.NoError(t, err)

	// The stack runs, each container with its memory cap.
	for _, s := range observability.Services {
		info, err := c.InspectContainer(ctx, observability.ContainerName(realm, s))
		require.NoError(t, err, s)
		assert.Equal(t, docker.StateRunning, info.Status, s)
		assert.Positive(t, info.HostConfig.Memory, s)
	}
	mon, err := GetMonitoring(ctx, c, realm)
	require.NoError(t, err)
	require.Len(t, mon.Clusters, 1)
	mon.Clusters[0].Slurm = nil // Prometheus may not have scraped slurmctld yet
	assert.Equal(t, []MonitoredCluster{{Name: name, Nodes: 3, SlurmVersion: version}}, mon.Clusters)

	// Prometheus has slurmctld's metrics and the daemons' probes.
	prom := func(query string) promResult {
		return stackQuery(ctx, t, c, realm, observability.ServicePrometheus, "/api/v1/query", query)
	}
	assert.Eventually(t, func() bool {
		return len(prom(`up{job="slurm_nodes", cluster="`+name+`"} == 1`).Data.Result) == 1 &&
			len(prom(`slurm_nodes{cluster="`+name+`"}`).Data.Result) > 0
	}, 2*time.Minute, 2*time.Second, "no slurmctld metrics in Prometheus")
	// GetMonitoring reads the key numbers from them: the one worker is
	// Slurm's one node.
	assert.Eventually(t, func() bool {
		mon, err := GetMonitoring(ctx, c, realm)
		return err == nil && len(mon.Clusters) == 1 && mon.Clusters[0].Slurm != nil && mon.Clusters[0].Slurm.Nodes == 1
	}, time.Minute, 2*time.Second, "no Slurm numbers in GetMonitoring")
	assert.Eventually(t, func() bool {
		services := map[string]bool{}
		for _, r := range prom(`probe_success{cluster="` + name + `"} == 1`).Data.Result {
			services[r.Metric["service"]] = true
		}
		return services["slurmctld"] && services["slurmd"] && services["slurmdbd"]
	}, 2*time.Minute, 2*time.Second, "slurmctld, slurmd and slurmdbd not all probed")

	// Loki has the daemons' journals, each node's with its own node label.
	loki := func(query string) promResult {
		start := fmt.Sprint(time.Now().Add(-time.Hour).UnixNano())
		m := observability.NewManager(c, realm)
		out, err := stackGet(ctx, c, realm, "-G", m.URL(observability.ServiceLoki, "/loki/api/v1/query_range"),
			"--data-urlencode", "query="+query, "--data-urlencode", "start="+start, "--data-urlencode", "limit=10")
		var r promResult
		if err == nil {
			_ = json.Unmarshal([]byte(out), &r)
		}
		return r
	}
	for unit, node := range map[string]string{"slurmctld.service": "controller", "slurmdbd.service": "db", "mariadb.service": "db", "slurmd.service": "worker-0"} {
		assert.Eventually(t, func() bool {
			r := loki(`{cluster="` + name + `", unit="` + unit + `"}`)
			return len(r.Data.Result) > 0 && r.Data.Result[0].Stream["node"] == node
		}, 2*time.Minute, 2*time.Second, "no %s lines of %s in Loki", unit, node)
	}

	// Grafana: both datasources, served anonymously, no password login,
	// sind's dashboards, and no plugin installed at start.
	out, err := stackGet(ctx, c, realm, "http://localhost:3000/api/datasources")
	require.NoError(t, err)
	assert.Contains(t, out, `"uid":"`+observability.PrometheusUID+`"`)
	assert.Contains(t, out, `"uid":"`+observability.LokiUID+`"`)
	for _, uid := range []string{observability.PrometheusUID, observability.LokiUID} {
		out, err := stackGet(ctx, c, realm, "http://localhost:3000/api/datasources/uid/"+uid+"/health")
		require.NoError(t, err)
		assert.Contains(t, out, `"status":"OK"`, uid)
	}
	out, err = stackGet(ctx, c, realm, "-o", "/dev/null", "-w", "%{http_code}", "http://localhost:3000/explore")
	require.NoError(t, err)
	assert.Equal(t, "200", out)
	// admin:admin does not make a request a server admin's: server stats
	// stay forbidden, and of the server settings an org Admin only sees
	// the SMTP section.
	out, err = stackGet(ctx, c, realm, "-o", "/dev/null", "-w", "%{http_code}", "-u", "admin:admin", "http://localhost:3000/api/admin/stats")
	require.NoError(t, err)
	assert.Equal(t, "403", out)
	out, err = stackGet(ctx, c, realm, "-u", "admin:admin", "http://localhost:3000/api/admin/settings")
	require.NoError(t, err)
	var settings map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &settings), out)
	assert.NotContains(t, settings, "security")
	assert.NotContains(t, settings, "auth.basic")
	out, err = stackGet(ctx, c, realm, "http://localhost:3000/api/search?tag="+url.QueryEscape("sind"))
	require.NoError(t, err)
	assert.Contains(t, out, `"uid":"sind-slurm-cluster"`)
	assert.Contains(t, out, `"uid":"sind-logs"`)
	logs, err := c.Logs(ctx, observability.ContainerName(realm, observability.ServiceGrafana), 10000)
	require.NoError(t, err)
	assert.NotContains(t, logs, "Installing plugin")

	// Deleting the realm's last cluster removes the stack and its volumes.
	require.NoError(t, Delete(ctx, c, meshMgr, name))
	_, found, err := observability.NewManager(c, realm).Status(ctx)
	require.NoError(t, err)
	assert.False(t, found)
	for _, v := range []docker.VolumeName{observability.JournalVolume(realm), observability.VolumeName(realm, observability.ServiceLoki)} {
		exists, err := c.VolumeExists(ctx, v)
		require.NoError(t, err)
		assert.False(t, exists, v)
	}
}
