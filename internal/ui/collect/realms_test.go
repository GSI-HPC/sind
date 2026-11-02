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

// realmFixture runs Lock and Monitoring on a fake daemon.
type realmFixture struct {
	fake *fake
	st   *store.Store
	bus  *Bus
}

func newRealmFixture(t *testing.T, realms ...string) *realmFixture {
	t.Helper()
	f := &realmFixture{fake: newFake(t), st: store.New(), bus: &Bus{}}
	f.setRealms(realms...)
	f.st.SetDocker(store.DockerState{Checked: true})
	return f
}

func (f *realmFixture) setRealms(realms ...string) {
	var rs []cluster.RealmSummary
	for _, r := range realms {
		rs = append(rs, cluster.RealmSummary{Name: r, Clusters: 1})
	}
	f.st.SetRealms(rs)
}

func (f *realmFixture) lock() *Lock {
	return &Lock{Store: f.st, Client: f.fake.client(), Bus: f.bus, Every: time.Hour}
}

func (f *realmFixture) holder(realm string) string {
	if h := f.st.Snapshot().Locks[realm]; h != nil {
		return h.Command
	}
	return ""
}

func TestLockReadsAtStartAndOnLockEvents(t *testing.T) {
	f := newRealmFixture(t, "sind", "ci")
	f.fake.set(func() { f.fake.locks["sind"] = "sind create cluster dev" })
	start(t, f.lock().Run)

	waitFor(t, "lock at start", func() bool { return f.holder("sind") == "sind create cluster dev" })
	h := f.st.Snapshot().Locks["sind"]
	assert.Equal(t, 42, h.PID)
	assert.Nil(t, f.st.Snapshot().Locks["ci"])

	// A lock taken in a realm that has no containers yet: the event names
	// the realm, the store does not know it.
	f.fake.set(func() { f.fake.locks["new"] = "sind create cluster x" })
	f.bus.Notify(Event{Kind: KindLock, Realm: "new"})
	waitFor(t, "lock of a new realm", func() bool { return f.holder("new") == "sind create cluster x" })

	// Released.
	f.fake.set(func() { delete(f.fake.locks, "sind") })
	f.bus.Notify(Event{Kind: KindLock, Realm: "sind"})
	waitFor(t, "lock released", func() bool { return f.holder("sind") == "" })
	// A realm that is neither listed nor held is not read again.
	f.fake.settle()
	n := f.fake.count("network")
	f.bus.Notify(Event{Kind: KindRefresh, Realm: "ci"})
	waitFor(t, "refresh reads", func() bool { return f.fake.count("network") > n })
}

func TestLockIgnoresOtherEvents(t *testing.T) {
	f := newRealmFixture(t, "sind")
	start(t, f.lock().Run)
	f.fake.settle()
	n := f.fake.count("network")

	f.bus.Notify(Event{Kind: KindChange, Realm: "sind", Cluster: "dev"})
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind"})
	never(t, "a read", 100*time.Millisecond, func() bool { return f.fake.count("network") != n })
}

func TestLockPollFindsAReleasedLock(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.locks["sind"] = "sind delete cluster" })
	l := f.lock()
	l.Every = 30 * time.Millisecond
	start(t, l.Run)
	waitFor(t, "lock", func() bool { return f.holder("sind") != "" })

	// The command died without an event we saw, and the realm is gone too.
	f.fake.set(func() { delete(f.fake.locks, "sind") })
	f.setRealms()
	waitFor(t, "lock gone", func() bool { return f.holder("sind") == "" })
}

func TestLockReadsARealmThatAppears(t *testing.T) {
	f := newRealmFixture(t)
	start(t, f.lock().Run)
	f.fake.settle()
	f.fake.set(func() { f.fake.locks["ci"] = "sind create cluster a" })

	f.setRealms("ci")
	waitFor(t, "lock of the new realm", func() bool { return f.holder("ci") != "" })

	// The realm goes and comes back: read again.
	f.fake.set(func() { f.fake.locks["ci"] = "another" })
	waitFor(t, "lock again", func() bool {
		f.setRealms()
		time.Sleep(2 * time.Millisecond)
		f.setRealms("ci")
		time.Sleep(2 * time.Millisecond)
		return f.holder("ci") == "another"
	})
}

func TestLockReadsNothingWhileDockerIsDown(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.locks["sind"] = "one" })
	l := f.lock()
	l.Every = 30 * time.Millisecond
	start(t, l.Run)
	waitFor(t, "lock", func() bool { return f.holder("sind") == "one" })

	// While Docker is down nothing is read, not even by the poll.
	f.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	f.fake.settle()
	n := f.fake.count("network")
	never(t, "a read while down", 120*time.Millisecond, func() bool { return f.fake.count("network") != n })
}

func TestLockReadsAllWhenDockerAnswersAgain(t *testing.T) {
	// The poll is an hour away, so only the recovery reads.
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.locks["sind"] = "one" })
	start(t, f.lock().Run)
	waitFor(t, "lock", func() bool { return f.holder("sind") == "one" })

	f.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	f.fake.settle()
	f.fake.set(func() { f.fake.locks["sind"] = "two" })
	n := f.fake.count("network")
	never(t, "a read while down", 50*time.Millisecond, func() bool { return f.fake.count("network") != n })

	f.st.SetDocker(store.DockerState{Checked: true})
	waitFor(t, "read after recovery", func() bool { return f.holder("sind") == "two" })

	f.fake.set(func() { f.fake.locks["sind"] = "three" })
	f.bus.Notify(Event{})
	waitFor(t, "resync", func() bool { return f.holder("sind") == "three" })
}

func TestLockResyncWhileDownReadsNothing(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	start(t, f.lock().Run)
	f.fake.settle()
	f.bus.Notify(Event{})
	never(t, "a read", 80*time.Millisecond, func() bool { return f.fake.count("network") != 0 })
}

func TestLockKeepsTheHolderOnAReadError(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.locks["sind"] = "one" })
	start(t, f.lock().Run)
	waitFor(t, "lock", func() bool { return f.holder("sind") == "one" })

	f.fake.set(func() { f.fake.failing = "inspect"; delete(f.fake.locks, "sind") })
	f.bus.Notify(Event{Kind: KindLock, Realm: "sind"})
	f.fake.settle()
	assert.Equal(t, "one", f.holder("sind"))
}

func (f *realmFixture) monitoringCollector() *Monitoring {
	return &Monitoring{Store: f.st, Client: f.fake.client(), Bus: f.bus, Interval: NewInterval(time.Hour)}
}

func TestMonitoringReadsAtStartOnEventsAndWhenGone(t *testing.T) {
	f := newRealmFixture(t, "sind", "ci")
	f.fake.set(func() { f.fake.monitoring["sind"] = true })
	start(t, f.monitoringCollector().Run)

	waitFor(t, "monitoring", func() bool { return f.st.Snapshot().Monitorings["sind"] != nil })
	m := f.st.Snapshot().Monitorings["sind"]
	assert.Equal(t, "sind", m.Realm)
	require.Len(t, m.Services, 4)
	assert.Nil(t, f.st.Snapshot().Monitorings["ci"])

	// The stack of ci is made: its container events say so.
	f.fake.set(func() { f.fake.monitoring["ci"] = true })
	f.bus.Notify(Event{Kind: KindChange, Realm: "ci", Cluster: "a"})
	waitFor(t, "ci", func() bool { return f.st.Snapshot().Monitorings["ci"] != nil })

	// The stack of sind is removed.
	f.fake.set(func() { delete(f.fake.monitoring, "sind") })
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind"})
	waitFor(t, "sind gone", func() bool { return f.st.Snapshot().Monitorings["sind"] == nil })
	assert.NotNil(t, f.st.Snapshot().Monitorings["ci"])
}

func TestMonitoringIgnoresLockEventsAndKeepsOnErrors(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.monitoring["sind"] = true })
	start(t, f.monitoringCollector().Run)
	waitFor(t, "monitoring", func() bool { return f.st.Snapshot().Monitorings["sind"] != nil })
	f.fake.settle()
	n := f.fake.count("ps")

	f.bus.Notify(Event{Kind: KindLock, Realm: "sind"})
	never(t, "a read for a lock", 80*time.Millisecond, func() bool { return f.fake.count("ps") != n })

	f.fake.set(func() { f.fake.failing = "ps" })
	f.bus.Notify(Event{Kind: KindChange, Realm: "sind"})
	waitFor(t, "a failed read", func() bool { return f.fake.count("ps") > n })
	f.fake.settle()
	assert.NotNil(t, f.st.Snapshot().Monitorings["sind"])
}

func TestMonitoringPolls(t *testing.T) {
	f := newRealmFixture(t, "sind")
	m := f.monitoringCollector()
	m.Interval = NewInterval(30 * time.Millisecond)
	start(t, m.Run)
	f.fake.set(func() { f.fake.monitoring["sind"] = true })
	waitFor(t, "found by the poll", func() bool { return f.st.Snapshot().Monitorings["sind"] != nil })
}

func TestMonitoringPollsOnlyWhileAPageIsOpen(t *testing.T) {
	f := newRealmFixture(t, "sind")
	m := f.monitoringCollector()
	m.Interval = NewInterval(20 * time.Millisecond)
	m.Viewers = &Viewers{}
	start(t, m.Run)
	f.fake.settle()
	before := f.fake.count("ps")

	// No page is open: the polls wait.
	never(t, "a poll without a page", 120*time.Millisecond, func() bool { return f.fake.count("ps") != before })

	// A page opens: it reads at once, and polls from then on.
	release := m.Viewers.Watch(nil)
	f.fake.set(func() { f.fake.monitoring["sind"] = true })
	waitFor(t, "read for the page", func() bool { return f.st.Snapshot().Monitorings["sind"] != nil })
	f.fake.set(func() { delete(f.fake.monitoring, "sind") })
	waitFor(t, "polled for the page", func() bool { return f.st.Snapshot().Monitorings["sind"] == nil })

	// The page closes: the polls wait again.
	release()
	release() // once only
	assert.True(t, m.Viewers.None())
	f.fake.settle()
	// A count of its own, as the condition of the first never may still run.
	after := f.fake.count("ps")
	never(t, "a poll after the page closed", 120*time.Millisecond, func() bool { return f.fake.count("ps") != after })
}

func TestMonitoringTakesANewIntervalAtOnce(t *testing.T) {
	f := newRealmFixture(t, "sind")
	m := f.monitoringCollector() // every hour
	start(t, m.Run)
	f.fake.settle()
	f.fake.set(func() { f.fake.monitoring["sind"] = true })

	m.Interval.Set(20 * time.Millisecond)
	m.Interval.Set(20 * time.Millisecond) // a notice is pending already
	assert.Equal(t, 20*time.Millisecond, m.Interval.Get())
	waitFor(t, "read at the change", func() bool { return f.st.Snapshot().Monitorings["sind"] != nil })
	f.fake.set(func() { delete(f.fake.monitoring, "sind") })
	waitFor(t, "polled at the new interval", func() bool { return f.st.Snapshot().Monitorings["sind"] == nil })
}

func TestCheckInterval(t *testing.T) {
	assert.NoError(t, CheckInterval(MinInterval))
	assert.NoError(t, CheckInterval(MaxInterval))
	assert.EqualError(t, CheckInterval(time.Second), "the interval must be between 5s and 1h, got 1s")
	assert.Error(t, CheckInterval(2*time.Hour))
}

func TestViewersCountPages(t *testing.T) {
	v := &Viewers{}
	one, two := v.arrivals(), v.arrivals()
	assert.True(t, v.None())
	a, b := v.Watch(nil), v.Watch(nil)
	assert.False(t, v.None())
	assert.Len(t, one, 1, "one notice, for the first page")
	assert.Len(t, two, 1, "each collector has its own")
	a()
	assert.False(t, v.None())
	b()
	assert.True(t, v.None())
	<-one
	v.Watch(nil)
	v.Watch(nil)
	assert.Len(t, one, 1, "the first page again")
}

func TestLockAndMonitoringStartWithDefaultIntervals(t *testing.T) {
	f := newRealmFixture(t, "sind")
	l, m := f.lock(), f.monitoringCollector()
	l.Every, m.Interval = 0, nil
	stopL := start(t, l.Run)
	stopM := start(t, m.Run)
	f.fake.settle()
	stopL()
	stopM()
}
