// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixtureStore(st *store.Store) {
	st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "ci", Clusters: 1}})
	st.SetClusters("sind", []cluster.Summary{{Name: "dev", Workers: 2}, {Name: "hpc", Workers: 1}})
	st.SetClusters("ci", []cluster.Summary{{Name: "dev", Workers: 1}})
	st.SetNodes("sind", []cluster.NodeSummary{
		{Cluster: "dev", FQDN: "worker-0.dev.sind", Container: "sind-dev-worker-0", Role: config.RoleWorker},
		{Cluster: "dev", FQDN: "controller.dev.sind", Container: "sind-dev-controller", Role: config.RoleController},
	})
	runs := []store.Run{{ID: "x", Title: "create cluster dev"}}
	for i := 0; i < 10; i++ {
		runs = append(runs, store.Run{ID: "r" + string(rune('a'+i)), Title: "power reboot worker-" + string(rune('0'+i)), Status: store.StatusOK})
	}
	st.SetRuns(runs)
}

func TestJump(t *testing.T) {
	ts := helpServer(t)
	fixtureStore(ts.st)

	w := ts.get("/jump")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, "<html")
	// Clusters come first, then the common actions, then every page; no
	// action that cannot be undone sits in the list before the user types.
	pages := 0
	for _, p := range help.Default().Pages() {
		if !strings.Contains(p.Route, "{") {
			pages++
		}
	}
	assert.Equal(t, 3+3+pages, strings.Count(body, `class="pi"`))
	assert.Contains(t, body, `id="jump-o0" tabindex="-1" aria-selected="false" href="/clusters/ci/dev">dev<small>cluster · ci</small>`)
	assert.Contains(t, body, `id="jump-o3" tabindex="-1" aria-selected="false" href="/dialogs/create-cluster" data-jump-dialog>New cluster<small>action</small>`)
	assert.Contains(t, body, `id="jump-o4" tabindex="-1" aria-selected="false" data-jump-step="create-worker">Add workers…`)
	assert.Contains(t, body, `id="jump-o5" tabindex="-1" aria-selected="false" data-jump-step="power">Power…`)
	assert.Contains(t, body, `href="/clusters">Clusters<small>page</small>`)
	assert.Contains(t, body, `href="/">Overview<small>page</small>`)
	assert.Contains(t, body, `href="/doctor">`)
	assert.Contains(t, body, `role="listbox" aria-label="Results"`)
	assert.Contains(t, body, `<div class="pf">Type to find more: nodes, runs, words and how-tos.</div>`)
	assert.Contains(t, body, `<kbd>Enter</kbd> open`)
	assert.NotContains(t, body, "Delete")
	assert.NotContains(t, body, "{realm}")

	// Many clusters leave room for the actions and the pages.
	var many []cluster.Summary
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5", "a6"} {
		many = append(many, cluster.Summary{Name: n})
	}
	ts.st.SetClusters("ci", many)
	body = ts.get("/jump").Body.String()
	assert.Equal(t, jumpFirstClusters+3+pages, strings.Count(body, `class="pi"`))
	assert.NotContains(t, body, `href="/clusters/ci/a6"`)
	assert.NotContains(t, body, `href="/clusters/sind/dev"`)
	assert.Contains(t, body, `data-jump-step="power">Power…`)
	assert.Contains(t, body, `<div class="pf">Type to find 3 more clusters, and nodes, runs, words and how-tos.</div>`)
	ts.st.SetClusters("ci", many[:4])
	body = ts.get("/jump").Body.String()
	assert.Contains(t, body, `<div class="pf">Type to find 1 more cluster, and nodes, runs, words and how-tos.</div>`)
	fixtureStore(ts.st)

	for target, want := range map[string][]string{
		"/jump?q=hpc":         {`href="/clusters/sind/hpc">hpc<small>cluster · sind</small>`, `data-jump-say="1 result."`},
		"/jump?q=dev":         {`href="/clusters/ci/dev">dev<small>cluster · ci</small>`, `href="/nodes/sind/dev/worker-0">worker-0.dev<small>node · dev · sind</small>`, `href="/runs/x">create cluster dev<small>run · running</small>`},
		"/jump?q=controller":  {`href="/nodes/sind/dev/controller">controller.dev`, `data-help-topic="word/controller"`},
		"/jump?q=new+cluster": {`href="/dialogs/create-cluster" data-jump-dialog>New cluster<small>action</small>`},
		"/jump?q=add+workers": {`data-jump-step="create-worker">Add workers…<small>action · pick a cluster</small></button>`},
		"/jump?q=power":       {`data-jump-step="power">Power…`, `href="/runs/ra">power reboot worker-0<small>run · ok</small>`},
		"/jump?q=delete":      {`data-jump-step="delete-cluster">Delete cluster…`, `data-jump-step="delete-realm">Delete realm…<small>action · pick a realm</small>`},
		"/jump?q=rollback":    {`href="/help?topic=word%2frollback" data-help-topic="word/rollback">Rollback<small>word</small>`},
		"/jump?q=sbatch":      {`data-help-topic="howto/first-cluster"`},
		"/jump?q=clean":       {`data-help-topic="howto/clean-up"`},
		"/jump?q=stuck":       {`data-help-topic="howto/job-pending"`},
		"/jump?q=dns":         {`href="/mesh">Mesh and DNS<small>page</small>`},
		"/jump?q=keys":        {`data-help-topic="keys">Keys<small>help</small>`, `data-jump-cmd="keys">Single-key shortcuts on or off<small>setting</small></button>`},
		"/jump?q=theme":       {`data-jump-cmd="theme">Light or dark<small>setting</small>`},
		"/jump?q=help":        {`href="/help">Help<small>page</small>`, `data-jump-cmd="help">Help for this page`},
		"/jump?q=sind+dev":    {`href="/clusters/sind/dev">dev<small>cluster · sind</small>`},
		"/jump?q=zzzzqq":      {`class="empty">Nothing matches. Try a cluster, a node or a word such as sbatch, or start with ? to search help only.`, `data-jump-say="Nothing matches."`},
	} {
		w := ts.get(target)
		require.Equal(t, http.StatusOK, w.Code, target)
		for _, s := range want {
			assert.Contains(t, w.Body.String(), s, target)
		}
	}

	// A query with more matches than the list holds says so.
	w = ts.get("/jump?q=r")
	body = w.Body.String()
	assert.Equal(t, jumpMax, strings.Count(body, `class="pi"`))
	assert.Regexp(t, `<div class="pf">14 of \d+\. Type more to narrow the list\.</div>`, body)
	assert.Regexp(t, `data-jump-say="14 of \d+ results\. Type more to narrow the list\."`, body)
}

// TestJumpRanksClustersBeforeNodes checks the order of "dev": the
// clusters, then the nodes of dev, then the runs.
func TestJumpRanksClustersBeforeNodes(t *testing.T) {
	ts := helpServer(t)
	fixtureStore(ts.st)
	d := jump(help.Default(), ts.st.Snapshot(), jumpContext{}, "dev")
	var kinds []string
	for _, it := range d.Items[:5] {
		kind, _, _ := strings.Cut(it.Kind, " ")
		kinds = append(kinds, kind)
	}
	assert.Equal(t, []string{"cluster", "cluster", "node", "node", "run"}, kinds)
}

func TestJumpOrder(t *testing.T) {
	d := jump(help.Default(), store.Snapshot{}, jumpContext{}, "node")
	require.NotEmpty(t, d.Items)
	// A label that starts with the query comes before one that holds it.
	starts, holds := -1, -1
	for i, it := range d.Items {
		l := strings.ToLower(it.Label)
		if strings.HasPrefix(l, "node") && starts < 0 {
			starts = i
		}
		if !strings.HasPrefix(l, "node") && strings.Contains(l, "node") && holds < 0 {
			holds = i
		}
	}
	assert.Equal(t, 0, starts)
	if holds >= 0 {
		assert.Less(t, starts, holds)
	}
	// The label that is the query comes first.
	d = jump(help.Default(), store.Snapshot{}, jumpContext{}, "doctor")
	require.NotEmpty(t, d.Items)
	assert.Equal(t, "/doctor", d.Items[0].Href)
}

func TestJumpStep(t *testing.T) {
	ts := helpServer(t)
	fixtureStore(ts.st)

	w := ts.get("/jump?step=create-worker&from=" + url.QueryEscape("/clusters/sind/hpc?tab=nodes"))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `role="listbox" aria-label="Add workers to which cluster?" data-jump-say="Add workers to which cluster? 3 results."`)
	assert.Contains(t, body, `id="jump-o0" tabindex="-1" aria-selected="false" href="/dialogs/create-worker?cluster=hpc&amp;realm=sind" data-jump-dialog><span class="mono">hpc</span><small>sind · 1 worker · this page</small>`)
	assert.Contains(t, body, `<span class="mono">dev</span><small>ci · 1 worker</small>`)
	assert.Contains(t, body, `<span class="mono">dev</span><small>sind · 2 workers</small>`)
	assert.Contains(t, body, `<kbd>Enter</kbd> pick`)
	assert.NotContains(t, body, `<div class="pf">Type`)

	w = ts.get("/jump?step=power&q=ci")
	body = w.Body.String()
	assert.Equal(t, 1, strings.Count(body, `class="pi"`))
	assert.Contains(t, body, `href="/dialogs/power?cluster=dev&amp;realm=ci"`)

	w = ts.get("/jump?step=delete-realm&from=" + url.QueryEscape("/mesh?realm=ci"))
	body = w.Body.String()
	assert.Contains(t, body, `aria-label="Delete which realm?"`)
	assert.Contains(t, body, `id="jump-o0" tabindex="-1" aria-selected="false" href="/dialogs/delete-realm?realm=ci" data-jump-dialog><span class="mono">ci</span><small>realm · 1 cluster · this page</small>`)
	assert.Contains(t, body, `<span class="mono">sind</span><small>realm · 2 clusters</small>`)

	w = ts.get("/jump?step=delete-realm&q=zz")
	assert.Contains(t, w.Body.String(), `class="empty">No realm matches.`)

	ts.st.SetClusters("sind", nil)
	ts.st.SetClusters("ci", nil)
	w = ts.get("/jump?step=create-worker")
	assert.Contains(t, w.Body.String(), `class="empty">There is no cluster yet.`)

	for _, target := range []string{"/jump?step=nope", "/jump?step=create-cluster"} {
		w = ts.get(target)
		assert.Equal(t, http.StatusNotFound, w.Code, target)
	}
}

func TestJumpAction(t *testing.T) {
	h := help.Default()
	act := func(id string) help.Action {
		a, ok := h.Action(id)
		require.True(t, ok, id)
		return a
	}
	one := store.Snapshot{
		Realms:   []cluster.RealmSummary{{Name: "sind", Clusters: 1}},
		Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev"}}},
	}
	two := store.Snapshot{
		Realms:   []cluster.RealmSummary{{Name: "ci", Clusters: 1}, {Name: "sind", Clusters: 1}},
		Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev"}}, "ci": {{Name: "pr"}}},
	}
	for name, tc := range map[string]struct {
		id       string
		sn       store.Snapshot
		ctx      jumpContext
		ok       bool
		href     string
		step     string
		quiet    bool
		kind     string
		clusters int
	}{
		"no cluster":            {id: "power", ok: false},
		"one cluster":           {id: "create-worker", sn: one, clusters: 1, ok: true, href: "/dialogs/create-worker?cluster=dev&realm=sind", kind: "action · dev"},
		"clusters to pick":      {id: "delete-cluster", sn: two, clusters: 2, ok: true, step: "delete-cluster", quiet: true, kind: "action · pick a cluster"},
		"new cluster":           {id: "create-cluster", ok: true, href: "/dialogs/create-cluster", kind: "action"},
		"new cluster in realm":  {id: "create-cluster", ctx: jumpContext{Realm: "ci"}, ok: true, href: "/dialogs/create-cluster?realm=ci", kind: "action"},
		"no realm":              {id: "connect", ok: false},
		"the only realm":        {id: "connect", sn: one, clusters: 1, ok: true, href: "/dialogs/connect?realm=sind", quiet: true, kind: "action · realm sind"},
		"the realm of the page": {id: "delete-realm", sn: two, clusters: 2, ctx: jumpContext{Realm: "ci"}, ok: true, href: "/dialogs/delete-realm?realm=ci", quiet: true, kind: "action · realm ci"},
		"realms to pick":        {id: "disconnect", sn: two, clusters: 2, ok: true, step: "disconnect", quiet: true, kind: "action · pick a realm"},
	} {
		t.Run(name, func(t *testing.T) {
			it, ok := jumpAction(act(tc.id), tc.sn, tc.ctx, tc.clusters)
			require.Equal(t, tc.ok, ok)
			if !ok {
				return
			}
			assert.Equal(t, tc.href, it.Href)
			assert.Equal(t, tc.href != "", it.Dialog)
			assert.Equal(t, tc.step, it.Step)
			assert.Equal(t, tc.quiet, it.quiet)
			assert.Equal(t, tc.kind, it.Kind)
		})
	}
	// Every action that may ask has a question.
	for _, a := range h.Actions() {
		if a.ID != "create-cluster" {
			assert.Contains(t, jumpAsks, a.ID)
		}
	}
}

func TestJumpContextOf(t *testing.T) {
	withCookie := func(v string) *http.Request {
		r := httptest.NewRequest("GET", "/jump", nil)
		if v != "" {
			r.AddCookie(&http.Cookie{Name: realmCookie, Value: v})
		}
		return r
	}
	sn := store.Snapshot{
		Realms:   []cluster.RealmSummary{{Name: "sind", Clusters: 1}, {Name: "ci", Clusters: 1}},
		Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev"}}, "ci": {{Name: "pr"}}},
	}
	for name, tc := range map[string]struct {
		from, cookie string
		want         jumpContext
	}{
		"deleted realm":   {from: "/clusters", cookie: "gone", want: jumpContext{}},
		"deleted cluster": {from: "/clusters/sind/old", cookie: "ci", want: jumpContext{Realm: "sind"}},
		"cluster page":    {from: "/clusters/sind/dev?tab=nodes", want: jumpContext{"sind", "dev"}},
		"node page":       {from: "/nodes/ci/pr/worker-0", cookie: "sind", want: jumpContext{"ci", "pr"}},
		"realm of page":   {from: "/mesh?realm=ci", cookie: "sind", want: jumpContext{Realm: "ci"}},
		"realm cookie":    {from: "/clusters", cookie: "sind", want: jumpContext{Realm: "sind"}},
		"nothing":         {from: "/", want: jumpContext{}},
		"bad realm":       {from: "/clusters/BAD!/dev", want: jumpContext{}},
		"bad cluster":     {from: "/clusters/sind/BAD!", want: jumpContext{}},
		"bad cookie":      {from: "/", cookie: "BAD!", want: jumpContext{}},
		"bad address":     {from: "%zz", cookie: "sind", want: jumpContext{Realm: "sind"}},
		"list of realms":  {from: "/clusters/sind", want: jumpContext{}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, jumpContextOf(withCookie(tc.cookie), tc.from, sn))
		})
	}
}

func TestJumpSay(t *testing.T) {
	assert.Equal(t, "Nothing matches.", (&jumpData{}).Say())
	assert.Equal(t, "1 result.", (&jumpData{Query: "x", Total: 1, Items: make([]jumpItem, 1)}).Say())
	assert.Equal(t, "3 results.", (&jumpData{Query: "x", Total: 3, Items: make([]jumpItem, 3)}).Say())
	assert.Equal(t, "2 of 5 results. Type more to narrow the list.", (&jumpData{Query: "x", Total: 5, Items: make([]jumpItem, 2)}).Say())
	assert.Equal(t, "14 results. Type to find more.", (&jumpData{Total: 20, Items: make([]jumpItem, 14)}).Say())
	assert.Equal(t, "Delete which realm? 2 results.", (&jumpData{Step: "delete-realm", Ask: "Delete which realm?", Total: 2, Items: make([]jumpItem, 2)}).Say())
	assert.Equal(t, "", (&jumpData{Query: "x", Total: 3, Items: make([]jumpItem, 3)}).Foot())
	assert.Equal(t, "", (&jumpData{Step: "power", Total: 3, Items: make([]jumpItem, 3)}).Foot())
	assert.Equal(t, "cluster", (&jumpData{Step: "power"}).Noun())
	assert.Equal(t, "realm", (&jumpData{Step: "connect"}).Noun())
}

func TestJumpHelpOnly(t *testing.T) {
	ts := helpServer(t)
	fixtureStore(ts.st)

	w := ts.get("/jump?q=" + url.QueryEscape("?"))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, `href="/clusters`)
	assert.NotContains(t, body, `href="/runs`)
	assert.Contains(t, body, `data-help-topic="howto/first-cluster"`)
	assert.Contains(t, body, `data-help-topic="index"`)

	w = ts.get("/jump?q=" + url.QueryEscape("? delete"))
	body = w.Body.String()
	assert.Contains(t, body, `data-help-topic="action/delete-cluster"`)
	assert.NotContains(t, body, `href="/dialogs/`)

	w = ts.get("/jump?q=" + url.QueryEscape("?zzzzqq"))
	assert.Contains(t, w.Body.String(), "Nothing in help matches.")
}

func TestJumpEscapes(t *testing.T) {
	ts := helpServer(t)
	ts.st.SetClusters("sind", []cluster.Summary{{Name: `"><b>x`}})
	w := ts.get("/jump?q=x")
	assert.NotContains(t, w.Body.String(), "<b>x")
}
