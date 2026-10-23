// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/help"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpServer is a logged-in test server with the routes of the help part.
func helpServer(t *testing.T) *testServer {
	t.Helper()
	cfg, st := testConfig(t)
	helpRegister(cfg.Registry)
	return newTestServerFrom(t, cfg, st).login()
}

// TestHelpCoversTheUI checks that every page that the default registry
// holds has a help entry, and that every word and action that the templates
// mark exists in the help texts.
func TestHelpCoversTheUI(t *testing.T) {
	h := help.Default()
	for _, p := range defaultRegistry.snapshot().pages {
		path := strings.TrimPrefix(p.Pattern, "GET ")
		if path == "/{$}" {
			path = "/"
		}
		_, ok := h.PageFor(path)
		assert.True(t, ok, "page %s (%s) has no entry in internal/help/pages.yaml", p.Name, path)
	}

	wordRE := regexp.MustCompile(`data-word="([^"{}]+)"`)
	actionRE := regexp.MustCompile(`data-help="([^"{}]+)"`)
	err := fs.WalkDir(webFS(), "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(webFS(), p)
		if err != nil {
			return err
		}
		for _, m := range wordRE.FindAllStringSubmatch(string(data), -1) {
			_, ok := h.Word(m[1])
			assert.True(t, ok, "%s marks the word %q, which has no help", p, m[1])
		}
		for _, m := range actionRE.FindAllStringSubmatch(string(data), -1) {
			_, ok := h.Action(m[1])
			assert.True(t, ok, "%s marks the action %q, which has no help", p, m[1])
		}
		return nil
	})
	require.NoError(t, err)
}

func TestHelpPane(t *testing.T) {
	ts := helpServer(t)
	for target, want := range map[string][]string{
		"/help":                                {`data-crumb="This page"`, "<h2>Overview</h2>", `data-help-topic="word/realm"`, `data-help-topic="howto/first-cluster"`, "<kbd>?</kbd>", "<kbd>Ctrl</kbd> <kbd>Enter</kbd></dt><dd>Run the command of the dialog"},
		"/help?topic=%2F":                      {"<h2>Overview</h2>"},
		"/help?topic=%2Fclusters%2Fsind%2Fdev": {"<h2>Cluster</h2>", `data-help-topic="word/node-roles"`},
		"/help?topic=%2Fno%2Fsuch%2Fpage":      {"<h2>Overview</h2>"},
		"/help?topic=word/realm":               {`data-crumb="Word"`, "<h2>Realm</h2>", "sind get realms", `data-help-copy="sind get realms"`, "sind help realm", "gsi-hpc.github.io/sind/configuration/realms/?via=ui", `rel="noopener noreferrer"`},
		"/help?topic=word/lock":                {"<h2>Realm lock</h2>"},
		"/help?topic=action/power":             {`data-crumb="Action"`, "<h2>Power</h2>", "sind power reboot NODES", "Use the opposite verb."},
		"/help?topic=action/delete-realm":      {`class="callout bad"`, "Cannot be undone."},
		"/help?topic=howto/slow-create":        {`data-crumb="How do I…"`, `<ol class="steps">`, "sind logs controller slurmctld"},
		"/help?topic=howto/realm-busy":         {"<h2>A command waits for the realm lock</h2>"},
		"/help?q=lock":                         {`data-help-topic="word/lock"`, "<small>word</small>"},
		"/help?q=add+workers":                  {`data-help-topic="howto/add-workers"`, "<small>how to</small>", `data-help-topic="action/create-worker"`},
		"/help?q=zzzzqq":                       {"Nothing in help matches"},
	} {
		w := ts.get(target)
		require.Equal(t, http.StatusOK, w.Code, target)
		body := w.Body.String()
		for _, s := range want {
			assert.Contains(t, body, s, target)
		}
		assert.NotContains(t, body, "<html", target)
	}
}

func TestHelpPaneEscapesTheQuery(t *testing.T) {
	ts := helpServer(t)
	w := ts.get("/help?q=" + url.QueryEscape(`<script>alert(1)</script>`))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "<script>")
	assert.Contains(t, w.Body.String(), "&lt;script&gt;")
}

func TestHelpCard(t *testing.T) {
	ts := helpServer(t)
	for target, want := range map[string][]string{
		"/help?topic=word/mesh&card=1":             {`<span class="k">Word</span>`, "<b>Mesh</b>", "$ sind get mesh", "for more"},
		"/help?topic=word/lock&card=1":             {"<b>Realm lock</b>"},
		"/help?topic=action/delete-cluster&card=1": {`<span class="k bad">Action · Cannot be undone</span>`, "$ sind delete cluster NAME"},
		"/help?topic=action/create-worker&card=1":  {`<span class="k">Action · Delete the workers</span>`},
	} {
		w := ts.get(target)
		require.Equal(t, http.StatusOK, w.Code, target)
		for _, s := range want {
			assert.Contains(t, w.Body.String(), s, target)
		}
	}
	// A word without a command has no command line.
	w := ts.get("/help?topic=word/lock&card=1")
	assert.NotContains(t, w.Body.String(), "$ ")
}

func TestHelpNotFound(t *testing.T) {
	ts := helpServer(t)
	for _, target := range []string{
		"/help?topic=word/nope", "/help?topic=action/nope", "/help?topic=howto/nope",
		"/help?topic=page/overview", "/help?topic=nonsense",
		"/help?topic=howto/slow-create&card=1", "/help?topic=%2F&card=1",
	} {
		w := ts.get(target)
		assert.Equal(t, http.StatusNotFound, w.Code, target)
		assert.NotContains(t, w.Body.String(), "<html", target)
	}
}

func TestHelpBadRequest(t *testing.T) {
	ts := helpServer(t)
	for _, target := range []string{"/help?topic=" + strings.Repeat("a", 300), "/jump?q=" + strings.Repeat("a", 300)} {
		w := ts.get(target)
		assert.Equal(t, http.StatusBadRequest, w.Code, target[:12])
	}
}

func TestHelpMarkupInTheShell(t *testing.T) {
	ts := helpServer(t)
	w := ts.get("/demo/x")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, s := range []string{
		`data-help-toggle`, `<template id="help-shell">`, `id="help-q"`, `id="help-body"`,
		`data-palette-open`, `<template id="help-jump-shell">`, `id="jump-q"`, `id="jump-list"`,
		`/static/`,
	} {
		assert.Contains(t, body, s)
	}
	assert.Regexp(t, `src="/static/[0-9a-f]+/js/help\.js"`, body)
	assert.Regexp(t, `href="/static/[0-9a-f]+/css/help\.css"`, body)
	// The CSP allows no inline script or style, so the markup holds none.
	assert.NotRegexp(t, `<script>|\sstyle="|\sonclick=`, body)
}

func fixtureStore(st *store.Store) {
	st.SetClusters("sind", []cluster.Summary{{Name: "dev"}, {Name: "hpc"}})
	st.SetClusters("ci", []cluster.Summary{{Name: "dev"}})
	st.SetNodes("sind", []cluster.NodeSummary{
		{Cluster: "dev", FQDN: "worker-0.dev.sind", Container: "sind-dev-worker-0"},
		{Cluster: "dev", FQDN: "controller.dev.sind", Container: "sind-dev-controller"},
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
	assert.Equal(t, jumpMax, strings.Count(body, `class="pi"`))
	assert.Contains(t, body, `href="/clusters">Clusters<small>page</small>`)
	assert.Contains(t, body, `href="/">Overview<small>page</small>`)
	assert.NotContains(t, body, "{realm}")

	for target, want := range map[string][]string{
		"/jump?q=hpc":         {`href="/clusters/sind/hpc">hpc<small>cluster · sind</small>`},
		"/jump?q=dev":         {`href="/clusters/ci/dev">dev<small>cluster · ci</small>`, `href="/nodes/sind/dev/worker-0">worker-0.dev<small>node · sind</small>`, `href="/runs/x">create cluster dev<small>run · running</small>`},
		"/jump?q=controller":  {`href="/nodes/sind/dev/controller">controller.dev`, `data-help-topic="word/controller"`},
		"/jump?q=new+cluster": {`href="/dialogs/create-cluster">New cluster<small>action</small>`},
		"/jump?q=power":       {`href="/dialogs/power">Power<small>action</small>`, `href="/runs/ra">power reboot worker-0<small>run · ok</small>`},
		"/jump?q=rollback":    {`href="/help?topic=word%2frollback" data-help-topic="word/rollback">Rollback<small>word</small>`},
		"/jump?q=zzzzqq":      {`class="empty">Nothing matches. Start with ? to search help only.`},
	} {
		w := ts.get(target)
		require.Equal(t, http.StatusOK, w.Code, target)
		for _, s := range want {
			assert.Contains(t, w.Body.String(), s, target)
		}
	}
}

func TestJumpOrder(t *testing.T) {
	d := jump(help.Default(), store.Snapshot{}, "node")
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
}

func TestJumpHelpOnly(t *testing.T) {
	ts := helpServer(t)
	fixtureStore(ts.st)

	w := ts.get("/jump?q=" + url.QueryEscape("?"))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, `href="/clusters`)
	assert.NotContains(t, body, `href="/runs`)
	assert.Contains(t, body, `data-help-topic="word/realm"`)
	assert.Contains(t, body, `data-help-topic="howto/first-cluster"`)

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

func TestHelpKindLabel(t *testing.T) {
	assert.Equal(t, "how to", helpKindLabel(help.KindHowto))
	assert.Equal(t, "word", helpKindLabel(help.KindWord))
}
