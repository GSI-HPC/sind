// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTaskPageRedirectsToTheRun(t *testing.T) {
	ts := runTestServer(t, runNow)
	runStore(t, ts)

	w := ts.get("/tasks/t-mid")
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/runs/r-mid", w.Header().Get("Location"))

	// A request that htmx makes is told to go there with a header.
	r := request("GET", "/tasks/t-mid")
	r.AddCookie(ts.cookie)
	r.Header.Set("HX-Request", "true")
	w = ts.do(r)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "/runs/r-mid", w.Header().Get("HX-Redirect"))
}

func TestRunTaskPageWaits(t *testing.T) {
	ts := runTestServer(t, runNow)
	ts.st.AddTask(store.Task{ID: "t1", Display: "sind create cluster dev", Trace: "tr", Start: runAt(10), Status: store.TaskRunning})

	w := ts.get("/tasks/t1")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, want := range []string{
		"<title>Starting create cluster dev · sind ui</title>",
		"<h1>create cluster dev</h1>",
		"Waiting for the command to write its first progress event.",
		`action="/tasks/t1/cancel"`,
		`<b>Stop create cluster dev?</b> sind stops the command, as Ctrl-C does in a terminal.`,
		`<span class="pill run">running</span>`,
		`data-since="`,
		`data-say-key="w:t1" data-say-state="running" data-say="create cluster dev: started."`,
		"/events?f=rail&amp;f=run-wait&amp;f=tasks&amp;id=t1&amp;nav=activity&amp;task=t1",
	} {
		assert.True(t, strings.Contains(body, want), "the page lacks %s", want)
	}
	assert.NotContains(t, body, `class="task running"`, "the dock does not repeat the task of the page")
	assert.NotContains(t, body, `hx-trigger="load"`, "no run to go to yet")

	// The run shows up: the fragment asks for the redirect.
	ts.st.SetRuns([]store.Run{{ID: "r1", Trace: "tr", Title: "x"}})
	html := runFragment(t, ts, runNow, "run-wait", "id", "t1")
	assert.Contains(t, html, `hx-get="/tasks/t1" hx-trigger="load" hx-swap="none"`)
}

func TestRunTaskPageEndedWithoutRun(t *testing.T) {
	ts := runTestServer(t, runNow)
	ts.st.AddTask(store.Task{ID: "f", Display: "d", Trace: "tr", Start: runAt(10), End: runPtr(runAt(11)), Status: store.TaskFailed, Err: "docker: not found", Output: "boom <b>"})
	ts.st.AddTask(store.Task{ID: "c", Display: "d", Trace: "tr2", Start: runAt(10), End: runPtr(runAt(11)), Status: store.TaskCanceled})
	ts.st.AddTask(store.Task{ID: "o", Display: "d", Trace: "tr3", Start: runAt(10), End: runPtr(runAt(11)), Status: store.TaskOK})
	ts.st.AddTask(store.Task{ID: "s", Display: "d", Trace: "tr4", Start: runAt(10), Status: store.TaskRunning, Canceling: true})

	body := ts.get("/tasks/f").Body.String()
	assert.Contains(t, body, "The command ended (failed) before the event log held a run of it. docker: not found")
	assert.Contains(t, body, `<pre class="term tout">boom &lt;b&gt;</pre>`)
	assert.Contains(t, body, `<div class="callout bad">`)
	assert.NotContains(t, body, "/cancel")

	body = ts.get("/tasks/c").Body.String()
	assert.Contains(t, body, `<div class="callout warn">The command ended (canceled)`)
	assert.NotContains(t, body, "standard error")
	// The dock keeps the card of the failed command to one line of its
	// error, and leaves its output to its page.
	assert.Contains(t, body, `<div class="tl terr"><span title="docker: not found">docker: not found</span><span class="num">1.0s</span></div>`)
	assert.NotContains(t, body, "boom")
	body = ts.get("/tasks/o").Body.String()
	assert.Contains(t, body, `<div class="callout">The command ended without errors. Commands such as Drain and Resume write no progress`)
	ts.st.AddTask(store.Task{ID: "w", Display: "d", Trace: "tr5", Start: runAt(10), End: runPtr(runAt(11)), Status: store.TaskOK, Output: "warning"})
	body = ts.get("/tasks/w").Body.String()
	assert.Contains(t, body, `<div class="callout ">The command ended (ok)`)
	assert.Contains(t, body, `<pre class="term tout">warning</pre>`)
	body = ts.get("/tasks/s").Body.String()
	assert.Contains(t, body, "Stopping…")
	assert.NotContains(t, body, `action="/tasks/s/cancel"`)
}

func TestRunTaskPageOfExec(t *testing.T) {
	ts := runTestServer(t, runNow)
	ts.st.AddTask(store.Task{ID: "r", Display: "sind exec dev -- scontrol update NodeName=worker-0 State=RESUME", Trace: "tr", Start: runAt(10), Status: store.TaskRunning})
	ts.st.AddTask(store.Task{ID: "f", Display: "sind --realm ci exec ci1 -- scontrol update NodeName=worker-0 State=RESUME", Trace: "tr2", Start: runAt(10), End: runPtr(runAt(11)), Status: store.TaskFailed, Err: "exit status 1"})

	body := ts.get("/tasks/r").Body.String()
	assert.Contains(t, body, "The command runs. It writes no progress, so it has no run page: this page shows how it ends.")
	assert.NotContains(t, body, "first progress event")
	body = ts.get("/tasks/f").Body.String()
	assert.Contains(t, body, "The command ended (failed). exit status 1")
	assert.NotContains(t, body, "event log")
}

func TestRunless(t *testing.T) {
	for display, want := range map[string]bool{
		"sind exec dev -- scontrol ping": true, "sind --realm ci exec": true,
		"sind create cluster": false, "sind --realm ci delete realm": false, "sind": false, "sind --realm": false, "": false,
	} {
		assert.Equal(t, want, runless(store.Task{Display: display}), display)
	}
}

func TestRunTaskRoutesRefuseALongId(t *testing.T) {
	ts := runTestServer(t, runNow)
	long := strings.Repeat("x", maxParamLen+1)
	assert.Equal(t, http.StatusBadRequest, ts.get("/tasks/"+long).Code)
	assert.Equal(t, http.StatusBadRequest, ts.post("/tasks/"+long+"/dismiss", "").Code)
}

func TestRunTaskPageGone(t *testing.T) {
	ts := runTestServer(t, runNow)
	w := ts.get("/tasks/nope")
	assert.Equal(t, http.StatusNotFound, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "sind ui holds no task nope.")
	assert.Contains(t, body, "sind ui keeps the last 25 commands that ended, until it stops.")
	assert.Contains(t, body, `<a class="btn primary" href="/activity">Activity</a>`)
	assert.NotContains(t, body, "may have been deleted")
	// A run of that id is the closest match.
	ts.st.SetRuns([]store.Run{{ID: "r-1", Title: "create cluster"}})
	assert.Contains(t, ts.get("/tasks/r-1").Body.String(), `<a class="btn primary" href="/runs/r-1">Run r-1</a>`)
	assert.Contains(t, runFragment(t, ts, runNow, "run-wait", "id", "nope"), "This task is gone")
	// A task without a display shows its action.
	ts.st.AddTask(store.Task{ID: "a", Action: "power", Start: runAt(1), Status: store.TaskRunning})
	assert.Contains(t, runFragment(t, ts, runNow, "run-wait", "id", "a"), `<h1>power</h1>`)
}

func TestRunTaskDismiss(t *testing.T) {
	ts := runTestServer(t, runNow)
	ts.st.AddTask(store.Task{ID: "done", Status: store.TaskFailed, Start: runAt(1)})
	ts.st.AddTask(store.Task{ID: "busy", Status: store.TaskRunning, Start: runAt(1)})

	w := ts.post("/tasks/busy/dismiss", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "the command still runs; stop it first")
	_, ok := ts.st.Snapshot().Task("busy")
	assert.True(t, ok)

	w = ts.post("/tasks/done/dismiss", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	done, ok := ts.st.Snapshot().Task("done")
	require.True(t, ok, "the task stays for its run page")
	assert.True(t, done.Hidden)
	assert.NotContains(t, runFragment(t, ts, runNow, "tasks"), "/tasks/done", "the dock leaves it out")
	busy, _ := ts.st.Snapshot().Task("busy")
	assert.False(t, busy.Hidden)

	// Closing a card twice, as two tabs do, is not an error.
	assert.Equal(t, http.StatusNoContent, ts.post("/tasks/done/dismiss", "").Code)

	// Without the CSRF token nothing happens.
	r := request("POST", "/tasks/busy/dismiss")
	r.Header.Set("Origin", "http://127.0.0.1:7878")
	r.AddCookie(ts.cookie)
	assert.Equal(t, http.StatusForbidden, ts.do(r).Code)
}

func TestRunRoutesAreRegistered(t *testing.T) {
	// The default registry, which the server of sind ui uses, has them all.
	r := defaultRegistry.snapshot()
	for _, name := range append([]string{"tasks", "run-list", "run-wait"}, runFragments...) {
		_, ok := r.fragments[name]
		assert.True(t, ok, name)
	}
	var pages []string
	for _, p := range r.pages {
		pages = append(pages, p.Pattern)
	}
	assert.Contains(t, pages, "GET /runs/{id}")
	var routes []string
	for _, rt := range r.routes {
		routes = append(routes, rt.Pattern)
	}
	assert.Contains(t, routes, "GET /tasks/{id}")
	assert.Contains(t, routes, "GET /activity")
	assert.Contains(t, routes, "POST /tasks/{id}/dismiss")
	assert.NotNil(t, r.navCount("activity"))
}

func TestRunTaskClear(t *testing.T) {
	ts := runTestServer(t, runNow)
	ts.st.AddTask(store.Task{ID: "run", Status: store.TaskRunning})
	ts.st.AddTask(store.Task{ID: "ok", Status: store.TaskOK, End: runPtr(runAt(1))})
	ts.st.AddTask(store.Task{ID: "bad", Status: store.TaskFailed, End: runPtr(runAt(1))})
	w := ts.post("/tasks/clear", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	tasks := ts.st.Snapshot().Tasks
	require.Len(t, tasks, 3, "the tasks stay for their run pages")
	assert.False(t, tasks[0].Hidden, "the command that runs keeps its card")
	assert.True(t, tasks[1].Hidden)
	assert.True(t, tasks[2].Hidden)
}

func TestRunTaskPageOffersToTryAgain(t *testing.T) {
	ts := runTestServer(t, runNow)
	end := runAt(12)
	ts.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	ts.st.AddTask(store.Task{
		ID: "t1", Action: "create-worker", Realm: "sind", Cluster: "dev", Display: "sind create worker dev --count 2",
		Form:  map[string][]string{"realm": {"sind"}, "cluster": {"dev"}, "count": {"2"}},
		Trace: "tr", Start: runAt(10), End: &end, Status: store.TaskFailed, Err: "exit status 2",
	})
	body := ts.get("/tasks/t1").Body.String()
	for _, want := range []string{
		`<code>sind create worker dev --count 2</code><button type="button" data-pg-copy>Copy</button>`,
		`action="/tasks/t1/retry"`,
		`>Edit and try again…</a>`,
		`<a class="btn" href="/clusters/sind/dev">Open dev</a>`,
	} {
		assert.Contains(t, body, want)
	}
}
