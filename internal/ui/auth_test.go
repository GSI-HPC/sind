// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strings"
	"testing"

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
	first := ts.cookie
	ts.login()
	assert.NotEqual(t, first.Value, ts.cookie.Value)
	assert.NotEqual(t, ts.srv.auth.sessions[first.Value], ts.csrf)
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

	w = ts.post("/changes", "name="+strings.Repeat("a", maxFormBytes+1))
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	w = ts.post("/changes?x=%zz", "name=dev")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "malformed")
}

func TestSecretsAreRandomAndCompared(t *testing.T) {
	a, b := newSecret(), newSecret()
	assert.NotEqual(t, a, b)
	assert.Len(t, a, 43)
	assert.True(t, equalSecret(a, a))
	assert.False(t, equalSecret(a, b))
	assert.False(t, equalSecret(a, ""))
}

func TestForbiddenSaysNothingAboutTheToken(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(request("GET", "/"))
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "the-token")
	assert.Contains(t, w.Body.String(), "sind ui printed in your terminal")
}
