// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb"
	"github.com/GSI-HPC/go-clikit/localweb/htmx"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostHeader(t *testing.T) {
	ts := newTestServer(t).login()
	for host, want := range map[string]int{
		"127.0.0.1:7878":      http.StatusOK,
		"localhost:7878":      http.StatusMisdirectedRequest,
		"rebind.example:7878": http.StatusMisdirectedRequest,
		"127.0.0.1:7879":      http.StatusMisdirectedRequest,
		"127.0.0.1":           http.StatusMisdirectedRequest,
		"":                    http.StatusMisdirectedRequest,
	} {
		r := request("GET", "/demo/x")
		r.Host = host
		r.AddCookie(ts.cookie)
		assert.Equal(t, want, ts.do(r).Code, "Host %q", host)
	}
	// Even the token exchange needs our host name.
	r := request("GET", "/?token=the-token")
	r.Host = "rebind.example:7878"
	assert.Equal(t, http.StatusMisdirectedRequest, ts.do(r).Code)
}

func TestRefusalsAreLoggedAndSayWhereTheAddressIs(t *testing.T) {
	ts := newTestServer(t)
	var logs bytes.Buffer
	ts.srv.log = slog.New(slog.NewTextHandler(&logs, nil))

	r := request("GET", "/")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := ts.do(r)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "The request came from another site.\n"+openHint+"\n", w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	assert.Contains(t, logs.String(), "Sec-Fetch-Site is cross-site")

	w = ts.do(request("GET", "/"))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "This browser is not signed in.\n"+openHint+"\n", w.Body.String())
	assert.Contains(t, logs.String(), "no session cookie")

	// A wrong Host is not told where the address is, nor which Host would do.
	r = request("GET", "/")
	r.Host = "rebind.example:7878"
	w = ts.do(r)
	assert.Equal(t, http.StatusMisdirectedRequest, w.Code)
	assert.Equal(t, "This server answers to its loopback address only.\n", w.Body.String())
	assert.Contains(t, logs.String(), "rebind.example:7878")
}

func TestSecurityHeaders(t *testing.T) {
	ts := newTestServer(t).login()
	for _, target := range []string{"/demo/x", "/nope", "/static/" + ts.srv.assets.Version() + "/app.css", "/events"} {
		h := ts.get(target).Header()
		for k, want := range map[string]string{
			"Content-Security-Policy":      contentSecurityPolicy,
			"Referrer-Policy":              "same-origin",
			"X-Content-Type-Options":       "nosniff",
			"X-Frame-Options":              "DENY",
			"Cross-Origin-Opener-Policy":   "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin",
		} {
			assert.Equal(t, want, h.Get(k), "%s: %s", target, k)
		}
		assert.Empty(t, h.Get("Access-Control-Allow-Origin"), "%s allows CORS", target)
	}
	assert.Equal(t, "no-store", ts.get("/demo/x").Header().Get("Cache-Control"))
	// Not even a refused request goes without them.
	assert.NotEmpty(t, ts.do(request("GET", "/demo/x")).Header().Get("Content-Security-Policy"))

	for _, directive := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "font-src 'self'", "frame-ancestors 'none'"} {
		assert.Contains(t, contentSecurityPolicy, directive)
	}
	assert.NotContains(t, contentSecurityPolicy, "unsafe")
	assert.NotContains(t, contentSecurityPolicy, "http")
}

func TestPageShell(t *testing.T) {
	cfg, st := testConfig(t)
	cfg.FS = overlay(t, fstest.MapFS{
		"templates/test.tmpl": {Data: []byte(testTemplates)},
		"static/css/demo.css": {Data: []byte("p{}")},
		"static/js/demo.js":   {Data: []byte("1")},
	})
	ts := newTestServerFrom(t, cfg, st).login()
	w := ts.get("/demo/x?q=1&other=2")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	a := ts.srv.assets

	assert.Contains(t, body, "<title>Demo · sind ui</title>")
	assert.Regexp(t, `<body data-now="\d+" hx-headers='\{"X-Sind-CSRF":"`+ts.csrf+`"\}'>`, body)
	assert.Contains(t, body, `<link rel="stylesheet" href="`+a.URL("app.css")+`">`)
	assert.Contains(t, body, `<link rel="stylesheet" href="`+a.URL("css/demo.css")+`">`)
	assert.Less(t, strings.Index(body, a.URL("app.css")), strings.Index(body, a.URL("css/demo.css")), "extra styles follow app.css")
	assert.Contains(t, body, `<script src="`+a.URL("app.js")+`"></script>`)
	v := ts.srv.vendor
	for _, name := range []string{htmx.Script, htmx.SSE, htmx.Morph} {
		assert.Contains(t, body, `<script src="`+v.URL(name)+`" defer></script>`, name)
	}
	assert.Contains(t, body, `<meta name="htmx-config" content='`+strings.ReplaceAll(htmx.Config, `"`, "&#34;")+`'>`)
	assert.Contains(t, body, `hx-ext="sse,morph"`)
	assert.Contains(t, body, `<script src="`+a.URL("js/demo.js")+`" defer></script>`)
	assert.Contains(t, body, "127.0.0.1:7878</span>", "the address in the top bar")

	// The page names the live updates it needs, with the Params they declare.
	m := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, m)
	events, err := url.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
	require.NoError(t, err)
	assert.Equal(t, "/events", events.Path)
	assert.Equal(t, []string{"count", "one-run", "rail", "tasks"}, events.Query()["f"])
	assert.Equal(t, "x", events.Query().Get("id"))
	assert.Equal(t, "clusters", events.Query().Get("nav"))
	assert.NotContains(t, events.Query(), "other", "a key no fragment declares")
	assert.NotContains(t, events.Query(), "q", "the page's own key is not a fragment's")

	// The shell: the rail with the lit entry, the content and the dock.
	assert.Contains(t, body, `<a class="nav on" href="/clusters" aria-current="page">`)
	assert.Contains(t, body, `<a class="nav" href="/nodes">`)
	for _, label := range []string{"Overview", "Clusters", "Nodes", "Mesh and DNS", "Monitoring", "Topology", "Doctor", "Activity"} {
		assert.Contains(t, body, label)
	}
	assert.Contains(t, body, "<h1>demo x</h1>")
	assert.Contains(t, body, `<p sse-swap="count">tasks=0</p>`)
	assert.Contains(t, body, `<p sse-swap="one-run">run=create cluster dev</p>`)
	assert.Contains(t, body, `<input type="hidden" name="csrf" value="`+ts.csrf+`">`)
	assert.Contains(t, body, `id="tasks"`)
	assert.Contains(t, body, `data-theme-toggle`)
	assert.Contains(t, body, `id="realm-sel"`)

	// Nothing inline: the Content-Security-Policy would block it.
	assert.NotRegexp(t, `<script[^>]*>[^<]`, body)
	assert.NotRegexp(t, `<script(\s[^>]*)?>`+`\s*[^<\s]`, body)
	assert.NotContains(t, body, " style=")
	assert.NotRegexp(t, `\son[a-z]+=`, body)
	assert.NotContains(t, body, "javascript:")
}

func TestRailCountsAndLitEntry(t *testing.T) {
	cfg, st := testConfig(t)
	cfg.Registry.SetNavCount("clusters", func(store.Snapshot, time.Time) NavCount {
		return NavCount{Text: "2", Class: "bad"}
	})
	cfg.Registry.SetNavCount("nodes", func(store.Snapshot, time.Time) NavCount { return NavCount{Text: "5"} })
	ts := newTestServerFrom(t, cfg, st).login()
	body := ts.get("/demo/x").Body.String()
	assert.Contains(t, body, `Clusters<span class="n bad">2</span>`)
	assert.Contains(t, body, `Nodes<span class="n">5</span>`)
	assert.NotContains(t, body, `Overview<span`)
}

func TestNotFound(t *testing.T) {
	ts := newTestServer(t).login()
	for _, target := range []string{"/nope", "/demo/nope", "/demo/x?q=missing", "/frag/nope", "/frag-unknown", "/demo/x/y"} {
		w := ts.get(target)
		assert.Equal(t, http.StatusNotFound, w.Code, target)
		assert.Contains(t, w.Body.String(), "<h1>Not found</h1>", target)
		assert.Contains(t, w.Body.String(), "<title>Not found · sind ui</title>", target)
		assert.Contains(t, w.Body.String(), `sse-connect="/events?f=rail&amp;f=tasks"`, target)
	}
	// A POST to an unknown path is refused at the door, then not found.
	assert.Equal(t, http.StatusNotFound, ts.post("/nope", "").Code)
}

func TestWildcardTooLong(t *testing.T) {
	ts := newTestServer(t).login()
	// A wildcard that is too long is a bad request on a page; the Not found
	// page reads no Params.
	w := ts.get("/demo/" + strings.Repeat("a", maxParamLen+1))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "longer than")
	assert.NotContains(t, w.Body.String(), "bad request")
	assert.Equal(t, http.StatusNotFound, ts.get("/nope/"+strings.Repeat("a", maxParamLen+1)).Code)
}

func TestPageErrors(t *testing.T) {
	ts := newTestServer(t).login()
	w := ts.get("/demo/bad")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "id: not a run id\n", w.Body.String(), "the sentinel's own text is cut")

	w = ts.get("/demo/x?q=" + strings.Repeat("a", maxParamLen+1))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var values []string
	for range maxParamValues + 1 {
		values = append(values, "q=1")
	}
	w = ts.get("/demo/x?" + strings.Join(values, "&"))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "more than 64 values")

	w = ts.get("/demo/boom")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "boom", "the cause stays in the log")

	w = ts.get("/broken")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPageLoadError(t *testing.T) {
	cfg, st := testConfig(t)
	cfg.Registry.AddPage(Page{
		Name: "fails", Pattern: "GET /fails", Title: "Fails", Fragments: []string{"same"},
		Load: func(*View) error { return assert.AnError },
	})
	cfg.FS = overlay(t, fstest.MapFS{"templates/test.tmpl": {Data: []byte(testTemplates + `{{define "page-fails"}}x{{end}}`)}})
	ts := newTestServerFrom(t, cfg, st).login()
	assert.Equal(t, http.StatusInternalServerError, ts.get("/fails").Code)
}

func TestFragmentRoute(t *testing.T) {
	ts := newTestServer(t).login()
	w := ts.get("/frag/x")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "run=create cluster dev", w.Body.String())
	assert.Equal(t, http.StatusBadRequest, ts.get("/frag/bad").Code)
}

func TestRouteView(t *testing.T) {
	ts := newTestServer(t).login()
	w := ts.post("/act/7", "name=dev&tag=a&tag=b&hidden=1&csrf="+ts.csrf)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "always the same id=7 name=dev tag=[a b] csrf=true", w.Body.String())

	w = ts.post("/act/7?name=fromquery", "")
	assert.Equal(t, "always the same id=7 name=fromquery tag=[] csrf=true", w.Body.String())

	w = ts.post("/act/7", "name="+strings.Repeat("a", maxParamLen+1))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNewServerChecksWhatIsRegistered(t *testing.T) {
	good := fstest.MapFS{"templates/test.tmpl": {Data: []byte(testTemplates)}}
	for name, tc := range map[string]struct {
		mutate func(cfg *serverConfig, extra fstest.MapFS)
		want   string
	}{
		"no layout": {func(_ *serverConfig, e fstest.MapFS) {
			e["templates/layout.tmpl"] = &fstest.MapFile{Data: []byte("")}
		}, `template "layout" is missing`},
		"no not found page": {func(_ *serverConfig, e fstest.MapFS) {
			e["templates/notfound.tmpl"] = &fstest.MapFile{Data: []byte("")}
		}, `template "page-notfound" is missing`},
		"no rail": {func(cfg *serverConfig, _ fstest.MapFS) {
			cfg.Registry = NewRegistry()
			cfg.Registry.AddFragment(tasksFragment())
		}, "the shell fragment rail is not registered"},
		"no tasks": {func(cfg *serverConfig, _ fstest.MapFS) {
			cfg.Registry = NewRegistry()
			cfg.Registry.AddFragment(railFragment())
		}, "the shell fragment tasks is not registered"},
		"fragment without block": {func(cfg *serverConfig, _ fstest.MapFS) {
			cfg.Registry.AddFragment(Fragment{Name: "lonely"})
		}, "fragment lonely has no template block"},
		"page without block": {func(cfg *serverConfig, _ fstest.MapFS) {
			cfg.Registry.AddPage(Page{Name: "lonely", Pattern: "GET /lonely"})
		}, `page lonely has no template block "page-lonely"`},
		"page with unknown fragment": {func(cfg *serverConfig, e fstest.MapFS) {
			e["templates/more.tmpl"] = &fstest.MapFile{Data: []byte(`{{define "page-more"}}{{end}}`)}
			cfg.Registry.AddPage(Page{Name: "more", Pattern: "GET /more", Fragments: []string{"ghost"}})
		}, "page more names the unknown fragment ghost"},
		"template syntax": {func(_ *serverConfig, e fstest.MapFS) {
			e["templates/broken.tmpl"] = &fstest.MapFile{Data: []byte(`{{define "x"}}{{end`)}
		}, "templates:"},
		"no static files": {func(cfg *serverConfig, _ fstest.MapFS) {
			cfg.FS = fstest.MapFS{}
		}, "static files:"},
		"no token": {func(cfg *serverConfig, _ fstest.MapFS) {
			cfg.Token = ""
		}, "needs a Token"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, _ := testConfig(t)
			extra := fstest.MapFS{"templates/test.tmpl": good["templates/test.tmpl"]}
			tc.mutate(&cfg, extra)
			if cfg.FS == nil || name != "no static files" {
				cfg.FS = overlay(t, extra)
			}
			_, err := newServer(context.Background(), cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestNewServerDefaults(t *testing.T) {
	st := store.New()
	env := &Env{Store: st, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Addr: "127.0.0.1:1"}
	// The default registry, the embedded files and the system clock.
	s, err := newServer(context.Background(), serverConfig{Env: env, Token: "t", Bound: &localweb.Bound{Addrs: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")}}})
	require.NoError(t, err)
	r := request("GET", "/?token=t")
	r.Host = "127.0.0.1:1"
	w := newRecorder()
	s.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusSeeOther, w.Code)
	r = request("GET", "/")
	r.Host = "127.0.0.1:1"
	r.AddCookie(w.Result().Cookies()[0])
	w = newRecorder()
	s.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<h1>Overview</h1>")
	assert.WithinDuration(t, time.Now(), s.now(), time.Second)
}

func TestFragmentHTMLUnknown(t *testing.T) {
	ts := newTestServer(t)
	_, err := ts.srv.fragmentHTML("nope", &View{})
	assert.ErrorContains(t, err, `unknown fragment "nope"`)
}

func TestNotFoundThatCannotRender(t *testing.T) {
	cfg, st := testConfig(t)
	cfg.Registry = NewRegistry()
	cfg.Registry.AddFragment(Fragment{Name: "rail", Load: func(*View) error { return assert.AnError }})
	cfg.Registry.AddFragment(tasksFragment())
	ts := newTestServerFrom(t, cfg, st).login()
	assert.Equal(t, http.StatusInternalServerError, ts.get("/nope").Code)
}
