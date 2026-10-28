// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retryRun is a create cluster of bench from a terminal whose worker-1 ran
// out of the 5m wait: a controller and its backup, a db node, two workers
// and one image.
func retryRun() store.Run {
	node := func(short, role string) store.Span {
		return runSp("target", short, runEndedAt(0, 9, "ok"), runWith(func(s *store.Span) { s.Node, s.Role = short, role }))
	}
	pull := runSp("step", "pull images", runEndedAt(0, 1, "ok"), runKids(
		runSp("target", "ghcr.io/gsi-hpc/sind-node:26.05.4", runEndedAt(0, 1, "ok"), runWith(func(s *store.Span) { s.Node = s.Name })),
	))
	nodes := runSp("step", "nodes", runEndedAt(1, 9, "failed"), runKids(
		node("controller", "controller"), node("controller-backup", "controller"), node("db", "db"),
		node("worker-0", "worker"), timedOut("worker-1", "munge", 5*time.Minute),
	))
	run := runOf(10, "failed", runSp("wait", runLockWait, runEndedAt(0, 0.1, "ok")), pull, nodes, runSp("step", "rollback", runEndedAt(9, 10, "ok"), runKids(node("worker-9", "worker"))))
	run.ID, run.Title, run.Root.Node, run.Class = "rb", "create cluster bench", "bench", "timeout"
	return run
}

func TestActRetryOf(t *testing.T) {
	run := retryRun()
	snap := store.Snapshot{Runs: []store.Run{run}}
	r := actRetryOf(snap, "rb")
	require.NotNil(t, r)
	assert.Equal(t, "a terminal", r.Source)
	assert.Equal(t, "sind ui does not have its config.", r.Lack)
	assert.Equal(t, "failed", r.Word)
	assert.Equal(t, map[string]bool{"backup": true, "db": true}, r.Roles)
	assert.Equal(t, 2, r.Workers, "the rollback makes no worker")
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:26.05.4", r.Image)
	assert.Equal(t, "5m", r.OldWait)
	assert.Equal(t, []string{"wait", "10m"}, r.NewWait)
	assert.Equal(t, "10m", r.NewWaitWord())
	assert.Equal(t, "worker-1 ran out of the 5m wait", r.Why)
	assert.Empty(t, r.Realm)
	assert.Equal(t, "a controller, a backup controller, a db node and 2 workers", r.Nodes())

	for name, id := range map[string]string{"no id": "", "no such run": "nope"} {
		assert.Nil(t, actRetryOf(snap, id), name)
	}
	for name, change := range map[string]func(*store.Run){
		"another command": func(r *store.Run) { r.Root.Name = "create worker" },
		"ok":              func(r *store.Run) { r.Status = store.StatusOK },
		"goes on":         func(r *store.Run) { r.End = nil },
	} {
		other := retryRun()
		change(&other)
		assert.Nil(t, actRetryOf(store.Snapshot{Runs: []store.Run{other}}, "rb"), name)
	}

	// Stopped, from sind ui or another program, two images, and nodes
	// that timed out without a wait the log names.
	stopped := runOf(9, "canceled",
		runSp("step", "pull images", runKids(runSp("target", "a:1"), runSp("target", "b:1"))),
		runSp("step", "nodes", runKids(timedOut("worker-0", "munge", 0))))
	stopped.ID, stopped.Root.Name, stopped.FromUI = "rs", "create cluster", true
	r = actRetryOf(store.Snapshot{Runs: []store.Run{stopped}}, "rs")
	assert.Equal(t, "sind ui", r.Source)
	assert.Equal(t, "sind ui no longer keeps its config.", r.Lack)
	assert.Equal(t, "stopped", r.Word)
	assert.Empty(t, r.Image)
	assert.Empty(t, r.OldWait)
	assert.Equal(t, "a controller and 1 worker", r.Nodes())
	stopped.FromUI, stopped.Parent = false, "00f067aa0ba902b7"
	stopped.Root.Children = nil
	r = actRetryOf(store.Snapshot{Runs: []store.Run{stopped}}, "rs")
	assert.Equal(t, "another program", r.Source)
	assert.Equal(t, "a controller", r.Nodes())
	assert.Empty(t, r.NewWaitWord())

	assert.False(t, (&actDialog{}).FromRun("nodes"))
	assert.False(t, (&actDialog{Retry: r}).FromRun("image"), "the log names no image")
	assert.False(t, r.HasNodes)
	assert.False(t, (&actDialog{Retry: r}).FromRun("nodes"), "the log names no nodes")
	assert.True(t, (&actDialog{Retry: &actRetry{HasNodes: true}}).FromRun("nodes"))
	assert.Equal(t, url.Values{"name": {"x"}}, actKeptForm(url.Values{"name": {"x"}, "from": {"rb"}}))
}

func TestActDialog_TryARunAgain(t *testing.T) {
	f := newActFixture(t)
	f.st.SetRuns([]store.Run{retryRun()})
	w := f.get("/dialogs/create-cluster?from=rb&name=bench")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, want := range []string{
		`<h2 id="act-title">Try bench again</h2>`,
		`<p id="act-lead">Rebuilt from the run rb. Try again saves the config, then runs sind create cluster with it.</p>`,
		`<input type="hidden" name="from" value="rb">`,
		`<b>This run came from a terminal. sind ui does not have its config.</b>`,
		`<li>Nodes: a controller, a backup controller, a db node and 2 workers, as the run had them.</li>`,
		`<li>Image: <span class="mono">ghcr.io/gsi-hpc/sind-node:26.05.4</span>, as the run pulled it.</li>`,
		`<li>Wait: 5m → 10m, as worker-1 ran out of the 5m wait.</li>`,
		`<li>Not in the log: CPUs, memory, /data, users and Slurm lines. They have the defaults of New cluster.</li>`,
		`<button type="button" class="btn sm" data-act-yaml-start>Start from YAML…</button><a class="btn sm" href="/runs/rb">Show the failed run</a>`,
		`<label for="act-image">Image <span class="chip act-fromrun">from the run</span></label>`,
		`name="image" value="ghcr.io/gsi-hpc/sind-node:26.05.4"`,
		`<span class="lbl">Nodes <span class="chip act-fromrun">from the run</span></span>`,
		`id="act-db" name="db" checked`,
		`id="act-backup" name="backup" checked`,
		`name="w0-count" value="2"`,
		`<details class="act-sec" id="act-sec-run" open>`,
		`Wait for ready <span class="chip act-fromrun">was 5m</span>`,
		`<input type="radio" name="wait" value="10m" checked>`,
		`sind ui set 10m because worker-1 ran out of the 5m wait. Pick 5m to keep the wait of the run.`,
		`The run names no realm. sind is the default realm of a terminal.`,
		`>Try again<kbd class="act-kbd"`,
	} {
		assert.Contains(t, body, want)
	}
	assert.Equal(t, 1, strings.Count(body, "data-act-yaml-start"), "the notice has Start from YAML")
	assert.Contains(t, body, `<p class="hint act-retry-yaml">Try again runs your YAML in place of the failed run rb. Run options keep its wait and its pull.</p>`)
	assert.Contains(t, body, `id="act-pull" name="pull" aria-describedby`, "a fixed tag: no pull, as a choice that YAML keeps")
	assert.NotContains(t, body, `name="pull_auto"`)
	assert.NotContains(t, body, `id="act-api" name="api" checked`)
	assert.NotContains(t, body, `id="act-submitter" name="submitter" checked`)

	// The preview keeps the label of the button.
	pw := f.post("/actions/create-cluster/preview", actForm("realm", "sind", "name", "bench", "from", "rb", "wg", "w0", "w0-count", "2", "wait", "10m", "dtype", "volume"))
	require.Equal(t, http.StatusOK, pw.Code)
	assert.Contains(t, pw.Body.String(), `>Try again<kbd class="act-kbd"`)
	assert.Contains(t, pw.Body.String(), `Ready. Try again runs the command.`)
	assert.Contains(t, pw.Body.String(), ` · wait 10m</span>`, "the footer names the longer wait")
	pw = f.post("/actions/create-cluster/preview", actForm("realm", "sind", "name", "bench", "from", "rb", "wg", "w0", "w0-count", "2", "wait", "other", "wait_other", "20m", "dtype", "volume"))
	assert.Contains(t, pw.Body.String(), ` · wait 20m</span>`)

	// A run stopped while it waited for the realm lock names no nodes:
	// they have the defaults, without the tag of the run.
	lock := runOf(2, "canceled", runSp("wait", runLockWait, runEndedAt(0, 2, "canceled")))
	lock.ID, lock.Title, lock.Root.Name, lock.Root.Node = "rs", "create cluster bench", "create cluster", "bench"
	f.st.SetRuns([]store.Run{lock})
	body = f.get("/dialogs/create-cluster?from=rs&name=bench").Body.String()
	assert.Contains(t, body, `<li>Nodes: not in the log, so the defaults of New cluster.</li>`)
	assert.Contains(t, body, `<span class="lbl">Nodes</span>`)
	assert.Contains(t, body, `Show the stopped run`)

	// A run that sind ui does not know: New cluster, as without it.
	body = f.get("/dialogs/create-cluster?from=nope").Body.String()
	assert.Contains(t, body, `<h2 id="act-title">New cluster</h2>`)
	assert.NotContains(t, body, `name="from"`)
}
