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
		assert.Contains(t, w.Body.String(), `aria-label="Realm: ci, 1 cluster.`, site)
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
	assert.Contains(t, body, `aria-label="Realm: all realms, 3 clusters."`)
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

	// The live updates carry the realm of the page and its scope, and the
	// fragment that they render says which realm the list holds.
	body = pgGetWithRealm(ts, "/nodes?realm=ci", "").Body.String()
	m := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, m)
	events, err := url.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
	require.NoError(t, err)
	assert.Equal(t, "ci", events.Query().Get("realm"))
	assert.Equal(t, "ci", events.Query().Get("scope"))
	assert.Equal(t, "nodes", events.Query().Get("nav"))
	assert.Contains(t, events.Query()["f"], "pg-nodes-table")
	v := ts.srv.newView(Params{"realm": {"ci"}})
	v.Nav = "nodes"
	html, err := ts.srv.renderFragment(ts.srv.reg.fragments["pg-nodes-head"], v)
	require.NoError(t, err)
	assert.Contains(t, html, `<span class="pg-scope">2 nodes in realm <b class="mono">ci</b>.</span>`)

	// The Overview shows every realm without ?realm=; a ?realm= filters
	// only the page, which neither the cookie nor the scope remembers.
	w = pgGetWithRealm(ts, "/", "ci")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `href="/clusters/sind/dev"`)
	assert.Nil(t, pgRealmSet(w))
	assert.Nil(t, pgRealmSet(pgGetWithRealm(ts, "/?realm=ci", "")))
	assert.Nil(t, pgRealmSet(pgGetWithRealm(ts, "/?realm=", "ci")))
}

func TestPgRealmFilterKeepsTheScope(t *testing.T) {
	ts, _ := moreTestServer(t, false, false)
	pgFillStore(ts.st)
	// Overview and Activity narrowed to a realm keep the scope of the
	// cookie, and say that the realm filters only them.
	for _, path := range []string{"/?realm=ci", "/activity?realm=ci"} {
		w := pgGetWithRealm(ts, path, "sind")
		require.Equal(t, http.StatusOK, w.Code, path)
		body := w.Body.String()
		assert.Contains(t, body, `aria-label="Realm: sind,`, path)
		assert.Contains(t, body, `href="/clusters?realm=sind"`, path)
		assert.Contains(t, body, `<span class="pg-scope">Only realm <b class="mono">ci</b> on this page. <a href="`, path)
		assert.Nil(t, pgRealmSet(w), path)
	}
	// A realm that is gone, which Activity shows, is no scope: the links
	// of the Realm group do not carry it.
	body := pgGetWithRealm(ts, "/activity?realm=gone", "").Body.String()
	assert.Contains(t, body, `Only realm <b class="mono">gone</b> on this page.`)
	for _, link := range []string{"/clusters", "/nodes", "/mesh"} {
		assert.NotContains(t, body, link+`?realm=gone"`)
	}
	assert.Contains(t, body, `href="/clusters"`)
}

func TestPgRealmSwitcher(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/clusters", 200)
	assert.NotContains(t, body, "rail-rsw", "no realm, no switcher")
	pgFillStore(ts.st)
	// The switcher heads the Realm group: the scope, its clusters, and a
	// dot when another realm needs a look; the Overview group shows every
	// realm.
	body = pgBody(t, ts, "/clusters?realm=ci", 200)
	assert.Contains(t, body, `<span class="rgl" id="rail-overview">Overview<span class="rgs">all realms</span></span>`)
	assert.Contains(t, body, `<span class="rgl" id="rail-realm">Realm</span>
<details class="pg-mw rail-rsw" data-pg-menu><summary class="rail-rbtn" aria-haspopup="menu" aria-label="Realm: ci, 1 cluster. Realm sind: 1 node needs a look." aria-describedby="rail-realm-note"><b class="mono">ci</b><span class="faint">1 cluster</span><span class="rail-dot bad" title="Realm sind: 1 node needs a look" aria-hidden="true"></span>`)
	assert.Contains(t, body, `<a class="pg-mi rail-ro" role="menuitemradio" aria-checked="false" tabindex="-1" href="/clusters?realm="><span class="rail-ck" aria-hidden="true"></span><span class="pg-ml">All realms</span><span class="pg-mx">3 clusters</span></a>`)
	assert.Contains(t, body, `<a class="pg-mi rail-ro on" role="menuitemradio" aria-checked="true" tabindex="-1" href="/clusters?realm=ci"><span class="rail-ck" aria-hidden="true">✓</span><span class="pg-ml mono">ci</span><span class="rail-dot warn" aria-hidden="true"></span><span class="pg-mx">1 cluster · 1 needs a look</span></a>`)
	assert.Contains(t, body, `href="/clusters?realm=sind"><span class="rail-ck" aria-hidden="true"></span><span class="pg-ml mono">sind</span><span class="rail-dot bad" aria-hidden="true"></span><span class="pg-mx">2 clusters · 1 needs a look</span></a>`)
	assert.Contains(t, body, `<span class="pg-mx" id="rail-realm-note">Clusters, Nodes, Mesh and DNS, Monitoring and Topology show this realm. Overview and Activity show all realms.</span><span class="pg-ml">What is a realm?</span>`)
	// The More sheet of a phone has a copy of its own.
	assert.Contains(t, body, `aria-describedby="rail-realm-m-note"`)
	// The entries of the Realm group link to the realm and count it.
	assert.Contains(t, body, `<a class="nav on" href="/clusters?realm=ci" aria-current="page">`)
	assert.Contains(t, body, `href="/mesh?realm=ci">`)
	assert.Contains(t, body, `title="1 cluster in realm ci"`)
	assert.Contains(t, body, `title="1 node needs a look in realm ci"`)
	assert.Contains(t, body, `<a class="nav" href="/">`, "the Overview shows every realm")
	assert.Contains(t, body, `<a class="nav" href="/activity">`)
	// No dot for the realm of the scope, nor when it shows every realm.
	body = pgBody(t, ts, "/clusters?realm=sind", 200)
	assert.Contains(t, body, `aria-label="Realm: sind, 2 clusters. Realm ci: 1 node needs a look."`)
	body = pgBody(t, ts, "/clusters?realm=", 200)
	assert.Contains(t, body, `aria-label="Realm: all realms, 3 clusters." aria-describedby="rail-realm-note"><b class="mono">All realms</b><span class="faint">3 clusters</span><span class="pg-caret"`)
	assert.Contains(t, body, `<a class="nav on" href="/clusters" aria-current="page">`)
	// On a page of one cluster or node, the switcher keeps the scope and
	// goes to the list.
	body = pgGetWithRealm(ts, "/clusters/sind/dev", "ci").Body.String()
	assert.Contains(t, body, `aria-label="Realm: ci, 1 cluster.`)
	assert.Contains(t, body, `href="/clusters?realm=sind"><span class="rail-ck"`)
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/worker-0", 200), `href="/nodes?realm=sind"><span class="rail-ck"`)
	// The Overview and Activity show every realm: a pick goes to the list.
	assert.Contains(t, pgBody(t, ts, "/?realm=ci", 200), `href="/clusters?realm=sind"><span class="rail-ck"`)
	// A realm that the snapshot does not hold is the Not found page, but
	// while Docker is down it is still the realm shown.
	pgBody(t, ts, "/nodes?realm=future", 404)
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "down"})
	body = pgBody(t, ts, "/nodes?realm=future", 200)
	assert.Contains(t, body, `aria-label="Realm: future, no cluster. Realm sind: 1 node needs a look."`)
	assert.Contains(t, body, `href="/nodes?realm=future"><span class="rail-ck" aria-hidden="true">✓</span><span class="pg-ml mono">future</span><span class="pg-mx">no cluster</span></a>`)
}

func TestPgRealmScope(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	// The scope is the remembered realm, also on the pages that show every
	// realm, and none for a realm that is gone or a bad cookie.
	for cookie, want := range map[string]string{"ci": `aria-label="Realm: ci,`, "gone": `aria-label="Realm: all realms,`, "Not a realm": `aria-label="Realm: all realms,`} {
		body := pgGetWithRealm(ts, "/clusters/sind/dev", cookie).Body.String()
		assert.Contains(t, body, want, cookie)
	}
	// The live updates render the scope of the page, which a pick in
	// another tab does not change.
	v := ts.srv.newView(Params{})
	v.Scope = "ci"
	html, err := ts.srv.renderFragment(ts.srv.reg.fragments["rail"], v)
	require.NoError(t, err)
	assert.Contains(t, html, `aria-label="Realm: ci,`)
	// A bad scope is a bad request.
	assert.Equal(t, http.StatusBadRequest, ts.get("/events?f=rail&scope=A_B").Code)
}

func TestPgRealmPickerStaysOnThePage(t *testing.T) {
	// Every page of the Realm group that shows one realm or all of them is
	// registered under the name that pgRealmPages has for it, so the
	// switcher stays there.
	ts, _ := moreTestServer(t, true, false)
	for name, path := range pgRealmPages {
		target := path + "?realm=sind"
		w := ts.get(target)
		require.Equal(t, http.StatusOK, w.Code, name)
		body := w.Body.String()
		if name == "overview" || name == "run-activity" {
			assert.Contains(t, body, `href="/clusters?realm=sind"><span class="rail-ck"`, name)
			assert.Contains(t, body, `Only realm <b class="mono">sind</b> on this page. <a href="`+path+`?realm=">Show all realms</a>`, name)
			assert.Nil(t, pgRealmSet(pgGetWithRealm(ts, target, "")), "a filter of %s is not remembered", name)
		} else {
			assert.Contains(t, body, `href="`+path+`?realm=sind"><span class="rail-ck"`, name)
			assert.NotContains(t, body, `Only realm`, name)
			if c := pgRealmSet(pgGetWithRealm(ts, target, "")); assert.NotNil(t, c, name) {
				assert.Equal(t, "sind", c.Value, "the lists show the realm picked on %s next", name)
			}
		}
		assert.Contains(t, body, " · sind · sind ui</title>", name)
		// Each answers a realm that does not exist the same way, but
		// Activity, as the event log keeps the runs of a realm that is gone.
		w = ts.get(path + "?realm=sindd")
		if name == "run-activity" {
			require.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), `Only realm <b class="mono">sindd</b>`)
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
