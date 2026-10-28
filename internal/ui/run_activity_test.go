// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunActivityPage(t *testing.T) {
	ts := runTestServer(t, runNow)
	runStore(t, ts)

	w := ts.get("/activity")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, want := range []string{
		"<title>Activity · sind ui</title>",
		`<a class="tab on" href="/activity" aria-current="page">All<span class="n">4</span></a>`,
		`<a class="tab" href="/activity?status=running">Running<span class="n">1</span></a>`,
		`<a class="tab" href="/activity?status=ok">OK<span class="n">1</span></a>`,
		`<a class="tab" href="/activity?status=failed">Failed<span class="n">1</span></a>`,
		`<a class="tab" href="/activity?status=canceled">Stopped<span class="n">1</span></a>`,
		`<a class="mono" href="/runs/r-mid">create cluster dev</a>`,
		`<span class="pill run">running</span>`,
		`<span class="pill bad">failed</span>`,
		"3 ok · 2 failed · 1 stopped",
		"1 ok · 3 running",
		`<span class="chip">terminal</span>`,
		"/events?f=rail&amp;f=run-list&amp;f=tasks&amp;nav=activity",
	} {
		assert.True(t, strings.Contains(body, want), "the page lacks %s", want)
	}
	// r-mid was started by a task, so it has no terminal chip; the other three have.
	assert.Equal(t, 3, strings.Count(body, `<span class="chip">terminal</span>`))
	// The runs are listed as the snapshot has them, newest first.
	assert.Less(t, strings.Index(body, "r-mid"), strings.Index(body, "r-ok"))
	assert.Equal(t, body, ts.get("/activity").Body.String())
}

func TestRunActivityFilters(t *testing.T) {
	ts := runTestServer(t, runNow)
	runStore(t, ts)
	for filter, want := range map[string][]string{
		"running":  {"r-mid"},
		"ok":       {"r-ok"},
		"failed":   {"r-failed"},
		"canceled": {"r-canceled"},
	} {
		w := ts.get("/activity?status=" + filter)
		require.Equal(t, http.StatusOK, w.Code, filter)
		body := w.Body.String()
		assert.Contains(t, body, `<a class="tab on" href="/activity?status=`+filter+`" aria-current="page">`, filter)
		assert.Equal(t, len(want), strings.Count(body, `<a class="mono" href="/runs/`), filter)
		for _, id := range want {
			assert.Contains(t, body, `href="/runs/`+id+`"`, filter)
		}
	}
	w := ts.get("/activity?status=nonsense")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "status: unknown filter")
}

func TestRunActivityEmpty(t *testing.T) {
	ts := runTestServer(t, runNow)
	body := ts.get("/activity").Body.String()
	assert.Contains(t, body, "No runs yet.")
	assert.NotContains(t, body, "<table")
	runStore(t, ts)
	ts.st.SetRuns([]store.Run{{ID: "x", Title: "t", Start: runAt(0), End: runPtr(runAt(1)), Status: "ok"}})
	assert.Contains(t, ts.get("/activity?status=failed").Body.String(), "No run matches this filter.")
}

func TestRunActivityLimit(t *testing.T) {
	ts := runTestServer(t, runNow)
	var runs []store.Run
	for i := range runListMax + 3 {
		runs = append(runs, store.Run{ID: fmt.Sprintf("r%d", i), Start: runAt(0)})
	}
	ts.st.SetRuns(runs)
	body := ts.get("/activity").Body.String()
	assert.Equal(t, runListMax, strings.Count(body, `<a class="mono" href="/runs/`))
	assert.Contains(t, body, "3 older runs are not listed.")
	assert.Contains(t, body, `<a class="mono" href="/runs/r0">r0</a>`, "a run without a title is named by its id")
}

func TestRunNavCount(t *testing.T) {
	assert.Equal(t, NavCount{}, runNavCount(store.Snapshot{}, runNow))
	over := store.Run{End: runPtr(runAt(1)), Status: store.StatusOK}
	failed := store.Run{ID: "f", Title: "create cluster a", End: runPtr(runAt(1)), Status: store.StatusFailed}
	other := store.Run{ID: "o", Title: "create cluster b", End: runPtr(runAt(1)), Status: store.StatusFailed}
	assert.Equal(t, NavCount{}, runNavCount(store.Snapshot{Runs: []store.Run{over, over}}, runNow), "runs that ended well need no badge")
	assert.Equal(t, NavCount{Text: "2 failed", Class: "bad", Label: "2 runs failed in the last 6 hours"},
		runNavCount(store.Snapshot{Runs: []store.Run{failed, over, other}}, runNow))
	assert.Equal(t, "1 failed", runNavCount(store.Snapshot{Runs: []store.Run{failed, other}, Dismissed: map[string]bool{"o": true}}, runNow).Text,
		"a run that the user dismissed from Needs you")
	again := store.Run{Title: "create cluster b", End: runPtr(runAt(1)), Status: store.StatusOK}
	assert.Equal(t, "1 failed", runNavCount(store.Snapshot{Runs: []store.Run{again, failed, other}}, runNow).Text,
		"a run whose command ended ok since")
	assert.Equal(t, NavCount{}, runNavCount(store.Snapshot{Runs: []store.Run{failed}}, runNow.Add(7*time.Hour)), "an old failure is old news")
	assert.Equal(t, NavCount{Text: "1 running", Class: "run", Label: "1 command runs now"}, runNavCount(store.Snapshot{Runs: []store.Run{failed, {}}}, runNow))
	assert.Equal(t, "2 commands run now", runNavCount(store.Snapshot{Runs: []store.Run{{}, {}}}, runNow).Label)
}

func TestRunMatchesAndCounts(t *testing.T) {
	ok := store.Run{End: runPtr(runAt(1)), Status: "ok"}
	assert.True(t, runMatches(ok, ""))
	assert.True(t, runMatches(ok, "ok"))
	assert.False(t, runMatches(ok, "failed"))
	assert.False(t, runMatches(ok, "running"))
	assert.True(t, runMatches(store.Run{}, "running"))
	assert.False(t, runMatches(store.Run{}, "ok"), "a run that goes on has no status")
	assert.Equal(t, "–", runCountsText(runOf(0, "")))
	assert.Equal(t, "1 ok · 1 running · 1 queued · 1 failed · 1 stopped · 1 skipped",
		runCountsText(runOf(0, "", runSp("step", "s", runStartedAt(0), runKids(
			runSp("target", "a", runEndedAt(0, 1, "ok")), runSp("target", "b", runStartedAt(0)), runSp("target", "c"),
			runSp("target", "d", runEndedAt(0, 1, "failed")), runSp("target", "e", runEndedAt(0, 1, "canceled")),
			runSp("target", "f", runEndedAt(0, 1, "skipped")))))))
}

// runSearchStore fills the store with runs on clusters of two realms: a
// task of sind ui on dev of sind, terminal runs on dev of both realms and
// on lab of lab, and one without a cluster.
func runSearchStore(ts *testServer) {
	ts.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	ts.st.SetClusters("lab", []cluster.Summary{{Name: "dev"}, {Name: "lab"}})
	end := runPtr(runAt(5))
	run := func(id, title, node, status string) store.Run {
		r := store.Run{ID: id, Title: title, Trace: "tr-" + id, Start: runAt(0), Root: store.Span{Node: node}}
		if status != "" {
			r.End, r.Status = end, status
		}
		return r
	}
	power := run("r-dev", "power reboot dev", "dev", "ok")
	power.Root.Children = []store.Span{{Kind: activity.KindStep, Name: "stopping", Children: []store.Span{
		{Kind: activity.KindTarget, Node: "worker-0", Role: "worker"}, {Kind: activity.KindTarget, Node: "worker-1", Role: "worker"},
	}}}
	ts.st.SetRuns([]store.Run{
		run("r-ui", "create worker dev", "dev", ""),
		power,
		run("r-lab", "create cluster lab", "lab", "failed"),
		run("r-none", "doctor", "", "ok"),
	})
	ts.st.AddTask(store.Task{ID: "t", Action: "create-worker", Realm: "sind", Cluster: "dev", Trace: "tr-r-ui",
		Display: "sind create worker dev --count 3 --cpus 4", Argv: []string{"sind", "create", "worker", "dev", "--count", "3", "--cpus", "4"},
		Start: runAt(0), Status: store.TaskRunning})
}

func TestRunActivitySearch(t *testing.T) {
	ts := runTestServer(t, runNow)
	runSearchStore(ts)

	body := ts.get("/activity").Body.String()
	for _, want := range []string{
		`<form class="runfind" method="get" action="/activity" role="search">`,
		`<input type="search" name="q" value="" placeholder="Filter by command, cluster, flag or run ID" aria-label="Filter runs"`,
		`<a class="mono" href="/runs/r-ui">create worker dev <span class="flag">--count 3</span> <span class="flag">--cpus 4</span></a> <span class="chip" title="realm">sind</span>`,
		`<a class="mono" href="/runs/r-lab">create cluster lab</a> <span class="chip" title="realm">lab</span>`,
		`<div class="runchips-g" role="group" aria-label="Filter by realm"><span class="faint">Realm</span>`,
		`<a class="fchip" href="/activity?realm=lab" title="realm lab">lab<span class="n">2</span></a>`,
		`<a class="fchip" href="/activity?realm=sind" title="realm sind">sind<span class="n">2</span></a>`,
		`<a class="fchip" href="/activity?cluster=dev" title="cluster dev">dev<span class="n">2</span></a>`,
		`<a class="fchip" href="/activity?cluster=lab" title="cluster lab in realm lab">lab<span class="n">1</span></a>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "Clear filter")
	assert.NotContains(t, body, `href="/runs/r-dev">power reboot dev</a> <span class="chip" title="realm">`, "dev is in two realms")

	// A text, a realm and a cluster; the tabs keep them.
	body = ts.get("/activity?q=CPUS+4").Body.String()
	assert.Equal(t, 1, strings.Count(body, `<a class="mono" href="/runs/`))
	assert.Contains(t, body, `value="CPUS 4"`)
	assert.Contains(t, body, `<a class="tab" href="/activity?q=CPUS&#43;4&amp;status=running">Running<span class="n">1</span></a>`)
	assert.Contains(t, body, `<a class="runchips-clear" href="/activity">Clear filter</a>`)

	body = ts.get("/activity?realm=sind&status=running").Body.String()
	assert.Contains(t, body, `<input type="hidden" name="status" value="running"><input type="hidden" name="realm" value="sind">`)
	assert.Contains(t, body, `<a class="fchip on" href="/activity?status=running" title="realm sind" aria-current="true"><span aria-hidden="true">✓ </span>sind<span class="n">1</span></a>`)
	assert.Contains(t, body, `<a class="fchip" href="/activity?cluster=dev&amp;realm=sind&amp;status=running" title="cluster dev in realm sind">dev<span class="n">1</span></a>`)
	assert.Contains(t, body, `<a class="runchips-clear" href="/activity?status=running">Clear filter</a>`)
	assert.Equal(t, 1, strings.Count(body, `<a class="mono" href="/runs/`))

	body = ts.get("/activity?realm=sind&cluster=dev").Body.String()
	assert.Contains(t, body, `<input type="hidden" name="cluster" value="dev">`)
	assert.Contains(t, body, `href="/runs/r-ui"`)
	assert.Contains(t, body, `href="/runs/r-dev"`, "a terminal run on a cluster of that name")
	assert.NotContains(t, body, `href="/runs/r-lab"`)

	// The names of the nodes find a run whose title folds them.
	body = ts.get("/activity?q=worker-1").Body.String()
	assert.Contains(t, body, `href="/runs/r-dev"`)
	assert.Equal(t, 1, strings.Count(body, `<a class="mono" href="/runs/`))

	body = ts.get("/activity?q=nothing").Body.String()
	assert.Contains(t, body, `No run matches this filter. <a href="/activity">Clear filter</a>`)
}

func TestRunEntryOf(t *testing.T) {
	snap := store.Snapshot{Clusters: map[string][]cluster.Summary{"a": {{Name: "x"}}, "b": {{Name: "x"}, {Name: "y"}}}}
	e := runEntryOf(snap, store.Run{ID: "r1", Root: store.Span{Node: "y"}})
	assert.Equal(t, "r1", e.title, "a run without a title is named by its id")
	assert.Equal(t, []string{"b"}, e.realms)
	assert.Equal(t, "b", e.realm)
	e = runEntryOf(snap, store.Run{ID: "r2", Title: "create cluster x", Root: store.Span{Node: "x"}})
	assert.Equal(t, []string{"a", "b"}, e.realms)
	assert.Empty(t, e.realm)
	assert.True(t, e.matches(runQuery{Realm: "a", Cluster: "x", Text: "Create x"}))
	assert.False(t, e.matches(runQuery{Realm: "c"}))
	assert.False(t, e.matches(runQuery{Cluster: "y"}))
	assert.False(t, e.matches(runQuery{Text: "create z"}))
	e = runEntryOf(snap, store.Run{ID: "r3", Title: "gone", Root: store.Span{Node: "z"}})
	assert.Nil(t, e.realms, "no realm has the cluster")
	assert.Equal(t, "/activity", runQuery{}.href())
}

func TestRunListRowWords(t *testing.T) {
	// A flag keeps its value; a flag with = or one before another flag
	// stands alone.
	r := runListRow{Title: "create cluster --pull --wait 5m --memory=8g x"}
	assert.Equal(t, []runWord{
		{"create", false}, {"cluster", false}, {"--pull", true}, {"--wait 5m", true}, {"--memory=8g", true}, {"x", false},
	}, r.Words())
}
