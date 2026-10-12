// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// monitoredOnCall answers the calls of get cluster and get monitoring for
// pairOnCall's cluster dev, monitored by realm sind's stack, whose Alloy
// is stopped.
func monitoredOnCall(t *testing.T) func([]string, string) mock.Result {
	base := pairOnCall(t)
	stack := func(name string) bool {
		return slices.ContainsFunc(observability.Services, func(s observability.Service) bool {
			return string(observability.ContainerName("sind", s)) == name
		})
	}
	return func(args []string, stdin string) mock.Result {
		filters := testutil.ArgValues(args, "--filter")
		switch {
		case args[0] == "ps" && slices.Contains(filters, "label="+observability.LabelVersion):
			var entries []testutil.PsEntry
			for _, s := range observability.Services {
				entries = append(entries, testutil.PsEntry{Names: string(observability.ContainerName("sind", s)), State: "running"})
			}
			return mock.Result{Stdout: testutil.NDJSON(entries...)}
		case args[0] == "ps" && slices.Contains(filters, "volume=sind-journal"):
			return mock.Result{Stdout: testutil.NDJSON(
				testutil.PsEntry{Names: "sind-dev-controller", Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=26.05.4"},
				testutil.PsEntry{Names: "sind-dev-worker-0", Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=26.05.4"},
			)}
		case args[0] == "inspect" && stack(args[1]):
			var infos []map[string]any
			for i, name := range args[1:] {
				state, networks := "running", map[string]any{"sind-mesh": map[string]string{"IPAddress": "172.18.255.25" + string(rune('3'-i))}}
				if strings.HasSuffix(name, "-alloy") {
					state, networks = "exited", nil
				}
				infos = append(infos, map[string]any{
					"Name": "/" + name, "State": map[string]string{"Status": state},
					"Config":          map[string]string{"Image": "img-" + name},
					"NetworkSettings": map[string]any{"Networks": networks},
				})
			}
			b, _ := json.Marshal(infos)
			return mock.Result{Stdout: string(b)}
		case args[0] == "exec" && args[1] == "sind-grafana":
			return mock.Result{}
		case args[0] == "volume" && args[1] == "inspect":
			return mock.Result{Stdout: "[{}]"}
		case args[0] == "inspect":
			r := base(args, stdin)
			var infos []map[string]any
			require.NoError(t, json.Unmarshal([]byte(r.Stdout), &infos))
			for _, info := range infos {
				name := strings.TrimPrefix(info["Name"].(string), "/sind-dev-")
				info["Mounts"] = []map[string]string{{"Type": "volume", "Name": "sind-journal", "Destination": "/var/log/journal"}}
				info["HostConfig"] = map[string]any{"Mounts": []map[string]any{{"Type": "volume", "Source": "sind-journal", "Target": "/var/log/journal", "VolumeOptions": map[string]string{"Subpath": "dev/" + name}}}}
			}
			b, _ := json.Marshal(infos)
			return mock.Result{Stdout: string(b)}
		}
		return base(args, stdin)
	}
}

func TestGetCluster_Monitored(t *testing.T) {
	stdout, _, err := executeWithMock(&mock.Executor{OnCall: monitoredOnCall(t)}, "get", "cluster", "dev")
	require.NoError(t, err)
	assert.Regexp(t, `\ngrafana\s+sind-grafana\s+\x{2713}\n`, stdout)
	assert.Regexp(t, `\nalloy\s+sind-alloy\s+\x{2717}\n`, stdout)
	assert.Regexp(t, `/var/log/journal\s+sind-journal:dev/<node>\s+volume\s+\x{2713}`, stdout)

	stdout, _, err = executeWithMock(&mock.Executor{OnCall: monitoredOnCall(t)}, "get", "cluster", "dev", "-o", "json")
	require.NoError(t, err)
	var status map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &status))
	assert.Equal(t, true, status["monitoring"])
	network := status["network"].(map[string]any)
	assert.Equal(t, true, network["grafana_ok"])
	assert.Equal(t, false, network["alloy_ok"])
}

func TestGetMonitoring(t *testing.T) {
	stdout, _, err := executeWithMock(&mock.Executor{OnCall: monitoredOnCall(t)}, "get", "monitoring")
	require.NoError(t, err)
	assert.Equal(t, `SERVICE      CONTAINER         ADDRESS                            STATUS
grafana      sind-grafana      http://grafana.sind.sind:3000      running
prometheus   sind-prometheus   http://prometheus.sind.sind:9090   running
loki         sind-loki         http://loki.sind.sind:3100         running
alloy        sind-alloy        -                                  exited

CLUSTERS
NAME   NODES   SLURM
dev    2       26.05.4
`, stdout)

	stdout, _, err = executeWithMock(&mock.Executor{OnCall: monitoredOnCall(t)}, "get", "monitoring", "-o", "json")
	require.NoError(t, err)
	var m cluster.Monitoring
	require.NoError(t, json.Unmarshal([]byte(stdout), &m))
	assert.Equal(t, "sind", m.Realm)
	assert.Equal(t, cluster.MonitoringService{
		Name: observability.ServiceGrafana, Container: "sind-grafana", Image: "img-sind-grafana",
		IP: "172.18.255.253", URL: "http://grafana.sind.sind:3000", State: "running", Ready: true, Checked: true,
	}, m.Services[0])
	assert.Equal(t, []cluster.MonitoredCluster{{Name: "dev", Nodes: 2, SlurmVersion: "26.05.4"}}, m.Clusters)
}

func TestGetMonitoring_NoStack(t *testing.T) {
	m := &mock.Executor{OnCall: func([]string, string) mock.Result { return mock.Result{} }}
	_, _, err := executeWithMock(m, "get", "monitoring")
	require.EqualError(t, err, `no monitoring stack in realm "sind"`)
	assert.False(t, isUsageError(err))

	_, _, err = executeWithMock(m, "get", "monitoring", "-o", "yaml")
	require.Error(t, err)
	_, _, err = executeCommand("get", "monitoring", "extra")
	require.Error(t, err)
}

func TestFormatServiceState(t *testing.T) {
	assert.Equal(t, "missing", formatServiceState(cluster.MonitoringService{}))
	assert.Equal(t, "not ready", formatServiceState(cluster.MonitoringService{State: "running", Checked: true}))
	assert.Equal(t, "unchecked", formatServiceState(cluster.MonitoringService{State: "running"}))
	assert.Equal(t, "running", formatServiceState(cluster.MonitoringService{State: "running", Ready: true, Checked: true}))
	assert.Equal(t, "exited", formatServiceState(cluster.MonitoringService{State: "exited"}))
}
