// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/require"
)

// runFixture reduces the first n lines of the recorded event log
// activity/testdata/file (all lines for n < 0) to runs, newest first.
func runFixture(t *testing.T, file string, n int) []store.Run {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("activity", "testdata", file))
	require.NoError(t, err)
	red := activity.NewReducer(10)
	for i, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if n >= 0 && i >= n {
			break
		}
		red.Line(line)
	}
	return red.Runs()
}

// runNow is the clock of the tests of the progress display: a moment in the
// recorded runs, which start at 09:30:00 on 2026-10-14.
var runNow = time.Date(2026, 10, 14, 9, 30, 25, 0, time.UTC)

// runTestServer returns a server with the progress display and a session,
// whose clock stands at now.
func runTestServer(t *testing.T, now time.Time) *testServer {
	t.Helper()
	return runTestServerAt(t, func() time.Time { return now })
}

// runTestServerAt is runTestServer with a clock that may move.
func runTestServerAt(t *testing.T, clock func() time.Time) *testServer {
	t.Helper()
	cfg, st := testConfig(t)
	reg := NewRegistry()
	reg.AddFragment(railFragment())
	reg.AddFragment(tasksFragment())
	registerRun(reg)
	cfg.Registry = reg
	cfg.Now = clock
	ts := newTestServerFrom(t, cfg, st).login()
	st.SetRuns(nil)
	return ts
}

// runView returns a view of the snapshot of the store with the params.
func runView(ts *testServer, now time.Time, kv ...string) *View {
	v := ts.srv.newView(Params{})
	v.Now = now
	for i := 0; i+1 < len(kv); i += 2 {
		v.Params.Set(kv[i], kv[i+1])
	}
	return v
}

// runFragment renders the fragment name for the params.
func runFragment(t *testing.T, ts *testServer, now time.Time, name string, kv ...string) string {
	t.Helper()
	html, err := ts.srv.renderFragment(ts.srv.reg.fragments[name], runView(ts, now, kv...))
	require.NoError(t, err)
	return html
}

// runWithRun sets the Param id of the view to the run's and puts the run in
// its snapshot.
func (v *View) runWithRun(run store.Run) *View {
	v.Snap.Runs = []store.Run{run}
	v.Params.Set("id", run.ID)
	return v
}

// runFixtureAs is runFixture for the newest run, with the id and the trace
// that the test names, so that several recordings may share a store.
func runFixtureAs(t *testing.T, file string, n int, id, trace string) store.Run {
	t.Helper()
	run := runFixture(t, file, n)[0]
	run.ID, run.Trace = id, trace
	return run
}

// runReadFixture returns the recorded event log activity/testdata/file.
func runReadFixture(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("activity", "testdata", file))
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}
