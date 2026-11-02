// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sindFixture runs Sind on a fake daemon with short intervals.
type sindFixture struct {
	fake *fake
	st   *store.Store
	bus  *Bus
	sind *Sind
}

func newSindFixture(t *testing.T) *sindFixture {
	t.Helper()
	f := newFake(t)
	st := store.New()
	bus := &Bus{}
	return &sindFixture{fake: f, st: st, bus: bus, sind: &Sind{
		Store: st, Client: f.client(), Bus: bus,
		Poll: time.Hour, Retry: time.Hour,
	}}
}

func (f *sindFixture) addCluster(realm, cluster string) {
	f.fake.set(func() {
		f.fake.nodes[realm] = append(f.fake.nodes[realm],
			node(realm, cluster, "controller", "controller"), node(realm, cluster, "worker-0", "worker"))
		f.fake.clusters[realm+"/"+cluster] = true
	})
}

func TestSindReadsEverythingAtStart(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	f.addCluster("ci", "a")
	f.fake.set(func() {
		f.fake.meshes["sind"] = true
		f.fake.external["sind"] = []string{"kind-control-plane"}
		f.fake.outside = []testutil.PsEntry{{ID: "app", Names: "my-app", State: "running", Status: "Up 2 hours", Image: "python:3.12-slim", Networks: "bridge"}}
	})
	start(t, f.sind.Run)

	waitFor(t, "everything read", func() bool {
		sn := f.st.Snapshot()
		_, ok := sn.Meshes["sind"]
		return ok && len(sn.Nodes["sind"]) == 2 && len(sn.Nodes["ci"]) == 2 &&
			len(sn.Clusters["sind"]) == 1 && len(sn.Networks["sind"]) == 1
	})
	sn := f.st.Snapshot()
	assert.Empty(t, sn.Docker.Err)
	require.Len(t, sn.Realms, 2)
	assert.Equal(t, "ci", sn.Realms[0].Name)
	require.Len(t, sn.Clusters["sind"], 1)
	assert.Equal(t, "dev", sn.Clusters["sind"][0].Name)
	assert.Equal(t, 2, sn.Clusters["sind"][0].NodeCount)
	assert.Len(t, sn.Nodes["sind"], 2)
	assert.Len(t, sn.Networks["sind"], 1)
	m := sn.Meshes["sind"]
	require.NotNil(t, m.Info)
	assert.Equal(t, "10.0.0.2", m.Info.DNSIP)
	assert.Equal(t, []string{"kind-control-plane"}, m.External)
	assert.Equal(t, []store.Outside{{Name: "my-app", Image: "python:3.12-slim", State: "running", Status: "Up 2 hours", Networks: []string{"bridge"}}}, m.Connectable,
		"the nodes are sind's")
	require.Len(t, m.Records, 2)
	assert.Equal(t, "controller.dev.sind", m.Records[0].Hostname)
	assert.Empty(t, m.Err)
	_, hasMesh := sn.Meshes["ci"]
	assert.False(t, hasMesh, "a realm without mesh has none stored")
	assert.Len(t, sn.Nodes["ci"], 2)
}

func TestSindReadsTheSizeOfTheHostOnce(t *testing.T) {
	f := newSindFixture(t)
	start(t, f.sind.Run)
	waitFor(t, "first pass", func() bool { return f.st.Snapshot().Docker.Checked })
	assert.Zero(t, f.st.Snapshot().Docker.MemTotal, "docker info failed: no size yet")

	f.fake.set(func() { f.fake.info = `{"ServerVersion":"29.1.0","MemTotal":17179869184,"NCPU":8}` })
	f.bus.Notify(Event{})
	waitFor(t, "the size", func() bool { return f.st.Snapshot().Docker.MemTotal == 16<<30 })
	assert.Equal(t, 8, f.st.Snapshot().Docker.NCPU)
	infos := f.fake.count("info")

	f.bus.Notify(Event{})
	f.fake.settle()
	assert.Equal(t, infos, f.fake.count("info"), "read once")
	assert.Equal(t, int64(16<<30), f.st.Snapshot().Docker.MemTotal)
}

func TestSindReadsOnlyTheRealmOfAnEvent(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	f.addCluster("ci", "a")
	start(t, f.sind.Run)
	waitFor(t, "first pass", func() bool { return len(f.st.Snapshot().Nodes["ci"]) == 2 })
	f.fake.settle()
	psBefore := f.fake.count("ps")

	f.fake.set(func() { f.fake.nodes["ci"] = append(f.fake.nodes["ci"], node("ci", "a", "worker-1", "worker")) })
	f.bus.Notify(Event{Kind: KindChange, Realm: "ci", Cluster: "a"})

	waitFor(t, "ci read again", func() bool { return len(f.st.Snapshot().Nodes["ci"]) == 3 })
	f.fake.settle()
	// GetRealms, then the clusters and the nodes of ci alone: those of sind
	// are not read.
	assert.Equal(t, 3, f.fake.count("ps")-psBefore)
}

func TestSindIgnoresLockEvents(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	start(t, f.sind.Run)
	waitFor(t, "first pass", func() bool { return len(f.st.Snapshot().Nodes["sind"]) == 2 })
	f.fake.settle()
	before := f.fake.count("ps")

	f.bus.Notify(Event{Kind: KindLock, Realm: "sind"})
	never(t, "a read for a lock event", 100*time.Millisecond, func() bool { return f.fake.count("ps") != before })
}

func TestSindResyncReadsEveryRealm(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	f.addCluster("ci", "a")
	start(t, f.sind.Run)
	waitFor(t, "first pass", func() bool { return len(f.st.Snapshot().Nodes["ci"]) == 2 })

	f.addCluster("sind", "prod")
	f.addCluster("ci", "b")
	f.bus.Notify(Event{})
	waitFor(t, "both realms read", func() bool {
		sn := f.st.Snapshot()
		return len(sn.Clusters["sind"]) == 2 && len(sn.Clusters["ci"]) == 2
	})
}

func TestSindPollsWithoutEvents(t *testing.T) {
	f := newSindFixture(t)
	f.sind.Poll = 30 * time.Millisecond
	f.addCluster("sind", "dev")
	start(t, f.sind.Run)
	waitFor(t, "first pass", func() bool { return len(f.st.Snapshot().Clusters["sind"]) == 1 })

	f.addCluster("sind", "prod")
	waitFor(t, "found by the poll", func() bool { return len(f.st.Snapshot().Clusters["sind"]) == 2 })
}

func TestSindForgetsAGoneRealm(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	f.addCluster("ci", "a")
	f.fake.set(func() { f.fake.meshes["ci"] = true })
	start(t, f.sind.Run)
	waitFor(t, "ci read", func() bool { _, ok := f.st.Snapshot().Meshes["ci"]; return ok })

	f.fake.set(func() { delete(f.fake.nodes, "ci"); delete(f.fake.clusters, "ci/a"); delete(f.fake.meshes, "ci") })
	f.bus.Notify(Event{Kind: KindChange, Realm: "ci", Cluster: "a"})

	waitFor(t, "ci gone", func() bool {
		sn := f.st.Snapshot()
		_, hasMesh := sn.Meshes["ci"]
		return len(sn.Realms) == 1 && len(sn.Nodes["ci"]) == 0 && len(sn.Clusters["ci"]) == 0 && !hasMesh
	})
	assert.Len(t, f.st.Snapshot().Nodes["sind"], 2)

	// A gone realm that no event names goes at the next full read too.
	f.addCluster("ci", "a")
	f.bus.Notify(Event{})
	waitFor(t, "ci back", func() bool { return len(f.st.Snapshot().Nodes["ci"]) == 2 })
	f.fake.set(func() { delete(f.fake.nodes, "ci") })
	f.bus.Notify(Event{})
	waitFor(t, "ci gone again", func() bool { return len(f.st.Snapshot().Nodes["ci"]) == 0 })
}

func TestSindDockerUnreachableAndRecovers(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	f.fake.set(func() { f.fake.down = true })
	f.sind.Retry = 40 * time.Millisecond
	start(t, f.sind.Run)

	waitFor(t, "docker error", func() bool { return f.st.Snapshot().Docker.Err != "" })
	d := f.st.Snapshot().Docker
	assert.True(t, d.Checked)
	assert.Equal(t, "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", d.Err)
	assert.Empty(t, f.st.Snapshot().Realms)

	// Retries come slowly: the retry interval, not a busy loop.
	n := f.fake.count("ps")
	time.Sleep(25 * time.Millisecond)
	assert.LessOrEqual(t, f.fake.count("ps")-n, 1)

	f.fake.set(func() { f.fake.down = false })
	waitFor(t, "recovered", func() bool { return f.st.Snapshot().Docker.Err == "" && len(f.st.Snapshot().Clusters["sind"]) == 1 })
}

func TestSindKeepsWhatItCannotRead(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	logger, out := debugLog()
	f.sind.Log = logger
	start(t, f.sind.Run)
	waitFor(t, "first pass", func() bool { return len(f.st.Snapshot().Networks["sind"]) == 1 })

	f.fake.set(func() { f.fake.failing = "network ls" })
	f.addCluster("sind", "prod")
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind"})
	waitFor(t, "clusters read", func() bool { return len(f.st.Snapshot().Clusters["sind"]) == 2 })
	f.fake.settle()
	assert.Len(t, f.st.Snapshot().Networks["sind"], 1, "the networks stay as they were")
	assert.Contains(t, out.String(), "reading networks")
}

func TestSindMeshErrors(t *testing.T) {
	t.Run("not readable", func(t *testing.T) {
		f := newSindFixture(t)
		f.addCluster("sind", "dev")
		f.fake.set(func() { f.fake.failing = "inspect sind-dns" })
		start(t, f.sind.Run)
		waitFor(t, "mesh error", func() bool { m, ok := f.st.Snapshot().Meshes["sind"]; return ok && m.Err != "" })
		m := f.st.Snapshot().Meshes["sind"]
		assert.Nil(t, m.Info)
		assert.Contains(t, m.Err, "boom")
	})
	t.Run("partly readable", func(t *testing.T) {
		f := newSindFixture(t)
		f.addCluster("sind", "dev")
		f.fake.set(func() { f.fake.meshes["sind"] = true; f.fake.failing = "cp " })
		start(t, f.sind.Run)
		waitFor(t, "mesh", func() bool { m, ok := f.st.Snapshot().Meshes["sind"]; return ok && m.Err != "" })
		m := f.st.Snapshot().Meshes["sind"]
		require.NotNil(t, m.Info)
		assert.Empty(t, m.Records)
		assert.Contains(t, m.Err, "boom")
	})
	t.Run("external containers", func(t *testing.T) {
		f := newSindFixture(t)
		f.addCluster("sind", "dev")
		f.fake.set(func() { f.fake.meshes["sind"] = true; f.fake.failing = "network=sind-mesh" })
		start(t, f.sind.Run)
		waitFor(t, "mesh", func() bool { m, ok := f.st.Snapshot().Meshes["sind"]; return ok && m.Err != "" })
		m := f.st.Snapshot().Meshes["sind"]
		assert.Equal(t, "boom", m.Err)
		assert.Len(t, m.Records, 2)
	})
	t.Run("containers that can connect", func(t *testing.T) {
		f := newSindFixture(t)
		f.addCluster("sind", "dev")
		f.fake.set(func() { f.fake.meshes["sind"] = true; f.fake.failingAll = true })
		m, ok := readMesh(t.Context(), f.fake.client(), "sind")
		assert.True(t, ok)
		assert.Equal(t, "boom", m.Err)
		assert.Nil(t, m.Connectable)
		assert.Len(t, m.Records, 2)
	})
}

func TestSindEndsWithContext(t *testing.T) {
	f := newSindFixture(t)
	stop := start(t, f.sind.Run)
	waitFor(t, "first pass", func() bool { return f.st.Snapshot().Docker.Checked })
	stop()
	// Nothing is set once it has returned.
	before := f.st.Snapshot()
	f.bus.Notify(Event{})
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, before.Docker, f.st.Snapshot().Docker)
}

func TestSindCancelledReadsSetNothing(t *testing.T) {
	f := newSindFixture(t)
	f.addCluster("sind", "dev")
	f.fake.set(func() { f.fake.down = true })
	ctx := t.Context()
	// A context that ended is no reason to say Docker is down.
	ctxDone, cancel := cancelled(ctx)
	defer cancel()
	f.sind.known = map[string]bool{}
	assert.True(t, f.sind.pass(ctxDone, true, nil))
	assert.False(t, f.st.Snapshot().Docker.Checked)

	// Nor to store a mesh error, or log one.
	f.fake.set(func() { f.fake.down = false; f.fake.failing = "inspect" })
	_, ok := readMesh(ctxDone, f.fake.client(), "sind")
	assert.False(t, ok)
	f.sind.known = map[string]bool{}
	f.sind.readRealm(ctxDone, "sind")
}
