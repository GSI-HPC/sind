// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellSkipLinkAndLiveRegions(t *testing.T) {
	ts := newTestServer(t).login()
	body := ts.get("/demo/x").Body.String()

	// The skip link is the first thing of the page that takes the focus,
	// and the content can take it.
	assert.Contains(t, body, "<body>\n<a class=\"skip\" href=\"#main\">Skip to content</a>\n<div class=\"app\"")
	assert.Contains(t, body, `<main class="main" id="main" tabindex="-1">`)
	// The banner of a lost connection and the line that tells screen
	// readers what a live update changed are there, and empty.
	assert.Contains(t, body, `<div class="conn" id="conn" role="status"></div>`)
	assert.Contains(t, body, `<div class="vh" id="say" role="status"></div>`)
	// The theme button is a toggle that says its state.
	assert.Contains(t, body, `data-theme-toggle aria-label="Dark theme" aria-pressed="false"`)
}

func TestRailIsListsWithLabels(t *testing.T) {
	ts := newTestServer(t).login()
	body := ts.get("/demo/x").Body.String()
	assert.NotContains(t, body, "<h6", "no headings before the page's h1")
	assert.Contains(t, body, `<span class="rgl" id="rail-overview">Overview</span>`+"\n"+`<ul class="rg rg-1" aria-labelledby="rail-overview">`)
	assert.Contains(t, body, `<ul class="rg rg-2" aria-labelledby="rail-realm">`, "two of the Realm entries are in a phone's bar")
	assert.Contains(t, body, `<li class="sec"><a class="nav" href="/mesh">`)
	assert.Contains(t, body, `<li><a class="nav" href="/doctor">`)
	// More opens the sheet of the entries that a phone's bar leaves out.
	assert.Contains(t, body, `<button type="button" class="nav more" data-rail-more aria-expanded="false" aria-controls="rail-sheet">`)
	assert.Contains(t, body, `<div class="rail-sheet" id="rail-sheet" hidden><ul aria-label="More pages"><li><a class="nav" href="/mesh">`)
	assert.Contains(t, body, `icons.svg#more"></use></svg>More</button>`, "More has no count when the sheet has none to show")
}

func TestRailMore(t *testing.T) {
	reg := NewRegistry()
	view := func(nav string) *View {
		return &View{Nav: nav, Asset: func(p string) string { return "/s/" + p }}
	}
	d := reg.rail(view("clusters"))
	assert.False(t, d.More.On)
	assert.Equal(t, "/s/icons.svg#more", d.More.Icon)
	assert.Equal(t, "/s/icons.svg#clusters", d.Groups[1].Items[0].Icon)
	assert.Equal(t, []int{1, 2, 1}, []int{d.Groups[0].Main, d.Groups[1].Main, d.Groups[2].Main})
	var sheet []string
	for _, e := range d.Sheet {
		sheet = append(sheet, e.Key)
	}
	assert.Equal(t, []string{"mesh", "monitoring", "topology", "activity"}, sheet)
	assert.True(t, reg.rail(view("topology")).More.On, "a page of the sheet lights More")

	// More shows the most urgent count of the sheet: a failure before work
	// that goes on, and a plain count never.
	reg.SetNavCount("monitoring", func(store.Snapshot, time.Time) NavCount { return NavCount{Text: "4"} })
	assert.Equal(t, NavCount{}, reg.rail(view("")).More.Count)
	reg.SetNavCount("activity", func(store.Snapshot, time.Time) NavCount { return NavCount{Text: "2 running", Class: "run"} })
	m := reg.rail(view("")).More
	assert.Equal(t, NavCount{Text: "▸", Class: "run"}, m.Count)
	assert.Equal(t, "Activity: 2 running", m.Note)
	reg.SetNavCount("mesh", func(store.Snapshot, time.Time) NavCount { return NavCount{Text: "1", Class: "bad"} })
	m = reg.rail(view("")).More
	assert.Equal(t, NavCount{Text: "!", Class: "bad"}, m.Count, "the failure of Mesh stays over the work of Activity")
	assert.Equal(t, "Mesh and DNS: 1", m.Note)
}

func TestRailMoreCountInThePage(t *testing.T) {
	cfg, st := testConfig(t)
	cfg.Registry.SetNavCount("activity", func(store.Snapshot, time.Time) NavCount { return NavCount{Text: "1 running", Class: "run"} })
	ts := newTestServerFrom(t, cfg, st).login()
	body := ts.get("/demo/x").Body.String()
	assert.Contains(t, body, `More<span class="n run" aria-hidden="true">▸</span><span class="vh">, Activity: 1 running</span></button>`)
}

func TestStaticFilesNeedNoSession(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(request("GET", "/static/"+ts.srv.assets.Version()+"/app.css"))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, contentSecurityPolicy, w.Header().Get("Content-Security-Policy"))
	r := request("GET", "/static/"+ts.srv.assets.Version()+"/app.css")
	r.Host = "rebind.example:7878"
	assert.Equal(t, http.StatusMisdirectedRequest, ts.do(r).Code, "the Host check stays in front")
	assert.Equal(t, http.StatusForbidden, ts.do(request("GET", "/htmx/"+ts.srv.vendor.Version()+"/htmx.min.js")).Code,
		"only sind ui's own files are open")
}

func TestRefusedBrowserGetsTheSignInPage(t *testing.T) {
	ts := newTestServer(t)
	var logs bytes.Buffer
	ts.srv.log = slog.New(slog.NewTextHandler(&logs, nil))
	page := func(r *http.Request) *http.Request {
		r.Header.Set("Sec-Fetch-Dest", "document")
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		return r
	}

	w := ts.do(page(request("GET", "/clusters")))
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	body := w.Body.String()
	assert.Contains(t, body, "<title>Sign in · sind ui</title>")
	assert.Contains(t, body, `<link rel="stylesheet" href="`+ts.srv.assets.URL("app.css")+`">`)
	assert.Contains(t, body, `<p class="callout warn">This browser is not signed in.</p>`)
	assert.Contains(t, body, `<code>sind ui</code><button type="button" data-shell-copy="sind ui">Copy</button>`)
	assert.Contains(t, body, "Open the address that it prints")
	assert.NotContains(t, body, "the-token")
	assert.NotRegexp(t, `<script[^>]*>[^<]`, body)
	assert.NotContains(t, body, " style=")
	assert.Contains(t, logs.String(), "no session cookie")

	// An old link after a restart: the same page, with its reason.
	body = ts.do(page(request("GET", "/?token=old"))).Body.String()
	assert.Contains(t, body, `<p class="callout warn">This link holds an old or wrong token.</p>`)

	// A browser without Sec-Fetch headers that asks for HTML.
	r := request("GET", "/")
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	assert.Contains(t, ts.do(r).Body.String(), "<title>Sign in · sind ui</title>")

	// What a page's script sends gets the line of text.
	fetch := request("GET", "/events")
	fetch.Header.Set("Sec-Fetch-Dest", "empty")
	for name, r := range map[string]*http.Request{
		"fetch":   fetch,
		"post":    page(request("POST", "/realm")),
		"no hint": request("GET", "/"),
	} {
		w := ts.do(r)
		assert.Equal(t, http.StatusForbidden, w.Code, name)
		assert.True(t, strings.HasSuffix(w.Body.String(), openHint+"\n"), name)
	}
}

func TestSignInPageThatFailsFallsBackToText(t *testing.T) {
	cfg, st := testConfig(t)
	cfg.FS = overlay(t, fstest.MapFS{
		"templates/test.tmpl":   {Data: []byte(testTemplates)},
		"templates/signin.tmpl": {Data: []byte(`{{define "signin"}}{{.Nope}}{{end}}`)},
	})
	ts := newTestServerFrom(t, cfg, st)
	var logs bytes.Buffer
	ts.srv.log = slog.New(slog.NewTextHandler(&logs, nil))
	r := request("GET", "/")
	r.Header.Set("Sec-Fetch-Dest", "document")
	w := ts.do(r)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "This browser is not signed in.\n"+openHint+"\n", w.Body.String())
	assert.Contains(t, logs.String(), "rendering the sign-in page")
}

func TestServerNeedsTheSignInPage(t *testing.T) {
	cfg, _ := testConfig(t)
	fsys := overlay(t, fstest.MapFS{"templates/test.tmpl": {Data: []byte(testTemplates)}})
	delete(fsys.(fstest.MapFS), "templates/signin.tmpl")
	cfg.FS = fsys
	_, err := newServer(t.Context(), cfg)
	assert.ErrorContains(t, err, `template "signin" is missing`)
}
