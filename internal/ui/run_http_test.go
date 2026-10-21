// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runStore fills the store with a run of each recording, a task for the one
// that goes on, and a terminal run: ids r-mid, r-ok, r-failed, r-canceled.
func runStore(t *testing.T, ts *testServer) (mid, ok, failed, canceled store.Run) {
	t.Helper()
	mid = runFixtureAs(t, "create-ok.jsonl", 59, "r-mid", "trace-mid")
	ok = runFixtureAs(t, "create-ok.jsonl", -1, "r-ok", "trace-ok")
	failed = runFixtureAs(t, "create-failed.jsonl", -1, "r-failed", "trace-failed")
	canceled = runFixtureAs(t, "create-canceled.jsonl", -1, "r-canceled", "trace-canceled")
	ts.st.SetRuns([]store.Run{mid, ok, failed, canceled})
	ts.st.AddTask(store.Task{ID: "t-mid", Display: "sind create cluster dev", Trace: "trace-mid", Start: runAt(0), Status: store.TaskRunning})
	return mid, ok, failed, canceled
}

func TestRunPageRenders(t *testing.T) {
	ts := runTestServer(t, runNow)
	runStore(t, ts)

	w := ts.get("/runs/r-mid")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, want := range []string{
		"<title>create cluster · sind ui</title>",
		`<div class="crumbs"><a href="/activity">Activity</a> / <span class="mono">r-mid</span></div>`,
		`<span class="pill run">running</span>`,
		"from sind ui",
		`action="/tasks/t-mid/cancel"`,
		`name="csrf" value="` + ts.csrf + `"`,
		"nodes 0/3 · controller ready munge",
		`data-doc-title="▸ nodes 0/3 · sind ui"`,
		`<details class="grow-details" data-open-id="0000000000000007">`,
		`class="ntile role-controller running"`,
		`stroke-dasharray="3.5 69.1"`,
		"101 MB / 101 MB",
		"/events?f=rail&amp;f=run-facts&amp;f=run-failed&amp;f=run-head&amp;f=run-more&amp;f=run-nodes&amp;f=run-summary&amp;f=run-timeline&amp;f=tasks&amp;id=r-mid&amp;nav=activity&amp;run=r-mid",
		`/css/run.css`, `/js/run.js`,
	} {
		assert.True(t, strings.Contains(body, want), "the page lacks %s", want)
	}
	assert.NotContains(t, body, `class="task running"`, "the dock does not repeat the run on show")
	assert.NotContains(t, body, "style=", "the CSP allows no inline style")
	assert.NotContains(t, body, "<script>", "no inline script")
	assert.NotContains(t, body, "What failed")
	assert.NotContains(t, body, "from a terminal, so sind ui cannot stop it")

	// Equal input, equal HTML: the live stream sends only what changed.
	assert.Equal(t, body, ts.get("/runs/r-mid").Body.String())

	failed := ts.get("/runs/r-failed").Body.String()
	assert.Contains(t, failed, "<h2>What failed</h2>")
	assert.Contains(t, failed, `<b class="mono">worker-[1,2]</b>`)
	assert.Contains(t, failed, "Nothing is left behind")
	assert.Contains(t, failed, `<a href="/doctor">Doctor</a>`)
	assert.Contains(t, failed, `<span class="pill bad">failed</span>`)
	assert.NotContains(t, failed, "from a terminal, so sind ui cannot stop it", "an ended run has nothing to stop")

	canceled := ts.get("/runs/r-canceled").Body.String()
	assert.Contains(t, canceled, "<h2>Stopped</h2>")
	assert.Contains(t, canceled, `<span class="pill warn">canceled</span>`)

	ok := ts.get("/runs/r-ok").Body.String()
	assert.NotContains(t, ok, "rfail")
	assert.Contains(t, ok, `<h2>Other steps</h2>`)
	assert.Contains(t, ok, "done in 53s")
	assert.Contains(t, ok, "Show progress", "the dock holds the cards of the other runs")
}

func TestRunPageTerminalNote(t *testing.T) {
	ts := runTestServer(t, runNow)
	run := runFixtureAs(t, "create-ok.jsonl", 59, "r-term", "trace-term")
	ts.st.SetRuns([]store.Run{run})
	body := ts.get("/runs/r-term").Body.String()
	assert.Contains(t, body, "This run was started from a terminal, so sind ui cannot stop it. Press Ctrl-C in that terminal to stop it.")
	assert.NotContains(t, body, "/cancel")
	assert.Contains(t, body, "from a terminal")
}

func TestRunPageNotFound(t *testing.T) {
	ts := runTestServer(t, runNow)
	assert.Equal(t, http.StatusNotFound, ts.get("/runs/nope").Code)
}

func TestRunPageTitleOfARunWithoutTitle(t *testing.T) {
	ts := runTestServer(t, runNow)
	ts.st.SetRuns([]store.Run{{ID: "bare"}})
	w := ts.get("/runs/bare")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<title>Run bare · sind ui</title>")
	// Nothing in it breaks the page: no steps, no clock.
	assert.Contains(t, w.Body.String(), "No steps yet.")
}

func TestRunFragmentsOfAGoneRun(t *testing.T) {
	ts := runTestServer(t, runNow)
	for _, name := range runFragments {
		html := runFragment(t, ts, runNow, name, "id", "gone")
		if name == "run-head" {
			assert.Contains(t, html, "This run is gone", name)
			continue
		}
		assert.Empty(t, html, name)
	}
}

func TestRunFragmentsOfAnEmptyRun(t *testing.T) {
	// Partial logs: a run with a command and nothing else, and one with
	// spans of an unknown parent, must render.
	ts := runTestServer(t, runNow)
	ts.st.SetRuns([]store.Run{
		{ID: "r1", Title: "x", Root: store.Span{Kind: "command"}},
		{ID: "r2", Title: "y", Root: runSp("command", "y", runKids(runSp("target", "orphan", runStartedAt(1))))},
	})
	for _, id := range []string{"r1", "r2"} {
		for _, name := range runFragments {
			runFragment(t, ts, runNow, name, "id", id)
		}
		assert.Equal(t, http.StatusOK, ts.get("/runs/"+id).Code)
	}
}

func TestRunStreamSendsWhatChanged(t *testing.T) {
	ts := runTestServer(t, runNow)
	mid, _, _, _ := runStore(t, ts)
	c := ts.connect("f=run-summary&f=run-head&f=tasks&id=r-mid&run=r-mid")

	// A change of another run does not touch the run page; the dock hides
	// this run, and it is the dock only that other runs change.
	other := runFixtureAs(t, "create-ok.jsonl", 59, "r-other", "trace-other")
	ts.st.SetRuns([]store.Run{mid, other}, "r-other")
	e := c.next()
	assert.Equal(t, "tasks", e.name)
	assert.Contains(t, e.data, "terminal")
	c.quiet(60 * time.Millisecond)

	// The run grew: its parts change.
	grown := runFixtureAs(t, "create-ok.jsonl", 70, "r-mid", "trace-mid")
	ts.st.SetRuns([]store.Run{grown, other}, "r-mid")
	got := map[string]string{}
	for len(got) < 1 {
		e = c.next()
		got[e.name] = e.data
	}
	assert.Contains(t, got["run-summary"], "nodes 1/3")
	c.cancel()
	c.waitServed()
}

// TestRunFragmentsOfEveryPrefix renders every part of the progress display
// for the run after each line of each recording, as a viewer who opens the
// page at any moment sees it: no error, and the same HTML twice.
func TestRunFragmentsOfEveryPrefix(t *testing.T) {
	ts := runTestServer(t, runNow)
	for _, file := range []string{"create-ok.jsonl", "create-failed.jsonl", "create-canceled.jsonl", "interleaved.jsonl"} {
		data := runFixture(t, file, -1)
		require.NotEmpty(t, data, file)
		lines := strings.Count(runReadFixture(t, file), "\n") + 1
		for n := 1; n <= lines; n += 3 {
			runs := runFixture(t, file, n)
			ts.st.SetRuns(runs)
			ts.st.AddTask(store.Task{ID: "t", Trace: "other", Status: store.TaskRunning, Start: runAt(0)})
			for _, run := range runs {
				for _, now := range []time.Time{runAt(0), runAt(30), runAt(3600)} {
					for _, name := range runFragments {
						first := runFragment(t, ts, now, name, "id", run.ID)
						assert.Equal(t, first, runFragment(t, ts, now, name, "id", run.ID), "%s %s after %d lines", file, name, n)
					}
				}
			}
			runFragment(t, ts, runAt(30), "run-list")
			runFragment(t, ts, runAt(30), "tasks")
			ts.st.RemoveTask("t")
		}
	}
}
