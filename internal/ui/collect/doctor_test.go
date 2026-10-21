// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDoctorFixture(t *testing.T) (*Doctor, *fake, *store.Store) {
	t.Helper()
	f := newFake(t)
	st := store.New()
	d := NewDoctor(st, f.client())
	d.Fs = afero.NewMemMapFs()
	d.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return d, f, st
}

func (f *fake) hold() (release func()) {
	gate := make(chan struct{})
	f.set(func() { f.gate = gate })
	return func() { f.set(func() { f.gate = nil }); close(gate) }
}

func TestDoctorChecksAtStartAndOnRequest(t *testing.T) {
	d, f, st := newDoctorFixture(t)
	release := f.hold()
	start(t, d.Run)

	waitFor(t, "running", func() bool { r := st.Snapshot().Doctor; return r != nil && r.Running })
	assert.Empty(t, st.Snapshot().Doctor.Checks)
	release()
	waitFor(t, "report", func() bool { r := st.Snapshot().Doctor; return r != nil && !r.Running })
	first := st.Snapshot().Doctor
	assert.Equal(t, d.Now(), first.Time)
	require.NotEmpty(t, first.Checks)
	assert.Equal(t, "Docker Engine", first.Checks[0].Name)
	assert.Equal(t, "failed", first.Checks[0].Status)
	assert.Contains(t, first.Checks[0].Detail, "not reachable")

	// Again, on request: the last outcome stays while it runs.
	release = f.hold()
	d.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 5, 0, 0, time.UTC) }
	d.Request()
	waitFor(t, "running again", func() bool { return st.Snapshot().Doctor.Running })
	during := st.Snapshot().Doctor
	assert.Equal(t, first.Checks, during.Checks)
	assert.Equal(t, first.Time, during.Time)
	// Requests during a run are one more run afterwards.
	d.Request()
	d.Request()
	release()
	waitFor(t, "second report", func() bool { r := st.Snapshot().Doctor; return !r.Running && r.Time.After(first.Time) })
	assert.Equal(t, 1, f.count("info")-1-1, "the request made during the run runs once more")
}

func TestDoctorUsesDefaultRealmWhenSindRealmIsInvalid(t *testing.T) {
	t.Setenv("SIND_REALM", "not a realm!")
	d, _, st := newDoctorFixture(t)
	stop := start(t, d.Run)
	waitFor(t, "report", func() bool { r := st.Snapshot().Doctor; return r != nil && !r.Running })
	stop()
}

func TestDoctorKeepsTheLastReportWhenCancelled(t *testing.T) {
	d, f, st := newDoctorFixture(t)
	stop := start(t, d.Run)
	waitFor(t, "report", func() bool { r := st.Snapshot().Doctor; return r != nil && !r.Running })
	last := st.Snapshot().Doctor

	release := f.hold()
	d.Request()
	waitFor(t, "running", func() bool { return st.Snapshot().Doctor.Running })
	go func() { time.Sleep(20 * time.Millisecond); release() }()
	stop()
	assert.Equal(t, last, st.Snapshot().Doctor)
}

func TestDoctorCancelledBeforeAnyReport(t *testing.T) {
	d, _, st := newDoctorFixture(t)
	ctx, cancel := cancelled(t.Context())
	defer cancel()
	d.check(ctx)
	assert.Nil(t, st.Snapshot().Doctor)
}

func TestDoctorDefaults(t *testing.T) {
	st := store.New()
	f := newFake(t)
	d := NewDoctor(st, f.client())
	assert.Nil(t, d.Fs)
	assert.Nil(t, d.Now)
	stop := start(t, d.Run)
	waitFor(t, "report", func() bool { r := st.Snapshot().Doctor; return r != nil && !r.Running })
	assert.WithinDuration(t, time.Now(), st.Snapshot().Doctor.Time, time.Minute)
	stop()
}
