// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/probe"
)

func seededStore(t *testing.T) (*World, *store.Store) {
	t.Helper()
	w, st := Seeded(), store.New()
	w.Publish(st)
	return w, st
}

// slurmdUp reports whether the slurmd of worker-4 of dev runs, as the
// store has it.
func slurmdUp(st *store.Store) bool {
	for _, n := range st.Snapshot().Status("sind", "dev").Nodes {
		if n.Name == "worker-4.dev" {
			return n.Health.Services[probe.ServiceSlurmd]
		}
	}
	return false
}

func TestSeeded(t *testing.T) {
	_, st := seededStore(t)
	sn := st.Snapshot()

	assert.Equal(t, store.DockerState{Checked: true, MemTotal: HostMemory, NCPU: 16}, sn.Docker)
	require.Len(t, sn.Realms, 2)
	assert.Equal(t, "ci", sn.Realms[0].Name)
	assert.Equal(t, 1, sn.Realms[0].Clusters)
	assert.Equal(t, 3, sn.Realms[1].Clusters)

	byName := map[string]cluster.Summary{}
	for _, c := range sn.Clusters["sind"] {
		byName[c.Name] = c
	}
	assert.Equal(t, cluster.StateRunning, byName["default"].State)
	assert.Equal(t, cluster.StateRunning, byName["dev"].State)
	assert.Equal(t, cluster.StateMixed, byName["legacy"].State, "a stopped worker")
	dev := byName["dev"]
	assert.Equal(t, 11, dev.NodeCount)
	assert.Equal(t, 2, dev.Controllers)
	assert.Equal(t, 1, dev.DBs)
	assert.Equal(t, 1, dev.APIs)
	assert.Equal(t, 1, dev.Submitters)
	assert.Equal(t, 6, dev.Workers)
	assert.Equal(t, "25.11.8", byName["legacy"].SlurmVersion)

	require.NotNil(t, sn.Monitorings["sind"])
	// dev's six workers are Slurm's nodes; worker-4's slurmd is down, and
	// worker-5 is drained.
	assert.Equal(t, &cluster.SlurmCounts{Nodes: 6, NodesDown: 1, NodesDrain: 1, JobsRunning: 4, JobsPending: demoPending},
		sn.Monitorings["sind"].Clusters[0].Slurm)
	assert.Nil(t, sn.Monitorings["ci"], "monitoring in one realm only")
	m, ok := sn.Meshes["sind"]
	require.True(t, ok)
	assert.Equal(t, []string{"kind-control-plane"}, m.External)
	assert.Equal(t, "sind-mesh", m.Info.Network)
	assert.Len(t, m.Records, 11+2+3)
	ci := sn.Meshes["ci"]
	assert.Equal(t, "ci-mesh", ci.Info.Network)

	status := sn.Status("sind", "dev")
	require.NotNil(t, status)
	assert.True(t, *status.Network.Grafana)
	var down []string
	for _, n := range status.Nodes {
		for s, ok := range n.Health.Services {
			if !ok {
				down = append(down, n.Name+" "+string(s))
			}
		}
		if n.Name == "controller-backup.dev" {
			assert.Equal(t, "backup", n.Health.HA.Position)
		}
		if n.Name == "controller.dev" {
			assert.True(t, n.Health.HA.InControl)
		}
	}
	assert.Equal(t, []string{"worker-4.dev slurmd"}, down)
	assert.Len(t, status.Mounts, 3)
	assert.Equal(t, config.StorageVolume, status.Mounts[2].Type)
	assert.Equal(t, config.StorageHostPath, sn.Status("sind", "default").Mounts[2].Type)
	assert.Nil(t, sn.Status("ci", "pr-145").Network.Grafana)

	assert.Len(t, sn.Networks["sind"], 4, "the mesh and a network for each cluster")
	assert.NotEmpty(t, sn.Volumes["sind"])
	assert.Len(t, sn.Nodes["sind"], 11+2+3)
}

func TestWorld_Lifecycle(t *testing.T) {
	w, st := seededStore(t)
	n := func(name string) node { return node{name: name, cluster: "new", role: "worker"} }
	apply := func(op, name string) { w.apply("sind", op, n(name)); w.Publish(st) }
	state := func() map[string]cluster.State {
		out := map[string]cluster.State{}
		for _, ns := range st.Snapshot().Nodes["sind"] {
			if ns.Cluster == "new" {
				out[ns.FQDN] = ns.State
			}
		}
		return out
	}

	apply(opCreate, "w0")
	assert.Equal(t, map[string]cluster.State{"w0.new.sind.sind": cluster.StateStopped}, state())
	apply(opCreate, "w0")
	assert.Len(t, state(), 1, "a node that exists is not made twice")

	apply(opStart, "w0")
	assert.Equal(t, cluster.StateRunning, state()["w0.new.sind.sind"])
	assert.False(t, st.Snapshot().Status("sind", "new").Nodes[0].Health.Services[probe.ServiceSlurmd], "no services yet")
	apply(opReady, "w0")
	assert.True(t, st.Snapshot().Status("sind", "new").Nodes[0].Health.Services[probe.ServiceSlurmd])
	apply(opPause, "w0")
	assert.Equal(t, cluster.StatePaused, state()["w0.new.sind.sind"])
	apply(opUnpause, "w0")
	assert.Equal(t, cluster.StateRunning, state()["w0.new.sind.sind"])
	apply(opStop, "w0")
	assert.Equal(t, cluster.StateStopped, state()["w0.new.sind.sind"])
	assert.Equal(t, cluster.StateStopped, st.Snapshot().Clusters["sind"][3].State)

	apply(opStart, "nobody") // a node that is not there
	w.apply("sind", opStart, node{name: "w0", cluster: "nowhere"})
	apply(opRemove, "w0")
	assert.Empty(t, state())
	_, ok := st.Snapshot().Cluster("sind", "new")
	assert.False(t, ok, "a cluster without containers does not exist")
	assert.Nil(t, st.Snapshot().Status("sind", "new"))
}

func TestWorld_RealmGoes(t *testing.T) {
	w, st := seededStore(t)
	for _, c := range w.clustersOf("ci") {
		for _, n := range w.nodesOf("ci", c) {
			w.apply("ci", opRemove, n)
		}
	}
	w.Publish(st)
	sn := st.Snapshot()
	require.Len(t, sn.Realms, 1)
	assert.Empty(t, sn.Clusters["ci"])
	assert.Empty(t, sn.Nodes["ci"])
	_, ok := sn.Meshes["ci"]
	assert.False(t, ok)
	assert.Nil(t, sn.Status("ci", "pr-145"))
}

func TestWorld_Monitoring(t *testing.T) {
	w, st := seededStore(t)
	w.apply("ci", opMonitor, node{})
	w.Publish(st)
	m := st.Snapshot().Monitorings["ci"]
	require.NotNil(t, m)
	assert.Len(t, m.Services, 4)
	assert.Equal(t, "http://grafana.ci.sind:3000", m.Services[0].URL)
	assert.Equal(t, "http://172.22.0.9:12345", m.Services[3].URL, "alloy has no DNS name")
}

func TestWorld_Connect(t *testing.T) {
	w, st := seededStore(t)
	w.apply("sind", opConnect, node{name: "minio"})
	w.apply("sind", opConnect, node{name: "minio"})
	w.Publish(st)
	m := st.Snapshot().Meshes["sind"]
	assert.Equal(t, []string{"kind-control-plane", "minio"}, m.External)
	w.apply("sind", opDisconn, node{name: "kind-control-plane"})
	w.Publish(st)
	m = st.Snapshot().Meshes["sind"]
	assert.Equal(t, []string{"minio"}, m.External)

	// A realm that only has a connected container is no realm.
	w.apply("other", opConnect, node{name: "x"})
	w.Publish(st)
	assert.Len(t, st.Snapshot().Realms, 2)
}

func TestWorld_Drift(t *testing.T) {
	w, st := seededStore(t)
	require.False(t, slurmdUp(st))
	assert.False(t, w.Drift(), "a failure stays until a fix")

	// Once its services came up, as after a reboot, the node stays calm
	// for a while, then slurmd fails again.
	w.apply("sind", opReady, node{name: "worker-4", cluster: "dev"})
	w.Publish(st)
	require.True(t, slurmdUp(st))
	for range readyCalm {
		assert.False(t, w.Drift())
	}
	assert.True(t, w.Drift())
	w.Publish(st)
	assert.False(t, slurmdUp(st))
	assert.False(t, w.Drift())

	w.apply("sind", opStop, node{name: "worker-4", cluster: "dev"})
	assert.False(t, w.Drift(), "a node that is stopped has nothing to fail")
	w.apply("sind", opRemove, node{name: "worker-4", cluster: "dev"})
	assert.False(t, w.Drift())
	for _, n := range w.nodesOf("sind", "dev") {
		w.apply("sind", opRemove, n)
	}
	assert.False(t, w.Drift(), "no dev")
}

func TestWorld_Queries(t *testing.T) {
	w := Seeded()
	assert.Equal(t, []string{"default", "dev", "legacy"}, w.clustersOf("sind"))
	assert.Empty(t, w.clustersOf("nowhere"))
	assert.Nil(t, w.nodesOf("sind", "nowhere"))
	assert.Len(t, w.nodesOf("sind", "dev"), 11)
	assert.Nil(t, w.lookup("nowhere", "dev"))
	assert.Nil(t, w.lookup("sind", "nowhere"))
	assert.Nil(t, w.clusterOf("sind", "dev").find("nobody"))

	w.configure("sind", "dev", func(c *clusterState) { c.users = nil })
	assert.Nil(t, w.lookup("sind", "dev").users)
	assert.NotPanics(t, func() { w.configure("sind", "nowhere", func(*clusterState) { panic("not called") }) })
}

func TestServices(t *testing.T) {
	want := map[config.Role]int{config.RoleController: 3, config.RoleDB: 4, config.RoleAPI: 3, config.RoleSubmitter: 3, config.RoleWorker: 3}
	for role, n := range want {
		got := services(role)
		assert.Contains(t, got, probe.ServiceMunge, role)
		assert.Len(t, got, n, role)
	}
}

func TestWorld_Faults(t *testing.T) {
	w := Seeded()
	w.now = func() time.Time { return t0 }
	st := store.New()
	w.Publish(st)
	sn := st.Snapshot()

	// What was wrong before the demo started gets a time of its own.
	f, ok := sn.Fault("sind", "dev", "worker-4")
	require.True(t, ok)
	require.Len(t, f.Units, 1)
	u := f.Units[0]
	assert.Equal(t, "slurmd", u.Name)
	assert.Equal(t, "failed", u.State)
	assert.Equal(t, t0.Add(-seededFailed), u.Since)
	assert.Equal(t, "systemd[1]: slurmd.service: Failed with result 'exit-code'.", u.Journal[len(u.Journal)-1].Text)
	f, ok = sn.Fault("sind", "legacy", "worker-1")
	require.True(t, ok)
	assert.Equal(t, &store.Exit{Code: 137, OOMKilled: true, At: t0.Add(-seededStopped)}, f.Exit)
	_, ok = sn.Fault("sind", "dev", "worker-0")
	assert.False(t, ok)

	at, down := w.FailedSince("sind-dev-worker-4", "slurmd")
	assert.True(t, down)
	assert.Equal(t, t0.Add(-seededFailed), at)
	_, down = w.FailedSince("sind-dev-worker-4", "munge")
	assert.False(t, down)
	_, down = w.FailedSince("sind-dev-nobody", "slurmd")
	assert.False(t, down)

	// A restart brings the service up; one of a node that does not run,
	// or of a service the node has not, does nothing.
	w.apply("sind", opRestart, node{name: "worker-4", cluster: "dev", unit: "slurmctld"})
	_, down = w.FailedSince("sind-dev-worker-4", "slurmd")
	assert.True(t, down)
	w.apply("sind", opRestart, node{name: "worker-4", cluster: "dev", unit: "slurmd"})
	_, down = w.FailedSince("sind-dev-worker-4", "slurmd")
	assert.False(t, down)
	w.apply("sind", opRestart, node{name: "worker-1", cluster: "legacy", unit: "slurmd"})
	w.Publish(st)
	_, ok = st.Snapshot().Fault("sind", "dev", "worker-4")
	assert.False(t, ok, "the restart fixed it")
	assert.True(t, slurmdUp(st))

	// A stop is timed by the world's clock, and a fault of a cluster that
	// went goes too.
	w.apply("sind", opStop, node{name: "worker-0", cluster: "dev"})
	w.Publish(st)
	f, _ = st.Snapshot().Fault("sind", "dev", "worker-0")
	assert.Equal(t, &store.Exit{At: t0}, f.Exit)
	for _, n := range w.nodesOf("sind", "legacy") {
		w.apply("sind", opRemove, n)
	}
	w.Publish(st)
	_, ok = st.Snapshot().Fault("sind", "legacy", "worker-1")
	assert.False(t, ok)
}

func TestWorld_DriftTimesTheFailure(t *testing.T) {
	w, _ := seededStore(t)
	w.now = func() time.Time { return t0 }
	w.apply("sind", opReady, node{name: "worker-4", cluster: "dev"})
	_, down := w.FailedSince("sind-dev-worker-4", "slurmd")
	assert.False(t, down)
	for range readyCalm {
		w.Drift()
	}
	require.True(t, w.Drift())
	at, down := w.FailedSince("sind-dev-worker-4", "slurmd")
	assert.True(t, down)
	assert.Equal(t, t0, at)
}

func TestWorld_ClockDefault(t *testing.T) {
	assert.WithinDuration(t, time.Now(), NewWorld().clock(), time.Minute)
}
