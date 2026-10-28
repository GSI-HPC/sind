// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/stretchr/testify/require"
)

// pgNow is the clock of the page tests.
var pgNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// pgRegistry returns a registry with the shell, the Overview and the main
// pages.
func pgRegistry() *Registry {
	r := NewRegistry()
	r.AddFragment(railFragment())
	registerDock(r)
	r.AddRoute(pgRealmRoute(overviewPage, false, false))
	pgRegister(r)
	return r
}

// pgServer returns a server of the main pages with a logged-in session, on a
// store that holds nothing yet.
func pgServer(t *testing.T) *testServer {
	t.Helper()
	st := store.New()
	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	env := &Env{Store: st, Exec: &mock.Executor{}, Log: slog.New(slog.NewTextHandler(logs, nil)), Addr: fmt.Sprintf("127.0.0.1:%d", testPort)}
	ts := newTestServerFrom(t, serverConfig{
		Env: env, Token: "the-token", Bound: testBound(), Registry: pgRegistry(), Now: func() time.Time { return pgNow },
	}, st)
	st.SetRuns(nil)
	return ts.login()
}

// pgNodes builds the node summaries of a cluster: the roles with their
// counts, named as sind names them.
func pgNodes(realm, name string, roles map[config.Role]int, states map[string]cluster.State) []cluster.NodeSummary {
	var out []cluster.NodeSummary
	ip := 2
	add := func(role config.Role, short string) {
		st := cluster.StateRunning
		if s, ok := states[short]; ok {
			st = s
		}
		out = append(out, cluster.NodeSummary{
			Container: string(cluster.ContainerName(realm, name, short)), Cluster: name, Role: role,
			FQDN: fmt.Sprintf("%s.%s.%s.sind", short, name, realm), IP: fmt.Sprintf("172.20.0.%d", ip), State: st,
		})
		ip++
	}
	for _, role := range []config.Role{config.RoleController, config.RoleDB, config.RoleAPI, config.RoleSubmitter} {
		for i := 0; i < roles[role]; i++ {
			if role == config.RoleController && i == 1 {
				add(role, cluster.ControllerBackupShortName)
				continue
			}
			add(role, string(role))
		}
	}
	for i := 0; i < roles[config.RoleWorker]; i++ {
		add(config.RoleWorker, fmt.Sprintf("worker-%d", i))
	}
	return out
}

// pgFillStore puts a small installation in the store: the realm sind with a
// cluster dev (a stopped worker, a failed service, a pair of controllers)
// and a cluster hpc with monitoring, and the realm ci with a paused one.
func pgFillStore(st *store.Store) {
	st.SetDocker(store.DockerState{Checked: true})
	st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "ci", Clusters: 1}})
	st.SetClusters("sind", []cluster.Summary{
		{Name: "dev", SlurmVersion: "26.05.4", State: cluster.StateMixed, NodeCount: 9},
		{Name: "hpc", SlurmVersion: "25.11.8", State: cluster.StateRunning, NodeCount: 4},
	})
	st.SetClusters("ci", []cluster.Summary{{Name: "default", SlurmVersion: "26.05.4", State: cluster.StatePaused, NodeCount: 2}})
	dev := pgNodes("sind", "dev", map[config.Role]int{config.RoleController: 2, config.RoleDB: 1, config.RoleAPI: 1, config.RoleSubmitter: 1, config.RoleWorker: 4},
		map[string]cluster.State{"worker-3": cluster.StateStopped})
	hpc := pgNodes("sind", "hpc", map[config.Role]int{config.RoleController: 1, config.RoleWorker: 3}, nil)
	st.SetNodes("sind", append(dev, hpc...))
	st.SetNodes("ci", pgNodes("ci", "default", map[config.Role]int{config.RoleController: 1, config.RoleWorker: 1},
		map[string]cluster.State{"worker-0": cluster.StatePaused}))
	st.SetNetworks("sind", []cluster.NetworkSummary{
		{Name: "sind-mesh", Driver: "bridge", Subnet: "172.18.0.0/16", Gateway: "172.18.0.1"},
		{Name: "sind-dev-net", Driver: "bridge", Subnet: "172.20.0.0/16", Gateway: "172.20.0.1"},
		{Name: "sind-hpc-net", Driver: "bridge", Subnet: "172.21.0.0/16", Gateway: "172.21.0.1"},
	})
	st.SetVolumes("sind", []cluster.VolumeSummary{{Name: "sind-dev-config", Driver: "local"}, {Name: "sind-hpc-munge", Driver: "local"}})
	st.SetMonitoring("sind", &cluster.Monitoring{
		Realm:    "sind",
		Services: []cluster.MonitoringService{{Name: "grafana", URL: "http://grafana.sind.sind:3000"}},
		Clusters: []cluster.MonitoredCluster{{Name: "hpc", Nodes: 4}},
	})
	st.SetLock("sind", &state.LockHolder{Command: "sind create worker dev", Host: "zeta", PID: 4242, Since: pgNow.Add(-time.Minute)})
	st.SetDoctor(&store.DoctorReport{Time: pgNow, Checks: []store.DoctorCheck{
		{Name: "Docker daemon", Status: "ok"},
		{Name: "inotify", Status: "warning", Detail: "fs.inotify.max_user_watches is 8192"},
		{Name: "cgroupv2", Status: "failed", Detail: "no nsdelegate"},
	}})
	end := func(d time.Duration) *time.Time { t := pgNow.Add(-d); return &t }
	st.SetRuns([]store.Run{
		{ID: "r5", Title: "create worker dev", Start: pgNow.Add(-95 * time.Second)},
		{ID: "r4", Title: "create cluster hpc2", Parent: "span1", Start: pgNow.Add(-3 * time.Hour), End: end(2 * time.Hour), Status: store.StatusFailed, Err: "node controller: timed out"},
		{ID: "r3", Title: "delete cluster old", Parent: "span2", Start: pgNow.Add(-20 * time.Hour), End: end(19 * time.Hour), Status: store.StatusOK},
		{ID: "r2", Title: "power reboot worker-1.dev", Start: pgNow.Add(-30 * time.Hour), End: end(29 * time.Hour), Status: store.StatusCanceled},
		{ID: "r1", Title: "create cluster ancient", Start: pgNow.Add(-72 * time.Hour), End: end(71 * time.Hour), Status: store.StatusFailed, Err: "old"},
	})
}

// pgFillStatus puts the status of the cluster sind/dev in the store.
func pgFillStatus(st *store.Store) {
	svc := func(ok ...string) cluster.ServiceHealth {
		h := cluster.ServiceHealth{}
		for _, s := range ok {
			h[pgProbe(s)] = true
		}
		return h
	}
	down := func(h cluster.ServiceHealth, name string) cluster.ServiceHealth { h[pgProbe(name)] = false; return h }
	node := func(name string, role config.Role, h *cluster.NodeHealth) *cluster.NodeStatus {
		return &cluster.NodeStatus{Name: name + ".dev", Role: role, Managed: true, Health: h}
	}
	run := func(h cluster.ServiceHealth) *cluster.NodeHealth {
		return &cluster.NodeHealth{State: cluster.StateRunning, Services: h}
	}
	st.SetStatus("sind", "dev", &cluster.Status{
		Name: "dev", SlurmVersion: "26.05.4", State: cluster.StateMixed,
		Nodes: []*cluster.NodeStatus{
			node("controller", config.RoleController, &cluster.NodeHealth{State: cluster.StateRunning, Services: svc("munge", "sshd", "slurmctld"), HA: &cluster.HAStatus{Position: "primary", InControl: true}}),
			node("controller-backup", config.RoleController, &cluster.NodeHealth{State: cluster.StateRunning, Services: svc("munge", "sshd", "slurmctld"), HA: &cluster.HAStatus{Position: "backup"}}),
			node("db", config.RoleDB, run(svc("munge", "sshd", "mariadb", "slurmdbd"))),
			node("api", config.RoleAPI, run(svc("munge", "sshd", "slurmrestd"))),
			node("submitter", config.RoleSubmitter, run(svc("munge", "sshd", "sackd"))),
			node("worker-0", config.RoleWorker, run(svc("munge", "sshd", "slurmd"))),
			node("worker-1", config.RoleWorker, run(down(svc("munge", "sshd"), "slurmd"))),
			node("worker-2", config.RoleWorker, run(svc("munge", "sshd", "slurmd"))),
			node("worker-3", config.RoleWorker, &cluster.NodeHealth{State: cluster.StateStopped, Services: down(down(cluster.ServiceHealth{}, "munge"), "slurmd")}),
			nil,
		},
		Network: &cluster.NetworkHealth{
			Mesh: true, MeshName: "sind-mesh", MeshDriver: "bridge", MeshSubnet: "172.18.0.0/16", MeshGateway: "172.18.0.1", DNS: true, SSH: true,
			Cluster: true, ClusterName: "sind-dev-net", ClusterDriver: "bridge", ClusterSubnet: "172.20.0.0/16", ClusterGateway: "172.20.0.1",
		},
		Mounts: []cluster.MountPoint{
			{Path: "/etc/slurm", Source: "sind-dev-config", Type: config.StorageVolume, OK: true},
			{Path: "/data", Source: "/scratch/dev", Type: config.StorageHostPath, OK: true, Data: true},
			{Path: "/home", Source: "sind-dev-home", Type: config.StorageVolume},
		},
	})
}

// pgBody gets target in the session and returns its body, after it checked
// the status.
func pgBody(t *testing.T, ts *testServer, target string, status int) string {
	t.Helper()
	w := ts.get(target)
	require.Equal(t, status, w.Code, target)
	return w.Body.String()
}

func pgProbe(s string) probe.Service { return probe.Service(s) }

// pgFillSlurm puts what Slurm says about the cluster sind/dev in the store:
// a busy, a drained, a down and an idle worker, two partitions and the
// config files.
func pgFillSlurm(st *store.Store) {
	node := func(name, state, reason string, alloc, mem int, parts ...string) slurm.NodeInfo {
		return slurm.NodeInfo{Name: name, State: state, Reason: reason, CPUs: 2, AllocCPUs: alloc, MemoryMB: mem, Partitions: parts}
	}
	st.SetSlurm("sind", "dev", &store.Slurm{Info: &cluster.SlurmInfo{
		Nodes: []slurm.NodeInfo{
			node("worker-0", "mixed", "", 1, 1024, "all", "debug"),
			node("worker-1", "drained", "disk check", 0, 1024, "all"),
			node("worker-2", "idle", "", 0, 1024, "all"),
			node("worker-3", "down*", "Not responding", 0, 512, "all"),
		},
		Partitions: []slurm.PartitionInfo{
			{Name: "all", Default: true, State: "up", TimeLimit: "infinite", Nodes: 4, NodeList: "worker-[0-3]"},
			{Name: "debug", State: "down", TimeLimit: "30:00", Nodes: 1, NodeList: "worker-0"},
		},
		Files: []cluster.SlurmFile{
			{Path: "/etc/slurm/slurm.conf", Content: "ClusterName=dev\ninclude /etc/slurm/sind-nodes.conf\n"},
			{Path: "/etc/slurm/sind-nodes.conf", Content: "NodeName=worker-0 CPUs=2\n"},
		},
	}})
}
