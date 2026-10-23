// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
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
		`<a class="tab on" href="/activity">All<span class="n">4</span></a>`,
		`<a class="tab" href="/activity?status=running">Running<span class="n">1</span></a>`,
		`<a class="tab" href="/activity?status=ok">OK<span class="n">1</span></a>`,
		`<a class="tab" href="/activity?status=failed">Failed<span class="n">1</span></a>`,
		`<a class="tab" href="/activity?status=canceled">Canceled<span class="n">1</span></a>`,
		`<a class="mono" href="/runs/r-mid">create cluster dev</a>`,
		`<span class="pill run">running</span>`,
		`<span class="pill bad">failed</span>`,
		"3 ok · 2 failed · 1 canceled",
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
		assert.Contains(t, body, `<a class="tab on" href="/activity?status=`+filter+`">`, filter)
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
	assert.Equal(t, NavCount{}, runNavCount(store.Snapshot{}))
	over := store.Run{End: runPtr(runAt(1))}
	assert.Equal(t, NavCount{Text: "2"}, runNavCount(store.Snapshot{Runs: []store.Run{over, over}}))
	assert.Equal(t, NavCount{Text: "1 running", Class: "run"}, runNavCount(store.Snapshot{Runs: []store.Run{over, {}}}))
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
	assert.Equal(t, "1 ok · 1 running · 1 queued · 1 failed · 1 canceled · 1 skipped",
		runCountsText(runOf(0, "", runSp("step", "s", runStartedAt(0), runKids(
			runSp("target", "a", runEndedAt(0, 1, "ok")), runSp("target", "b", runStartedAt(0)), runSp("target", "c"),
			runSp("target", "d", runEndedAt(0, 1, "failed")), runSp("target", "e", runEndedAt(0, 1, "canceled")),
			runSp("target", "f", runEndedAt(0, 1, "skipped")))))))
}
