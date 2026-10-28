// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb"
	"github.com/GSI-HPC/go-clikit/localweb/session"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPort = 7878

// csrfPath is the test route that answers with the CSRF token of the
// request's session, as a page puts it in its forms.
const csrfPath = "/test/csrf"

// testBound is what the listener of the test servers bound: 127.0.0.1 and
// testPort.
func testBound() *localweb.Bound {
	return &localweb.Bound{Addrs: []netip.AddrPort{netip.MustParseAddrPort(fmt.Sprintf("127.0.0.1:%d", testPort))}}
}

// syncBuffer is a bytes.Buffer that goroutines may share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// snapshot returns a copy of r, for a test to add to without changing r.
func (r *Registry) snapshot() *Registry {
	return &Registry{pages: slices.Clone(r.pages), fragments: maps.Clone(r.fragments), routes: slices.Clone(r.routes),
		services: slices.Clone(r.services), navCounts: maps.Clone(r.navCounts)}
}

// overlay returns the embedded web files with the files of extra added
// (or replaced).
func overlay(t *testing.T, extra fstest.MapFS) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	base := webFS()
	require.NoError(t, fs.WalkDir(base, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(base, p)
		m[p] = &fstest.MapFile{Data: data}
		return err
	}))
	for p, f := range extra {
		m[p] = f
	}
	return m
}

// testTemplates are the blocks of the test page and fragments.
const testTemplates = `
{{define "page-demo"}}<h1>demo {{.Params.Get "id"}}</h1>
<p sse-swap="count">{{fragment "count" .}}</p>
<p sse-swap="one-run">{{fragment "one-run" .}}</p>{{template "csrf-field" .}}{{end}}
{{define "count"}}tasks={{.Data}}{{end}}
{{define "same"}}always the same{{with .Data}} {{.}}{{end}}{{end}}
{{define "runs"}}runs={{.Data}}{{end}}
{{define "one-run"}}run={{.Data}}{{end}}
`

// testRegistry returns a registry with the shell fragments, a page "demo"
// and the fragments that the page and the stream tests use.
func testRegistry() *Registry {
	r := NewRegistry()
	r.AddFragment(railFragment())
	r.AddFragment(tasksFragment())
	r.AddFragment(Fragment{
		Name: "count", Topics: []store.Topic{store.TopicTasks},
		Load: func(v *View) error { v.Data = len(v.Snap.Tasks); return nil },
	})
	r.AddFragment(Fragment{Name: "same", Topics: []store.Topic{store.TopicTasks}, Load: func(*View) error { return nil }})
	r.AddFragment(Fragment{
		Name: "runs", Prefixes: []string{"run"},
		Load: func(v *View) error { v.Data = len(v.Snap.Runs); return nil },
	})
	r.AddFragment(Fragment{
		Name: "one-run", Params: []string{"id"},
		Watch: func(p Params) []store.Topic { return []store.Topic{store.RunTopic(p.Get("id"))} },
		Load: func(v *View) error {
			id := v.Params.Get("id")
			if id == "bad" {
				return fmt.Errorf("id: not a run id: %w", ErrBadRequest)
			}
			if id == "boom" {
				return fmt.Errorf("boom")
			}
			if id == "" {
				return nil
			}
			run, ok := v.Snap.Run(id)
			if !ok {
				return fmt.Errorf("run %s: %w", id, ErrNotFound)
			}
			v.Data = run.Title
			return nil
		},
	})
	r.AddRoute(Route{Pattern: "GET /frag/{id}", Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		s.Fragment(w, r, "one-run")
	}})
	r.AddRoute(Route{Pattern: "GET /frag-unknown", Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		s.Fragment(w, r, "nope")
	}})
	r.AddRoute(Route{Pattern: "POST /act/{id}", Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		v, err := s.View(r, "name", "tag")
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		v.Data = fmt.Sprintf("id=%s name=%s tag=%v csrf=%t", v.Params.Get("id"), v.Params.Get("name"), v.Params["tag"], v.CSRF != "")
		s.Write(w, r, http.StatusOK, "same", v)
	}})
	r.AddRoute(Route{Pattern: "GET /broken", Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		s.Write(w, r, http.StatusOK, "no-such-block", s.newView(Params{}))
	}})
	r.AddPage(Page{
		Name: "demo", Pattern: "GET /demo/{id}", Title: "Demo", Nav: "clusters",
		Params: []string{"q"}, Fragments: []string{"count", "one-run"},
		Load: func(v *View) error {
			if v.Params.Get("q") == "missing" {
				return ErrNotFound
			}
			return nil
		},
	})
	return r
}

func testConfig(t *testing.T) (serverConfig, *store.Store) {
	t.Helper()
	st := store.New()
	env := &Env{Store: st, Exec: &mock.Executor{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Addr: fmt.Sprintf("127.0.0.1:%d", testPort)}
	return serverConfig{
		Env: env, Token: "the-token", Bound: testBound(), Registry: testRegistry(),
		FS: overlay(t, fstest.MapFS{"templates/test.tmpl": {Data: []byte(testTemplates)}}),
	}, st
}

// testServer is a Server with a logged-in session.
type testServer struct {
	t      *testing.T
	srv    *Server
	h      http.Handler
	st     *store.Store
	cancel context.CancelFunc
	cookie *http.Cookie
	csrf   string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	cfg, st := testConfig(t)
	return newTestServerFrom(t, cfg, st)
}

func newTestServerFrom(t *testing.T, cfg serverConfig, st *store.Store) *testServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := newServer(ctx, cfg)
	require.NoError(t, err)
	srv.coalesce = 20 * time.Millisecond
	srv.mux.HandleFunc("GET "+csrfPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, session.CSRF(r.Context()))
	})
	st.SetRuns([]store.Run{{ID: "x", Title: "create cluster dev"}})
	return &testServer{t: t, srv: srv, h: srv.Handler(), st: st, cancel: cancel}
}

// request builds a request to the server as a browser on 127.0.0.1 would.
func request(method, target string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:7878"+target, nil)
	r.Host = "127.0.0.1:7878"
	return r
}

func (ts *testServer) do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(w, r)
	return w
}

// login exchanges the token for a session, as opening the printed URL does.
func (ts *testServer) login() *testServer {
	ts.t.Helper()
	w := ts.do(request("GET", "/?token=the-token"))
	require.Equal(ts.t, http.StatusSeeOther, w.Code)
	require.Equal(ts.t, "/", w.Header().Get("Location"))
	cookies := w.Result().Cookies()
	require.Len(ts.t, cookies, 1)
	ts.cookie = cookies[0]
	assert.True(ts.t, ts.cookie.HttpOnly)
	assert.Equal(ts.t, http.SameSiteStrictMode, ts.cookie.SameSite)
	assert.Equal(ts.t, "sind_ui_7878", ts.cookie.Name)
	ts.csrf = ts.get(csrfPath).Body.String()
	require.NotEmpty(ts.t, ts.csrf)
	return ts
}

// get requests target in the session.
func (ts *testServer) get(target string) *httptest.ResponseRecorder {
	r := request("GET", target)
	r.AddCookie(ts.cookie)
	return ts.do(r)
}

// post sends a form to target in the session, with the headers that make
// it pass the checks.
func (ts *testServer) post(target string, form string) *httptest.ResponseRecorder {
	return ts.postAs(target, form, false)
}

// postHTMX is post with the header that htmx sets on its requests.
func (ts *testServer) postHTMX(target string, form string) *httptest.ResponseRecorder {
	return ts.postAs(target, form, true)
}

func (ts *testServer) postAs(target string, form string, htmx bool) *httptest.ResponseRecorder {
	r := request("POST", target)
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	r.Body = io.NopCloser(strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://127.0.0.1:7878")
	r.Header.Set(csrfHeader, ts.csrf)
	r.AddCookie(ts.cookie)
	return ts.do(r)
}

// nopCloser returns a request body that reads s.
func nopCloser(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
