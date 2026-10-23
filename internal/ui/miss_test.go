// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMissIsNotFound(t *testing.T) {
	m := &Miss{What: "Realm sind has no cluster x."}
	assert.ErrorIs(t, m, ErrNotFound)
	assert.ErrorIs(t, fmt.Errorf("render: %w", m), ErrNotFound)
	assert.NotErrorIs(t, m, ErrBadRequest)
	assert.Equal(t, "Realm sind has no cluster x.", m.Error())
	var got *Miss
	require.True(t, errors.As(fmt.Errorf("render: %w", m), &got))
	assert.Same(t, m, got)
}

func TestMissEdits(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"dev", "dev", 0}, {"dev", "devv", 1}, {"dev", "", 3}, {"", "ci", 2},
		{"conect", "connect", 1}, {"kitten", "sitting", 3}, {"wörker", "worker", 1},
	} {
		assert.Equal(t, c.want, missEdits(c.a, c.b), "%s/%s", c.a, c.b)
	}
}

func TestMissClosest(t *testing.T) {
	names := []string{"clusters", "nodes", "mesh", "monitoring", "topology", "doctor", "activity"}
	assert.Equal(t, []string{"clusters"}, missClosest("cluster", names, 2))
	assert.Equal(t, []string{"monitoring"}, missClosest("monitor", names, 2), "a prefix")
	assert.Equal(t, []string{"nodes"}, missClosest("node", names, 2))
	assert.Equal(t, []string{"doctor"}, missClosest("docter", names, 2))
	assert.Empty(t, missClosest("x", names, 2))
	assert.Empty(t, missClosest("no", names, 2), "a short prefix is no match")
	assert.Equal(t, []string{"dev", "dev2", "devel"}, missClosest("dev", []string{"devel", "dev2", "dev", "prod"}, 3), "the closest first")
	assert.Len(t, missClosest("dev", []string{"devel", "dev2", "dev"}, 2), 2)
}

func TestMissForPath(t *testing.T) {
	assert.Equal(t, []MissLink{{Label: "Clusters", URL: "/clusters"}, missOverview}, missForPath(store.Snapshot{}, "/cluster").Links)
	assert.Equal(t, []MissLink{{Label: "Clusters", URL: "/clusters"}, missOverview}, missForPath(store.Snapshot{}, "/clusters/a/b/c").Links)
	assert.Equal(t, []MissLink{{Label: "Topology", URL: "/topology"}, missOverview}, missForPath(store.Snapshot{}, "/Topo").Links)
	assert.Equal(t, []MissLink{missOverview}, missForPath(store.Snapshot{}, "/").Links)
	assert.Equal(t, []MissLink{missOverview}, missForPath(store.Snapshot{}, "/zzz").Links)
	assert.Empty(t, missForPath(store.Snapshot{}, "/zzz").What)
}

func TestNavHref(t *testing.T) {
	assert.Equal(t, "/mesh", navHref("mesh"))
	assert.Equal(t, "/", navHref("nope"))
}

func TestErrorPageInTheLayout(t *testing.T) {
	ts := newTestServer(t).login()

	// The Not found page names the address and offers the page that is
	// close to it.
	w := ts.get("/clustres?x=1")
	require.Equal(t, http.StatusNotFound, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `Nothing lives at <code class="mono miss-addr">/clustres?x=1</code>`)
	assert.Contains(t, body, `<a class="btn primary" href="/clusters">Clusters</a>`)
	assert.Contains(t, body, `<a class="btn" href="/">Overview</a>`)
	assert.Contains(t, body, "<kbd>/</kbd>")

	// A long address is cut.
	body = ts.get("/" + strings.Repeat("a", 300)).Body.String()
	assert.Contains(t, body, strings.Repeat("a", maxAddressLen-1)+"…</code>")

	// A plain ErrNotFound offers the way back.
	body = ts.get("/frag/nope").Body.String()
	assert.Contains(t, body, `<a class="btn primary" href="/">Overview</a>`)
	assert.Contains(t, body, "Go back to")

	// A bad address that a browser navigated to gets the layout and the
	// message; htmx and scripts get the message alone.
	r := request("GET", "/demo/bad")
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	r.AddCookie(ts.cookie)
	w = ts.do(r)
	require.Equal(t, http.StatusBadRequest, w.Code)
	body = w.Body.String()
	assert.Contains(t, body, "<title>Bad address · sind ui</title>")
	assert.Contains(t, body, "<h1>Bad address</h1>")
	assert.Contains(t, body, `sind ui cannot show <code class="mono miss-addr">/demo/bad</code>`)
	assert.Contains(t, body, `<p class="miss-what">id: not a run id</p>`)
	r.Header.Set("HX-Request", "true")
	w = ts.do(r)
	assert.Equal(t, "id: not a run id\n", w.Body.String())
}
