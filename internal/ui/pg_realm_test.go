// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgGetWithRealm gets target in the session of ts, as a link of a page of
// sind ui does, with the realm cookie set to realm unless it is empty.
func pgGetWithRealm(ts *testServer, target, realm string) *httptest.ResponseRecorder {
	r := request("GET", target)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(ts.cookie)
	if realm != "" {
		r.AddCookie(&http.Cookie{Name: realmCookie, Value: realm})
	}
	return ts.do(r)
}

// pgRealmSet returns the realm cookie that w sets, or nil.
func pgRealmSet(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == realmCookie {
			return c
		}
	}
	return nil
}

func TestPgRealmInTheAddress(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)

	// A ?realm= of a realm that exists is remembered.
	w := pgGetWithRealm(ts, "/nodes?realm=ci", "")
	require.Equal(t, http.StatusOK, w.Code)
	c := pgRealmSet(w)
	require.NotNil(t, c)
	assert.Equal(t, "ci", c.Value)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, "/", c.Path)
	assert.Positive(t, c.MaxAge)
	// A typed address, or a link of another site, shows the realm and
	// changes nothing.
	for _, site := range []string{"none", "cross-site", ""} {
		r := request("GET", "/nodes?realm=ci")
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
			r.Header.Set("Sec-Fetch-Mode", "navigate")
			r.Header.Set("Sec-Fetch-Dest", "document")
		}
		r.AddCookie(ts.cookie)
		w = ts.do(r)
		require.Equal(t, http.StatusOK, w.Code, site)
		assert.Contains(t, w.Body.String(), `<option value="ci" selected>`, site)
		assert.Nil(t, pgRealmSet(w), site)
	}
	// The same realm again sets nothing; another one replaces it.
	assert.Nil(t, pgRealmSet(pgGetWithRealm(ts, "/nodes?realm=ci", "ci")))
	assert.Equal(t, "sind", pgRealmSet(pgGetWithRealm(ts, "/clusters?realm=sind", "ci")).Value)
	// Every realm forgets it.
	w = pgGetWithRealm(ts, "/clusters?realm=", "ci")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Negative(t, pgRealmSet(w).MaxAge)
	body := w.Body.String()
	assert.Contains(t, body, `href="/clusters/sind/dev"`)
	assert.Contains(t, body, `href="/clusters/ci/default"`)
	assert.Contains(t, body, `<option value="" selected>all realms</option>`)
	// A realm that does not exist, or a bad name, is not remembered.
	assert.Nil(t, pgRealmSet(pgGetWithRealm(ts, "/clusters?realm=nowhere", "ci")))
	w = pgGetWithRealm(ts, "/clusters?realm=A_B", "ci")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Nil(t, pgRealmSet(w))

	// A list page without ?realm= goes to the address of the remembered
	// realm, with the rest of its query.
	for path, want := range map[string]string{
		"/clusters": "/clusters?realm=ci", "/nodes?x=1": "/nodes?realm=ci&x=1",
	} {
		w = pgGetWithRealm(ts, path, "ci")
		require.Equal(t, http.StatusFound, w.Code, path)
		assert.Equal(t, want, w.Header().Get("Location"), path)
	}
	// A bad cookie is the user's: it is left as it is, and ignored.
	w = pgGetWithRealm(ts, "/nodes", "Not a realm")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "worker-3.dev")
	assert.Nil(t, pgRealmSet(w))
	// A remembered realm that is gone is forgotten, once Docker answered.
	w = pgGetWithRealm(ts, "/nodes", "gone")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Negative(t, pgRealmSet(w).MaxAge)
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	w = pgGetWithRealm(ts, "/nodes", "gone")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, pgRealmSet(w), "Docker may know it again")

	// The live updates carry the realm of the page, and the fragment that
	// they render offers to show every realm on the same page.
	body = pgGetWithRealm(ts, "/nodes?realm=ci", "").Body.String()
	m := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, m)
	events, err := url.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
	require.NoError(t, err)
	assert.Equal(t, "ci", events.Query().Get("realm"))
	assert.Equal(t, "nodes", events.Query().Get("nav"))
	assert.Contains(t, events.Query()["f"], "pg-nodes-table")
	v := ts.srv.newView(Params{"realm": {"ci"}})
	v.Nav = "nodes"
	html, err := ts.srv.renderFragment(ts.srv.reg.fragments["pg-nodes-head"], v)
	require.NoError(t, err)
	assert.Contains(t, html, `<a href="/nodes?realm=">Show all</a>`)

	// The Overview shows every realm without ?realm=, and remembers one.
	w = pgGetWithRealm(ts, "/", "ci")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `href="/clusters/sind/dev"`)
	assert.Nil(t, pgRealmSet(w))
	assert.Equal(t, "ci", pgRealmSet(pgGetWithRealm(ts, "/?realm=ci", "")).Value)
}

func TestPgRealmPicker(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/clusters", 200)
	assert.Contains(t, body, `<form class="realm" method="get" action="/clusters" autocomplete="off" data-pg-realm>`)
	assert.Contains(t, body, `<select id="realm-sel" name="realm" aria-label="Realm" aria-describedby="realm-hint"><option value="" selected>all realms</option></select>`)
	assert.Contains(t, body, `<noscript><button type="submit" class="btn sm">Show</button></noscript>`)
	assert.Contains(t, body, `<span class="vh" id="realm-hint" role="status" data-pg-realm-hint></span>`)
	pgFillStore(ts.st)
	// A page of one cluster or node shows its realm, and the picker goes to
	// the list.
	body = pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.Contains(t, body, `<option value="sind" selected>sind · 2</option>`)
	assert.Contains(t, body, `<option value="ci">ci · 1</option>`)
	assert.Contains(t, body, `action="/clusters"`)
	assert.NotContains(t, body, `class="pg-scope"`)
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/worker-0", 200), `action="/nodes"`)
	// The Overview stays the Overview.
	assert.Contains(t, pgBody(t, ts, "/?realm=ci", 200), `action="/"`)
	// A realm that the snapshot does not hold is the Not found page, but
	// while Docker is down it is still the realm shown.
	pgBody(t, ts, "/nodes?realm=future", 404)
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	body = pgBody(t, ts, "/nodes?realm=future", 200)
	assert.Contains(t, body, `<option value="future" selected>future</option>`)
	assert.Contains(t, body, `action="/nodes"`)
}

func TestPgRealmPickerStaysOnThePage(t *testing.T) {
	// Every page that shows one realm or all of them is registered under
	// the name that pgRealmPages has for it, so the picker stays there.
	ts, _ := moreTestServer(t, true, false)
	for name, path := range pgRealmPages {
		target := path + "?realm=sind"
		w := ts.get(target)
		require.Equal(t, http.StatusOK, w.Code, name)
		body := w.Body.String()
		assert.Contains(t, body, `<form class="realm" method="get" action="`+path+`"`, name)
		assert.Contains(t, body, `<option value="sind" selected>`, name)
		assert.Contains(t, body, `Showing realm <b class="mono">sind</b> · <a href="`+path+`?realm=">Show all</a>`, name)
		assert.Contains(t, body, " · sind · sind ui</title>", name)
		if c := pgRealmSet(pgGetWithRealm(ts, target, "")); assert.NotNil(t, c, name) {
			assert.Equal(t, "sind", c.Value, "the lists show the realm picked on %s next", name)
		}
		// Each answers a realm that does not exist the same way, but
		// Activity, as the event log keeps the runs of a realm that is gone.
		w = ts.get(path + "?realm=sindd")
		if name == "run-activity" {
			require.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), `Showing realm <b class="mono">sindd</b>`)
			continue
		}
		require.Equal(t, http.StatusNotFound, w.Code, name)
		body = w.Body.String()
		assert.Contains(t, body, "There is no realm sindd.", name)
		assert.Contains(t, body, `href="`+path+`?realm=sind">Realm sind</a>`, name)
	}
}

func TestPgRealmGoneKeepsLiveUpdates(t *testing.T) {
	// Only the page answers Not found for a realm that is gone: the live
	// updates of a page open on it keep showing it, empty.
	st := store.New()
	st.SetDocker(store.DockerState{Checked: true})
	v := &View{Params: Params{"realm": {"gone"}}, Snap: st.Snapshot()}
	require.NoError(t, pgPageFragments(pgClustersPage, nil)[0].Load(v))
	assert.Empty(t, v.Data.(*pgClusterList).Clusters)
}
