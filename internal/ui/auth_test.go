// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/localweb/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenExchange(t *testing.T) {
	ts := newTestServer(t)
	assert.Equal(t, http.StatusForbidden, ts.do(request("GET", "/")).Code, "without a session")

	w := ts.do(request("GET", "/?token=wrong"))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, w.Result().Cookies())
	assert.Contains(t, w.Body.String(), "old or wrong token")

	ts.login()
	assert.Equal(t, http.StatusOK, ts.get("/demo/x").Code)

	r := request("GET", "/")
	r.AddCookie(&http.Cookie{Name: ts.cookie.Name, Value: "made-up"}) //nolint:gosec // G124: a forged cookie
	assert.Equal(t, http.StatusForbidden, ts.do(r).Code, "unknown session")
}

func TestTokenExchangeKeepsQueryAndMethod(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(request("GET", "/demo/x?q=a&token=the-token&q=b"))
	assert.Equal(t, "/demo/x?q=a&q=b", w.Header().Get("Location"))

	// HEAD may log in too; a POST with the token does not.
	assert.Equal(t, http.StatusSeeOther, ts.do(request("HEAD", "/?token=the-token")).Code)
	ts.login()
	r := request("POST", "/x?token=the-token")
	r.Header.Set("Origin", "http://127.0.0.1:7878")
	assert.Equal(t, http.StatusForbidden, ts.do(r).Code)
}

func TestSessionsAreIndependent(t *testing.T) {
	ts := newTestServer(t).login()
	first, firstCSRF := ts.cookie, ts.csrf
	ts.login()
	assert.NotEqual(t, first.Value, ts.cookie.Value)
	assert.NotEqual(t, firstCSRF, ts.csrf)
}

func TestPostNeedsOriginAndCSRF(t *testing.T) {
	ts := newTestServer(t).login()
	reached := 0
	ts.srv.mux.HandleFunc("POST /changes", func(http.ResponseWriter, *http.Request) { reached++ })
	post := func(origin, csrf, body string) int {
		r := request("POST", "/changes")
		r.Body = http.NoBody
		if body != "" {
			r.Body = nopCloser(body)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		r.AddCookie(ts.cookie)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set(csrfHeader, csrf)
		}
		return ts.do(r).Code
	}
	for _, c := range []struct {
		name, origin, csrf, body string
		want                     int
	}{
		{"no origin", "", ts.csrf, "", http.StatusForbidden},
		{"other origin", "http://evil.example", ts.csrf, "", http.StatusForbidden},
		{"other port", "http://127.0.0.1:7879", ts.csrf, "", http.StatusForbidden},
		{"no csrf", "http://127.0.0.1:7878", "", "", http.StatusForbidden},
		{"wrong csrf", "http://127.0.0.1:7878", "nope", "", http.StatusForbidden},
		{"wrong csrf field", "http://127.0.0.1:7878", "", "csrf=nope", http.StatusForbidden},
		{"header", "http://127.0.0.1:7878", ts.csrf, "", http.StatusOK},
		{"form field", "http://127.0.0.1:7878", "", "csrf=" + ts.csrf, http.StatusOK},
		{"header wins over a wrong field", "http://127.0.0.1:7878", ts.csrf, "csrf=nope", http.StatusOK},
	} {
		assert.Equal(t, c.want, post(c.origin, c.csrf, c.body), c.name)
	}
	assert.Equal(t, 3, reached, "only the requests that passed reached the handler")
}

func TestPostFormIsCappedAndChecked(t *testing.T) {
	ts := newTestServer(t).login()
	ts.srv.mux.HandleFunc("POST /changes", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.PostForm.Get("name")))
	})
	w := ts.post("/changes", "name=dev&csrf="+ts.csrf)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "dev", w.Body.String(), "the handler finds the form parsed")

	w = ts.post("/changes", "name="+strings.Repeat("a", session.DefaultMaxBody+1))
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	w = ts.post("/changes?x=%zz", "name=dev")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "malformed")
}

func TestForbiddenSaysNothingAboutTheToken(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(request("GET", "/"))
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "the-token")
	assert.Contains(t, w.Body.String(), "sind ui printed in your terminal")
}

// The cookie also goes to pages on other ports of 127.0.0.1. A browser
// marks such requests, and they get no further than a plain navigation.
func TestFetchMetadata(t *testing.T) {
	ts := newTestServer(t).login()
	reached := 0
	ts.srv.mux.HandleFunc("POST /changes", func(http.ResponseWriter, *http.Request) { reached++ })
	for _, c := range []struct {
		name, method, target, site, mode, dest string
		want                                   int
	}{
		{"no metadata", "GET", "/demo/x", "", "", "", http.StatusOK},
		{"own page", "GET", "/demo/x", "same-origin", "cors", "empty", http.StatusOK},
		{"typed address", "GET", "/demo/x", "none", "navigate", "document", http.StatusOK},
		{"link from another port", "GET", "/demo/x", "same-site", "navigate", "document", http.StatusOK},
		{"link from another site", "GET", "/demo/x", "cross-site", "navigate", "document", http.StatusOK},
		{"typed address, fetched", "GET", "/demo/x", "none", "cors", "empty", http.StatusForbidden},
		{"image of another port", "GET", "/demo/x", "same-site", "no-cors", "image", http.StatusForbidden},
		{"fetch of another site", "GET", "/demo/x", "cross-site", "cors", "empty", http.StatusForbidden},
		{"form post of another port", "POST", "/changes", "same-site", "navigate", "document", http.StatusForbidden},
		{"post of own page", "POST", "/changes", "same-origin", "cors", "empty", http.StatusOK},
		{"events by link", "GET", "/events?f=x", "same-site", "navigate", "document", http.StatusForbidden},
	} {
		r := request(c.method, c.target)
		r.AddCookie(ts.cookie)
		r.Header.Set("Origin", "http://127.0.0.1:7878")
		r.Header.Set(csrfHeader, ts.csrf)
		for k, v := range map[string]string{"Sec-Fetch-Site": c.site, "Sec-Fetch-Mode": c.mode, "Sec-Fetch-Dest": c.dest} {
			if v != "" {
				r.Header.Set(k, v)
			}
		}
		assert.Equal(t, c.want, ts.do(r).Code, c.name)
	}
	assert.Equal(t, 1, reached, "only the post of an own page reached the handler")
}

// A page that another program serves sends its Origin along with a fetch;
// a plain navigation has none.
func TestPageGetWithAnotherOriginIsRefused(t *testing.T) {
	ts := newTestServer(t).login()
	for origin, want := range map[string]int{
		"http://127.0.0.1:7878": http.StatusOK,
		"http://127.0.0.1:7879": http.StatusForbidden,
		"http://evil.example":   http.StatusForbidden,
		"null":                  http.StatusForbidden,
	} {
		r := request("GET", "/demo/x")
		r.AddCookie(ts.cookie)
		r.Header.Set("Origin", origin)
		assert.Equal(t, want, ts.do(r).Code, origin)
	}
}
