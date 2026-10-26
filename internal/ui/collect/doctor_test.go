// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/doctor"
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

// hold makes `docker info` wait until release is called. A later hold
// replaces the gate for the calls after it; its release leaves that gate.
func (f *fake) hold() (release func()) {
	gate := make(chan struct{})
	f.set(func() { f.gate = gate })
	return func() {
		f.set(func() {
			if f.gate == gate {
				f.gate = nil
			}
		})
		close(gate)
	}
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
	assert.Equal(t, doctor.StatusFailed, first.Checks[0].Status)
	assert.Contains(t, first.Checks[0].Detail, "not reachable")

	// Again, on request: the last outcome stays while it runs.
	release = f.hold()
	d.Now = func() time.Time { return time.Date(2026, 1, 2, 3, 5, 0, 0, time.UTC) }
	d.Request()
	waitFor(t, "running again", func() bool { return st.Snapshot().Doctor.Running })
	during := st.Snapshot().Doctor
	assert.Equal(t, first.Checks, during.Checks)
	assert.Equal(t, first.Time, during.Time)
	// Requests during a run are one more run afterwards. The third run is
	// held too, so that the test sees it start: the report of the second
	// run alone does not say whether the third has begun. Its gate is set
	// once the second run waits at the first, which it would take instead.
	d.Request()
	d.Request()
	waitFor(t, "second run held", func() bool { return f.held() == 1 })
	releaseThird := f.hold()
	release()
	waitFor(t, "third run", func() bool { r := st.Snapshot().Doctor; return r.Running && r.Time.After(first.Time) })
	assert.Empty(t, d.ask, "the requests made during the run are one run")
	releaseThird()
	waitFor(t, "third report", func() bool { return !st.Snapshot().Doctor.Running })
	assert.Equal(t, 3, f.count("info"))
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
