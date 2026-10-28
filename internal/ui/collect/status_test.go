// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusKeyOfATopic(t *testing.T) {
	for topic, want := range map[store.Topic]store.StatusKey{
		"status/sind/dev":              {Realm: "sind", Cluster: "dev"},
		store.StatusTopic("ci-1", "a"): {Realm: "ci-1", Cluster: "a"},
	} {
		k, ok := statusKey(topic)
		assert.True(t, ok, topic)
		assert.Equal(t, want, k)
	}
	for _, topic := range []store.Topic{"status/sind", "status/", "status//dev", "status/sind/", "status/a/b/c", "clusters/sind", "runs", "statuses/a/b"} {
		_, ok := statusKey(topic)
		assert.False(t, ok, topic)
	}
}

var (
	devKey  = store.StatusKey{Realm: "sind", Cluster: "dev"}
	prodKey = store.StatusKey{Realm: "sind", Cluster: "prod"}
	ciKey   = store.StatusKey{Realm: "ci", Cluster: "a"}
)

func TestWatchedCountsPerCluster(t *testing.T) {
	w := NewWatched()
	assert.Empty(t, w.Keys())

	r1 := w.Watch([]store.Topic{store.TopicRealms, devKey.Topic(), devKey.Topic(), prodKey.Topic(), "status/bad"})
	r2 := w.Watch([]store.Topic{devKey.Topic()})
	assert.Equal(t, []store.StatusKey{devKey, prodKey}, w.Keys())
	assert.Equal(t, []store.StatusKey{devKey, prodKey}, w.takeFresh())
	assert.Empty(t, w.takeFresh(), "fresh once")

	r1()
	r1() // a second release counts nothing
	assert.Equal(t, []store.StatusKey{devKey}, w.Keys())
	r2()
	assert.Empty(t, w.Keys())

	// A cluster that went away before it was read is not fresh any more.
	r3 := w.Watch([]store.Topic{ciKey.Topic()})
	r3()
	assert.Empty(t, w.takeFresh())

	// Streams of no cluster page count nothing and wake nobody.
	for len(w.wake) > 0 {
		<-w.wake
	}
	w.Watch([]store.Topic{store.TopicTasks})()
	assert.Empty(t, w.wake)
}

// statusFixture runs Status on a fake daemon.
type statusFixture struct {
	fake    *fake
	st      *store.Store
	bus     *Bus
	watched *Watched
	status  *Status
	// perRead is how many container lists one read of a status makes.
	perRead int
}

func newStatusFixture(t *testing.T) *statusFixture {
	t.Helper()
	f := newFake(t)
	st := store.New()
	bus := &Bus{}
	w := NewWatched()
	f.set(func() {
		f.clusters["sind/dev"] = true
		f.clusters["sind/prod"] = true
		f.clusters["ci/a"] = true
	})
	sf := &statusFixture{fake: f, st: st, bus: bus, watched: w, status: &Status{
		Store: st, Client: f.client(), Bus: bus, Watched: w,
		Every: time.Hour, After: 30 * time.Millisecond,
	}}
	sf.status.read(t.Context(), devKey) // measures one read; stores nothing, as no page watches it
	sf.perRead = f.count("ps")
	require.Positive(t, sf.perRead)
	return sf
}

// reads counts the status reads that Run made.
func (f *statusFixture) reads() int { return f.fake.count("ps")/f.perRead - 1 }

func (f *statusFixture) has(k store.StatusKey) bool {
	return f.st.Snapshot().Status(k.Realm, k.Cluster) != nil
}

func TestStatusReadsWhenWatchedAndDropsWhenReleased(t *testing.T) {
	f := newStatusFixture(t)
	start(t, f.status.Run)

	release := f.watched.Watch([]store.Topic{devKey.Topic()})
	waitFor(t, "status", func() bool { return f.has(devKey) })
	assert.Equal(t, "dev", f.st.Snapshot().Status("sind", "dev").Name)
	assert.False(t, f.has(prodKey))

	second := f.watched.Watch([]store.Topic{devKey.Topic(), prodKey.Topic()})
	waitFor(t, "second page", func() bool { return f.has(prodKey) })
	release()
	f.fake.settle()
	assert.True(t, f.has(devKey), "still watched by the second page")

	second()
	waitFor(t, "dropped", func() bool { return !f.has(devKey) && !f.has(prodKey) })
	n := f.reads()
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	never(t, "a read of an unwatched cluster", 150*time.Millisecond, func() bool { return f.reads() != n })
}

func TestStatusPollsEveryInterval(t *testing.T) {
	f := newStatusFixture(t)
	f.status.Every = 30 * time.Millisecond
	start(t, f.status.Run)
	f.watched.Watch([]store.Topic{devKey.Topic()})
	waitFor(t, "status", func() bool { return f.has(devKey) })
	n := f.reads()
	waitFor(t, "read again", func() bool { return f.reads() >= n+2 })
}

func TestStatusReadsAfterAnEventOfItsCluster(t *testing.T) {
	f := newStatusFixture(t)
	f.status.After = 80 * time.Millisecond
	start(t, f.status.Run)
	f.watched.Watch([]store.Topic{devKey.Topic(), prodKey.Topic(), ciKey.Topic()})
	waitFor(t, "statuses", func() bool { return f.has(devKey) && f.has(prodKey) && f.has(ciKey) })
	f.fake.settle()

	// Many events of one cluster make one read, after the delay.
	n := f.reads()
	for range 5 {
		f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	}
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, n, f.reads(), "not at once")
	waitFor(t, "read", func() bool { return f.reads() > n })
	f.fake.settle()
	assert.Equal(t, n+1, f.reads(), "one read, and none of prod or ci")

	// A network that no cluster owns, such as the mesh: every cluster of the
	// realm, and no other realm's.
	n = f.reads()
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind"})
	waitFor(t, "both", func() bool { return f.reads() >= n+2 })
	f.fake.settle()
	assert.Equal(t, n+2, f.reads())

	// A lock is not a change of a cluster.
	n = f.reads()
	f.bus.Notify(Event{Kind: KindLock, Realm: "sind"})
	never(t, "a read for a lock", 200*time.Millisecond, func() bool { return f.reads() != n })

	// A resync reads every watched cluster.
	f.bus.Notify(Event{})
	waitFor(t, "all three", func() bool { return f.reads() >= n+3 })
}

func TestStatusRefreshReadsAtOnce(t *testing.T) {
	f := newStatusFixture(t)
	f.status.After = time.Hour
	start(t, f.status.Run)
	f.watched.Watch([]store.Topic{devKey.Topic(), ciKey.Topic()})
	waitFor(t, "statuses", func() bool { return f.has(devKey) && f.has(ciKey) })
	f.fake.settle()

	n := f.reads()
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"}) // pending for an hour
	f.bus.Notify(Event{Kind: KindRefresh, Realm: "sind"})
	waitFor(t, "read", func() bool { return f.reads() > n })
	f.fake.settle()
	assert.Equal(t, n+1, f.reads(), "dev alone; the pending read of dev is done")
}

func TestStatusDropsAGoneClusterAndKeepsOnErrors(t *testing.T) {
	f := newStatusFixture(t)
	start(t, f.status.Run)
	f.watched.Watch([]store.Topic{devKey.Topic()})
	waitFor(t, "status", func() bool { return f.has(devKey) })

	// A read that fails leaves the status.
	f.fake.set(func() { f.fake.failing = "network inspect" })
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	n := f.fake.count("network inspect")
	waitFor(t, "failing read", func() bool { return f.fake.count("network inspect") > n })
	f.fake.settle()
	assert.True(t, f.has(devKey))

	// The cluster is deleted.
	f.fake.set(func() { f.fake.failing = ""; delete(f.fake.clusters, "sind/dev") })
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	waitFor(t, "gone", func() bool { return !f.has(devKey) })
}

func TestStatusIsNotStoredForAClusterThatWasClosedWhileRead(t *testing.T) {
	f := newStatusFixture(t)
	f.status.read(t.Context(), devKey)
	assert.False(t, f.has(devKey), "nobody watches it")

	f.watched.Watch([]store.Topic{devKey.Topic()})
	f.status.read(t.Context(), devKey)
	assert.True(t, f.has(devKey))
}

func TestStatusReadsWhatSlurmSays(t *testing.T) {
	f := newStatusFixture(t)
	f.watched.Watch([]store.Topic{devKey.Topic()})
	f.status.read(t.Context(), devKey)
	sl := f.st.Snapshot().Slurm("sind", "dev")
	require.NotNil(t, sl)
	assert.Empty(t, sl.Err)
	assert.Equal(t, "idle", sl.Info.Nodes[0].State)
	assert.Equal(t, "all", sl.Info.Partitions[0].Name)
	assert.Equal(t, "# /etc/slurm/slurm.conf\n", sl.Info.Files[0].Content)

	// sinfo fails: the status is kept, and what Slurm says holds the error
	// and the files.
	f.fake.set(func() { f.fake.failing = "sinfo" })
	f.status.read(t.Context(), devKey)
	sl = f.st.Snapshot().Slurm("sind", "dev")
	assert.Empty(t, sl.Info.Nodes)
	assert.NotEmpty(t, sl.Info.Files)
	assert.Equal(t, "boom", sl.Err)
	assert.True(t, f.has(devKey))

	// A read that was cancelled stores nothing; one of a cluster whose page
	// closed neither.
	f.st.SetSlurm("sind", "dev", nil)
	ctx, cancel := cancelled(t.Context())
	defer cancel()
	f.status.readSlurm(ctx, devKey)
	f.status.readSlurm(t.Context(), prodKey)
	assert.Empty(t, f.st.Snapshot().Slurms)

	// The cluster is gone: both go.
	f.fake.set(func() { f.fake.failing = ""; delete(f.fake.clusters, "sind/dev") })
	f.status.read(t.Context(), devKey)
	assert.Nil(t, f.st.Snapshot().Slurm("sind", "dev"))
}

func TestStatusLogsAFailedReadUnlessCancelled(t *testing.T) {
	f := newStatusFixture(t)
	log, out := debugLog()
	f.status.Log = log
	f.fake.set(func() { f.fake.failing = "ps" })
	f.watched.Watch([]store.Topic{devKey.Topic()})

	f.status.read(t.Context(), devKey)
	assert.Contains(t, out.String(), "reading the status")

	out.Reset()
	ctx, cancel := cancelled(t.Context())
	defer cancel()
	f.status.read(ctx, devKey)
	assert.Empty(t, out.String())
}

func TestStatusEndsWithContextWhileEventsAreDue(t *testing.T) {
	f := newStatusFixture(t)
	f.status.After = time.Hour
	stop := start(t, f.status.Run)
	f.watched.Watch([]store.Topic{devKey.Topic()})
	waitFor(t, "status", func() bool { return f.has(devKey) })
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	f.fake.settle()
	stop()
}

func TestStatusReleasedWhileADueReadIsPending(t *testing.T) {
	f := newStatusFixture(t)
	f.status.After = 150 * time.Millisecond
	start(t, f.status.Run)
	release := f.watched.Watch([]store.Topic{devKey.Topic()})
	waitFor(t, "status", func() bool { return f.has(devKey) })
	f.fake.settle()
	n := f.reads()
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	time.Sleep(30 * time.Millisecond)
	release()
	never(t, "a read of the closed page", 300*time.Millisecond, func() bool { return f.reads() != n })
}

func TestEarliest(t *testing.T) {
	_, ok := earliest(nil)
	assert.False(t, ok)
	now := time.Now()
	at, ok := earliest(map[store.StatusKey]time.Time{devKey: now.Add(time.Second), prodKey: now, ciKey: now.Add(time.Minute)})
	require.True(t, ok)
	assert.Equal(t, now, at)
}

func TestStatusDefaultsAndReadEachDeduplicates(t *testing.T) {
	f := newStatusFixture(t)
	f.status.Every, f.status.After = 0, 0
	f.watched.Watch([]store.Topic{devKey.Topic()})
	n := f.reads()
	f.status.readEach(t.Context(), []store.StatusKey{devKey, devKey})
	assert.Equal(t, n+1, f.reads())
	stop := start(t, f.status.Run)
	f.fake.settle()
	stop()
}

func TestStatusReadsEveryListedCluster(t *testing.T) {
	f := newStatusFixture(t)
	f.status.All = time.Hour
	f.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}, {Name: "prod"}})
	start(t, f.status.Run)
	waitFor(t, "the listed clusters at start", func() bool { return f.has(devKey) && f.has(prodKey) })
	assert.False(t, f.has(ciKey), "not listed, no page")

	// A cluster that the store newly lists is read at once.
	f.st.SetClusters("ci", []cluster.Summary{{Name: "a"}})
	waitFor(t, "a new cluster", func() bool { return f.has(ciKey) })
	f.fake.settle()

	// An event of a listed cluster reads it, although no page is open.
	n := f.reads()
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	waitFor(t, "read", func() bool { return f.reads() > n })

	// A cluster that the store no longer lists loses its status.
	f.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	waitFor(t, "dropped", func() bool { return !f.has(prodKey) })
	assert.True(t, f.has(devKey))

	// One that comes back is new again.
	f.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}, {Name: "prod"}})
	waitFor(t, "back", func() bool { return f.has(prodKey) })
}

func TestStatusReadsEveryClusterWhileAPageIsOpen(t *testing.T) {
	f := newStatusFixture(t)
	f.status.All = 30 * time.Millisecond
	f.status.Viewers = &Viewers{}
	f.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	start(t, f.status.Run)
	waitFor(t, "status at start", func() bool { return f.has(devKey) })
	f.fake.settle()

	n := f.reads()
	never(t, "a read with no page open", 150*time.Millisecond, func() bool { return f.reads() != n })
	f.status.Viewers.Watch(nil)
	waitFor(t, "read when the first page opens, and every All", func() bool { return f.reads() >= n+3 })
}

func TestStatusWithoutViewersReadsEveryClusterAlways(t *testing.T) {
	f := newStatusFixture(t)
	f.status.All = 30 * time.Millisecond
	f.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	start(t, f.status.Run)
	waitFor(t, "read every All", func() bool { return f.reads() >= 3 })
}

func TestStatusReadsWhatSlurmSaysOnlyWhileThePageIsOpen(t *testing.T) {
	f := newStatusFixture(t)
	f.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	start(t, f.status.Run)
	waitFor(t, "status", func() bool { return f.has(devKey) })
	f.fake.settle()
	assert.Nil(t, f.st.Snapshot().Slurm("sind", "dev"), "no page of dev is open")

	release := f.watched.Watch([]store.Topic{devKey.Topic()})
	waitFor(t, "what Slurm says", func() bool { return f.st.Snapshot().Slurm("sind", "dev") != nil })
	release()
	waitFor(t, "dropped with the page", func() bool { return f.st.Snapshot().Slurm("sind", "dev") == nil })
	assert.True(t, f.has(devKey), "the status stays: the store lists dev")
}
