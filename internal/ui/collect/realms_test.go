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
	f := &realmFixture{fake: newFake(t), st: store.New(), bus: NewBus()}
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
	return &Lock{Store: f.st, Client: f.fake.client(), Bus: f.bus, Log: quietLog(), Every: time.Hour}
}

func (f *realmFixture) holder(realm string) string {
	if h := f.st.Snapshot().Lock(realm); h != nil {
		return h.Command
	}
	return ""
}

func TestLockReadsAtStartAndOnLockEvents(t *testing.T) {
	f := newRealmFixture(t, "sind", "ci")
	f.fake.set(func() { f.fake.locks["sind"] = "sind create cluster dev" })
	start(t, f.lock().Run)

	waitFor(t, "lock at start", func() bool { return f.holder("sind") == "sind create cluster dev" })
	h := f.st.Snapshot().Lock("sind")
	assert.Equal(t, 42, h.PID)
	assert.Nil(t, f.st.Snapshot().Lock("ci"))

	// A lock taken in a realm that has no containers yet: the event names
	// the realm, the store does not know it.
	f.fake.set(func() { f.fake.locks["new"] = "sind create cluster x" })
	f.bus.Publish(Event{Kind: KindLock, Realm: "new"})
	waitFor(t, "lock of a new realm", func() bool { return f.holder("new") == "sind create cluster x" })

	// Released.
	f.fake.set(func() { delete(f.fake.locks, "sind") })
	f.bus.Publish(Event{Kind: KindLock, Realm: "sind"})
	waitFor(t, "lock released", func() bool { return f.holder("sind") == "" })
	// A realm that is neither listed nor held is not read again.
	f.fake.settle()
	n := f.fake.count("network")
	f.bus.Publish(Event{Kind: KindRefresh, Realm: "ci"})
	waitFor(t, "refresh reads", func() bool { return f.fake.count("network") > n })
}

func TestLockIgnoresOtherEvents(t *testing.T) {
	f := newRealmFixture(t, "sind")
	start(t, f.lock().Run)
	f.fake.settle()
	n := f.fake.count("network")

	f.bus.Publish(Event{Kind: KindContainer, Realm: "sind", Cluster: "dev"})
	f.bus.Publish(Event{Kind: KindNetwork, Realm: "sind"})
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

func TestLockResyncAndDockerDown(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.locks["sind"] = "one" })
	l := f.lock()
	l.Every = 30 * time.Millisecond
	start(t, l.Run)
	waitFor(t, "lock", func() bool { return f.holder("sind") == "one" })

	// While Docker is down nothing is read, not even by the poll.
	f.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	f.fake.settle()
	f.fake.set(func() { f.fake.locks["sind"] = "two" })
	n := f.fake.count("network")
	never(t, "a read while down", 120*time.Millisecond, func() bool { return f.fake.count("network") != n })

	// It answers again: everything is read at once, before the next poll.
	l.Every = time.Hour
	f.st.SetDocker(store.DockerState{Checked: true})
	waitFor(t, "read after recovery", func() bool { return f.holder("sind") == "two" })

	f.fake.set(func() { f.fake.locks["sind"] = "three" })
	f.bus.Publish(Event{})
	waitFor(t, "resync", func() bool { return f.holder("sind") == "three" })
}

func TestLockResyncWhileDownReadsNothing(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	start(t, f.lock().Run)
	f.fake.settle()
	f.bus.Publish(Event{})
	never(t, "a read", 80*time.Millisecond, func() bool { return f.fake.count("network") != 0 })
}

func TestLockKeepsTheHolderOnAReadError(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.locks["sind"] = "one" })
	start(t, f.lock().Run)
	waitFor(t, "lock", func() bool { return f.holder("sind") == "one" })

	f.fake.set(func() { f.fake.failing = "inspect"; delete(f.fake.locks, "sind") })
	f.bus.Publish(Event{Kind: KindLock, Realm: "sind"})
	f.fake.settle()
	assert.Equal(t, "one", f.holder("sind"))
}

func (f *realmFixture) monitoringCollector() *Monitoring {
	return &Monitoring{Store: f.st, Client: f.fake.client(), Bus: f.bus, Log: quietLog(), Every: time.Hour}
}

func TestMonitoringReadsAtStartOnEventsAndWhenGone(t *testing.T) {
	f := newRealmFixture(t, "sind", "ci")
	f.fake.set(func() { f.fake.monitoring["sind"] = true })
	start(t, f.monitoringCollector().Run)

	waitFor(t, "monitoring", func() bool { return f.st.Snapshot().MonitoringOf("sind") != nil })
	m := f.st.Snapshot().MonitoringOf("sind")
	assert.Equal(t, "sind", m.Realm)
	require.Len(t, m.Services, 4)
	assert.Nil(t, f.st.Snapshot().MonitoringOf("ci"))

	// The stack of ci is made: its container events say so.
	f.fake.set(func() { f.fake.monitoring["ci"] = true })
	f.bus.Publish(Event{Kind: KindContainer, Realm: "ci", Cluster: "a"})
	waitFor(t, "ci", func() bool { return f.st.Snapshot().MonitoringOf("ci") != nil })

	// The stack of sind is removed.
	f.fake.set(func() { delete(f.fake.monitoring, "sind") })
	f.bus.Publish(Event{Kind: KindNetwork, Realm: "sind"})
	waitFor(t, "sind gone", func() bool { return f.st.Snapshot().MonitoringOf("sind") == nil })
	assert.NotNil(t, f.st.Snapshot().MonitoringOf("ci"))
}

func TestMonitoringIgnoresLockEventsAndKeepsOnErrors(t *testing.T) {
	f := newRealmFixture(t, "sind")
	f.fake.set(func() { f.fake.monitoring["sind"] = true })
	start(t, f.monitoringCollector().Run)
	waitFor(t, "monitoring", func() bool { return f.st.Snapshot().MonitoringOf("sind") != nil })
	f.fake.settle()
	n := f.fake.count("ps")

	f.bus.Publish(Event{Kind: KindLock, Realm: "sind"})
	never(t, "a read for a lock", 80*time.Millisecond, func() bool { return f.fake.count("ps") != n })

	f.fake.set(func() { f.fake.failing = "ps" })
	f.bus.Publish(Event{Kind: KindContainer, Realm: "sind"})
	waitFor(t, "a failed read", func() bool { return f.fake.count("ps") > n })
	f.fake.settle()
	assert.NotNil(t, f.st.Snapshot().MonitoringOf("sind"))
}

func TestMonitoringPolls(t *testing.T) {
	f := newRealmFixture(t, "sind")
	m := f.monitoringCollector()
	m.Every = 30 * time.Millisecond
	start(t, m.Run)
	f.fake.set(func() { f.fake.monitoring["sind"] = true })
	waitFor(t, "found by the poll", func() bool { return f.st.Snapshot().MonitoringOf("sind") != nil })
}

func TestLockAndMonitoringStartWithDefaultIntervals(t *testing.T) {
	f := newRealmFixture(t, "sind")
	l, m := f.lock(), f.monitoringCollector()
	l.Every, m.Every = 0, 0
	stopL := start(t, l.Run)
	stopM := start(t, m.Run)
	f.fake.settle()
	stopL()
	stopM()
}
