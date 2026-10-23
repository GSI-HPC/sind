// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/help"
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

// part asks for target as help.js does, with the header of htmx.
func (ts *testServer) part(target string) *httptest.ResponseRecorder {
	r := request("GET", target)
	r.Header.Set("HX-Request", "true")
	r.AddCookie(ts.cookie)
	return ts.do(r)
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
		"/help": {`data-crumb="This page"`, "<h2>Overview</h2>", `href="/help?topic=word/realm" data-help-topic="word/realm"`, `data-help-topic="howto/first-cluster"`,
			`data-help-topic="keys"`, `data-help-topic="words"`, `data-help-topic="index"`, `<dt class="k1"><kbd>?</kbd></dt>`, `<kbd>Ctrl</kbd>+<kbd>K</kbd>`,
			`class="kbd-only helpkeys-short"`, `class="hint touch-only"`, `class="hint k1-off"`,
			"<kbd>Ctrl</kbd>+<kbd>Enter</kbd></dt><dd>Run the command of the dialog", "A dialog with changes asks first."},
		"/help?topic=%2F":                      {"<h2>Overview</h2>"},
		"/help?topic=%2Fclusters%2Fsind%2Fdev": {"<h2>Cluster</h2>", `data-help-topic="word/node-roles"`, `data-help-topic="howto/job-output"`, `data-help-topic="howto/clean-up"`},
		"/help?topic=%2Fno%2Fsuch%2Fpage":      {"<h2>Overview</h2>"},
		"/help?topic=word/realm":               {`data-crumb="Word"`, "<h2>Realm</h2>", "sind get realms", `data-help-copy="sind get realms"`, "sind help realm", "gsi-hpc.github.io/sind/configuration/realms/?via=ui", `rel="noopener noreferrer"`, `data-help-topic="words">All words`},
		"/help?topic=word/lock":                {"<h2>Realm lock</h2>"},
		"/help?topic=word/slurmd":              {"<h2>slurmd</h2>", "sind logs worker-0 slurmd"},
		"/help?topic=action/power":             {`data-crumb="Action"`, "<h2>Power</h2>", "sind power reboot NODES", "Use the opposite verb."},
		"/help?topic=action/delete-realm":      {`class="callout bad"`, "Cannot be undone."},
		"/help?topic=howto/slow-create":        {`data-crumb="How do I…"`, `<ol class="steps">`, "sind logs controller slurmctld"},
		"/help?topic=howto/realm-busy":         {"<h2>A command waits for the realm lock</h2>"},
		"/help?topic=howto/job-pending":        {"<h2>My job does not start</h2>", "sind exec -- squeue"},
		"/help?q=lock":                         {`data-help-topic="word/lock"`, "<small>word</small>"},
		"/help?q=add+workers":                  {`data-help-topic="howto/add-workers"`, "<small>how to</small>", `data-help-topic="action/create-worker"`},
		"/help?q=stuck":                        {`data-help-topic="howto/job-pending"`},
		"/help?q=zzzzqq":                       {"Nothing in help matches"},
	} {
		w := ts.part(target)
		require.Equal(t, http.StatusOK, w.Code, target)
		body := w.Body.String()
		for _, s := range want {
			assert.Contains(t, body, s, target)
		}
		assert.NotContains(t, body, "<html", target)
	}
}

func TestHelpTopicsOfTheUI(t *testing.T) {
	ts := helpServer(t)
	h := help.Default()

	w := ts.part("/help?topic=keys")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, s := range []string{
		`data-crumb="Keys"`, `<input type="checkbox" role="switch" data-help-keys checked>`, "<h3>In the jump box</h3>",
		`<dt class="k1"><kbd>g</kbd> <span class="kj">then</span> <kbd>m</kbd></dt><dd class="k1">Go to Mesh and DNS</dd>`,
		`<kbd>↑</kbd> <span class="kj">or</span> <kbd>↓</kbd>`, "<dt><kbd>Esc</kbd></dt>", "web-ui/?via=ui#keys",
	} {
		assert.Contains(t, body, s)
	}
	for _, c := range helpChords {
		assert.Contains(t, body, "Go to "+c.Label)
	}

	w = ts.part("/help?topic=words")
	require.Equal(t, http.StatusOK, w.Code)
	body = w.Body.String()
	assert.Contains(t, body, `<dl class="gloss">`)
	last := -1
	for _, word := range helpGlossary(h) {
		i := strings.Index(body, `data-help-topic="word/`+word.ID+`"`)
		require.GreaterOrEqual(t, i, 0, word.ID)
		assert.Greater(t, i, last, "%s is out of order", word.ID)
		last = i
	}
	assert.Len(t, helpGlossary(h), len(h.Words()))

	w = ts.part("/help?topic=index")
	require.Equal(t, http.StatusOK, w.Code)
	body = w.Body.String()
	for _, x := range h.Howtos() {
		assert.Contains(t, body, `data-help-topic="howto/`+x.ID+`"`)
	}
	assert.Contains(t, body, `<li class="kbd-only"><a class="link" href="/help?topic=keys"`)
}

// TestHelpChordsMatchTheScript checks that help.js goes where the help
// says that the g keys go.
func TestHelpChordsMatchTheScript(t *testing.T) {
	data, err := fs.ReadFile(webFS(), "static/js/help.js")
	require.NoError(t, err)
	m := regexp.MustCompile(`const goto = \{([^}]*)\}`).FindSubmatch(data)
	require.NotNil(t, m)
	pairs := regexp.MustCompile(`(\w): '([^']*)'`).FindAllSubmatch(m[1], -1)
	got := map[string]string{}
	for _, p := range pairs {
		got[string(p[1])] = string(p[2])
	}
	want := map[string]string{}
	for _, c := range helpChords {
		want[c.Key] = c.Path
	}
	assert.Equal(t, want, got)
	for _, it := range navItems {
		assert.Contains(t, got, firstChordKey(it.Href), "rail entry %s has no g key", it.Label)
	}
}

// firstChordKey returns the key of the chord that goes to path.
func firstChordKey(path string) string {
	for _, c := range helpChords {
		if c.Path == path {
			return c.Key
		}
	}
	return "none"
}

func TestHelpPage(t *testing.T) {
	ts := helpServer(t)
	h := help.Default()

	w := ts.get("/help")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, s := range []string{"<html", "<title>Help · sind ui</title>", "<h1>Help</h1>", `<article class="panel helpdoc">`,
		`data-crumb="All help"`, `<form class="helpsearch" action="/help" method="get" role="search">`, `href="/help?topic=words"`} {
		assert.Contains(t, body, s)
	}
	assert.NotContains(t, body, `<div class="crumbs">`)
	for _, x := range h.Howtos() {
		assert.Contains(t, body, x.Title)
	}

	w = ts.get("/help?topic=word/realm")
	require.Equal(t, http.StatusOK, w.Code)
	body = w.Body.String()
	assert.Contains(t, body, "<html")
	assert.Contains(t, body, `<div class="crumbs"><a href="/help">Help</a> / Word</div>`)
	assert.Contains(t, body, "<h2>Realm</h2>")

	w = ts.get("/help?q=sbatch")
	require.Equal(t, http.StatusOK, w.Code)
	body = w.Body.String()
	assert.Contains(t, body, `value="sbatch"`)
	assert.Contains(t, body, `href="/help?topic=howto%2ffirst-cluster"`)

	w = ts.get("/help?topic=word/nope")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHelpPaneEscapesTheQuery(t *testing.T) {
	ts := helpServer(t)
	w := ts.part("/help?q=" + url.QueryEscape(`<script>alert(1)</script>`))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "<script>")
	assert.Contains(t, w.Body.String(), "&lt;script&gt;")
}

func TestHelpCard(t *testing.T) {
	ts := helpServer(t)
	for target, want := range map[string][]string{
		"/help?topic=word/mesh&card=1":             {`<span class="k">Word</span>`, "<b>Mesh</b>", "$ sind get mesh", `<span class="faint more kbd-only k1-on"><kbd>?</kbd> for more</span>`},
		"/help?topic=word/lock&card=1":             {"<b>Realm lock</b>"},
		"/help?topic=action/delete-cluster&card=1": {`<span class="k bad">Action · Cannot be undone</span>`, "$ sind delete cluster NAME"},
		"/help?topic=action/create-worker&card=1":  {`<span class="k">Action · Delete the workers</span>`},
	} {
		w := ts.part(target)
		require.Equal(t, http.StatusOK, w.Code, target)
		for _, s := range want {
			assert.Contains(t, w.Body.String(), s, target)
		}
	}
	// A word without a command has no command line.
	w := ts.part("/help?topic=word/lock&card=1")
	assert.NotContains(t, w.Body.String(), "$ ")
}

func TestHelpNotFound(t *testing.T) {
	ts := helpServer(t)
	for _, target := range []string{
		"/help?topic=word/nope", "/help?topic=action/nope", "/help?topic=howto/nope",
		"/help?topic=page/overview", "/help?topic=nonsense",
		"/help?topic=howto/slow-create&card=1", "/help?topic=%2F&card=1", "/help?topic=keys&card=1",
	} {
		w := ts.part(target)
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
		`role="dialog" aria-modal="false" aria-labelledby="help-crumb"`, `id="help-status" role="status"`,
		`data-palette-open`, `aria-keyshortcuts="/ Control+K"`, `<kbd class="kbd-only k1-on">/</kbd>`,
		`<template id="help-jump-shell">`, `role="dialog" aria-modal="true" aria-label="Jump to"`,
		`id="jump-q"`, `aria-autocomplete="list"`, `aria-controls="jump-list"`, `id="jump-res"`, `id="jump-status" role="status"`,
		`/static/`,
	} {
		assert.Contains(t, body, s)
	}
	// The body of the pane is no live region: a screen reader would read
	// the whole entry again on each key.
	assert.NotContains(t, body, `aria-live="polite"></div>`)
	assert.NotRegexp(t, `id="help-body"[^>]*aria-live`, body)
	assert.Regexp(t, `src="/static/[0-9a-f]+/js/help\.js"`, body)
	assert.Regexp(t, `href="/static/[0-9a-f]+/css/help\.css"`, body)
	// The CSP allows no inline script or style, so the markup holds none.
	assert.NotRegexp(t, `<script>|\sstyle="|\sonclick=`, body)
}

func TestHelpKindLabel(t *testing.T) {
	assert.Equal(t, "how to", helpKindLabel(help.KindHowto))
	assert.Equal(t, "word", helpKindLabel(help.KindWord))
}
