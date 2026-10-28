// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/require"
)

// moreNodes returns the nodes of a cluster of the realm: one controller
// and workers, of which the worker at index down is stopped (-1: none).
func moreNodes(realm, name string, workers, down int) []cluster.NodeSummary {
	prefix := cluster.ContainerPrefix(realm, name)
	nodes := []cluster.NodeSummary{{Container: prefix + "controller", Cluster: name, Role: config.RoleController, FQDN: "controller." + name + "." + realm + ".sind", IP: "172.19.0.2", State: cluster.StateRunning}}
	for i := 0; i < workers; i++ {
		short := "worker-" + string(rune('0'+i))
		st := cluster.StateRunning
		if i == down {
			st = cluster.StateStopped
		}
		nodes = append(nodes, cluster.NodeSummary{Container: prefix + short, Cluster: name, Role: config.RoleWorker, FQDN: short + "." + name + "." + realm + ".sind", IP: "172.19.0." + string(rune('3'+i)), State: st})
	}
	return nodes
}

// moreFixture fills the store with two realms: "sind" with a mesh, a
// monitoring stack, a stopped worker, a connected container, and "ci"
// with one cluster and no mesh information. The host check has a warning.
func moreFixture(t *testing.T, st *store.Store) {
	t.Helper()
	st.SetDocker(store.DockerState{Checked: true})
	st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 3}, {Name: "ci", Clusters: 1}})
	st.SetClusters("sind", []cluster.Summary{
		{Name: "default", SlurmVersion: "26.05.1", State: cluster.StateRunning, NodeCount: 2},
		{Name: "dev", SlurmVersion: "26.05.1", State: cluster.StateMixed, NodeCount: 4},
		{Name: "empty", SlurmVersion: "26.05.1", State: cluster.StateEmpty},
	})
	st.SetNodes("sind", append(moreNodes("sind", "default", 1, -1), moreNodes("sind", "dev", 3, 1)...))
	st.SetNetworks("sind", []cluster.NetworkSummary{
		{Name: "sind-mesh", Driver: "bridge", Subnet: "172.18.0.0/16", Gateway: "172.18.0.1"},
		{Name: "sind-default-net", Driver: "bridge", Subnet: "172.19.0.0/16", Gateway: "172.19.0.1"},
		{Name: "sind-dev-net", Driver: "bridge", Subnet: "172.20.0.0/16", Gateway: "172.20.0.1"},
	})
	st.SetMesh("sind", store.Mesh{
		Info: &mesh.Info{Network: "sind-mesh", DNSContainer: "sind-dns", DNSIP: "172.18.255.254", DNSZone: "sind.sind", DNSImage: "coredns/coredns:1.12",
			SSHContainer: "sind-ssh", SSHVolume: "sind-ssh-config", SSHImage: "sind-node:latest"},
		Records:  []mesh.DNSRecord{{Hostname: "controller.default.sind.sind", IP: "172.19.0.2"}, {Hostname: "worker-0.default.sind.sind", IP: "172.19.0.3"}},
		External: []string{"kind-control-plane"},
	})
	st.SetMonitoring("sind", &cluster.Monitoring{
		Realm: "sind",
		Services: []cluster.MonitoringService{
			{Name: "grafana", Container: "sind-grafana", Image: "grafana/grafana:13.2.3", IP: "172.18.255.253", URL: "http://grafana.sind.sind:3000", State: "running", Ready: true, Checked: true},
			{Name: "prometheus", Container: "sind-prometheus", Image: "prom/prometheus:v3.15.0", URL: "http://prometheus.sind.sind:9090", State: "running"},
			{Name: "loki", Container: "sind-loki", Image: "grafana/loki:3.7.8", URL: "http://loki.sind.sind:3100", State: "running", Checked: true},
			{Name: "alloy", Container: "sind-alloy", Image: "grafana/alloy:v1.20.1", URL: "http://172.18.0.9:12345", State: "running", Ready: true, Checked: true},
		},
		Clusters: []cluster.MonitoredCluster{{Name: "dev", Nodes: 4, SlurmVersion: "26.05.1"}},
	})
	st.SetClusters("ci", []cluster.Summary{{Name: "pr-145", SlurmVersion: "26.05.1", State: cluster.StateRunning, NodeCount: 3}})
	st.SetNodes("ci", moreNodes("ci", "pr-145", 2, -1))
	st.SetDoctor(&store.DoctorReport{Time: pgNow.Add(-3 * time.Minute), Checks: []store.DoctorCheck{
		{Name: "Docker Engine", Status: "ok", Detail: "Docker Engine 28.5.1"},
		{Name: "cgroup v2", Status: "ok", Detail: "cgroup2 with nsdelegate"},
		{Name: "inotify", Status: "warning", Detail: "max_user_instances 128 (clusters of 10 or more nodes need 1024; optional)", Remediation: "Raise the limit now:\n\n  sudo sysctl fs.inotify.max_user_instances=1024"},
		{Name: "DNS policy", Status: "failed", Detail: "not authorized (optional)", Remediation: "sudo resolvectl dns docker0 127.0.0.1"},
		{Name: "Odd", Status: "strange"},
	}})
}

// moreTestServer returns a logged-in server on the default registry, which
// holds the pages of the part, with the fixture in its store and the
// collect.doctor key when withDoctor is set.
func moreTestServer(t *testing.T, fixture, withDoctor bool) (*testServer, *int) {
	t.Helper()
	cfg, st := testConfig(t)
	cfg.Registry, cfg.FS = defaultRegistry.snapshot(), webFS()
	calls := new(int)
	if withDoctor {
		cfg.Env.Put(ColDoctorKey, func() { *calls++ })
	}
	moreEnv.Store(cfg.Env)
	t.Cleanup(func() { moreEnv.Store(nil) })
	ts := newTestServerFrom(t, cfg, st)
	ts.srv.now = func() time.Time { return pgNow }
	if fixture {
		moreFixture(t, st)
	}
	require.NotNil(t, ts.login())
	return ts, calls
}

// dockerState converts a state name of a container.
func dockerState(s string) docker.ContainerState { return docker.ContainerState(s) }
