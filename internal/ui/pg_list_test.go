// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgRowOrder returns the keys of the rows of a list page in their order,
// with the rows that the text filter hides marked "-".
func pgRowOrder(body string) []string {
	re := regexp.MustCompile(`<tr class="click" data-href="/(?:clusters|nodes)/([^"]+)"[^>]*?( hidden)?>`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if m[2] != "" {
			m[1] = "-" + m[1]
		}
		out = append(out, m[1])
	}
	return out
}

func TestPgClustersSortAndChips(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	for target, want := range map[string][]string{
		"/clusters":                  {"ci/default", "sind/dev", "sind/hpc"},
		"/clusters?sort=-name":       {"sind/hpc", "sind/dev", "ci/default"},
		"/clusters?sort=realm":       {"ci/default", "sind/dev", "sind/hpc"},
		"/clusters?sort=-realm":      {"sind/hpc", "sind/dev", "ci/default"},
		"/clusters?sort=state":       {"sind/dev", "ci/default", "sind/hpc"},
		"/clusters?sort=slurm":       {"sind/hpc", "ci/default", "sind/dev"},
		"/clusters?sort=nodes":       {"ci/default", "sind/hpc", "sind/dev"},
		"/clusters?sort=monitoring":  {"sind/hpc", "ci/default", "sind/dev"},
		"/clusters?state=problem":    {"ci/default", "sind/dev"},
		"/clusters?state=running":    {"sind/hpc"},
		"/clusters?state=monitoring": {"sind/hpc"},
		"/clusters?q=dev":            {"-ci/default", "sind/dev", "-sind/hpc"},
		"/clusters?q=worker-3.dev":   {"-ci/default", "sind/dev", "-sind/hpc"},
		"/clusters?q=SIND+run":       {"-ci/default", "-sind/dev", "sind/hpc"},
	} {
		assert.Equal(t, want, pgRowOrder(pgBody(t, ts, target, 200)), target)
	}

	// A sorted, filtered list keeps its query in its links and its form.
	body := pgBody(t, ts, "/clusters?realm=sind&q=dev&state=problem&sort=-nodes", 200)
	assert.Contains(t, body, `<a class="pg-chip" href="/clusters?q=dev&amp;realm=sind&amp;sort=-nodes" data-pg-keep>All <span class="n">2</span></a>`)
	assert.Contains(t, body, `<a class="pg-chip on bad" href="/clusters?q=dev&amp;realm=sind&amp;sort=-nodes&amp;state=problem" data-pg-keep aria-current="true">Needs a look <span class="n">1</span></a>`)
	assert.Contains(t, body, `<th class="num" aria-sort="descending"><a class="pg-sort on" href="/clusters?q=dev&amp;realm=sind&amp;sort=nodes&amp;state=problem" data-pg-keep title="Sort by Nodes">Nodes<span class="pg-arrow" aria-hidden="true">▼</span></a></th>`)
	assert.Contains(t, body, `<th><a class="pg-sort" href="/clusters?q=dev&amp;realm=sind&amp;sort=state&amp;state=problem" data-pg-keep title="Sort by State">State<span class="pg-arrow" aria-hidden="true">↕</span></a></th>`)
	assert.Contains(t, body, `<th>Roles</th>`, "a column that does not sort is no link")
	assert.Contains(t, body, `<a href="/clusters?q=dev&amp;realm=&amp;sort=-nodes&amp;state=problem" data-pg-keep>All realms</a>`)
	assert.Contains(t, body, `<input type="hidden" name="realm" value="sind"><input type="hidden" name="state" value="problem"><input type="hidden" name="sort" value="-nodes">
  <input type="search" name="q" value="dev"`)
	assert.Contains(t, body, `sse-connect="/events?f=pg-clusters-chips&amp;f=pg-clusters-head&amp;f=pg-clusters-table&amp;f=rail&amp;f=tasks&amp;nav=clusters&amp;q=dev&amp;realm=sind&amp;sort=-nodes&amp;state=problem"`)
	// The default sort reversed links back to the default.
	body = pgBody(t, ts, "/clusters?sort=-name", 200)
	assert.Contains(t, body, `<a class="pg-sort on" href="/clusters?realm=" data-pg-keep title="Sort by Name">Name<span class="pg-arrow" aria-hidden="true">▼</span></a>`)

	// A text filter that matches nothing shows the row that says so, and a
	// link that clears the filters.
	body = pgBody(t, ts, "/clusters?q=nothing&state=running", 200)
	assert.Contains(t, body, `<tr class="pg-none" data-pg-none><td colspan="9"><div class="empty">No cluster matches the filter. <a href="/clusters?realm=">Show all</a></div></td></tr>`)

	for _, bad := range []string{"/clusters?state=nope", "/clusters?sort=roles", "/clusters?sort=", "/clusters?sort=-x"} {
		if bad == "/clusters?sort=" {
			assert.Equal(t, http.StatusOK, ts.get(bad).Code, "an empty sort is the default")
			continue
		}
		assert.Equal(t, http.StatusBadRequest, ts.get(bad).Code, bad)
	}
}

func TestPgNodesSortAndChips(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	pgFillStatus(ts.st)
	order := func(target string) []string { return pgRowOrder(pgBody(t, ts, target, 200)) }
	all := order("/nodes")
	require.Len(t, all, 15)
	assert.Equal(t, []string{"ci/default/controller", "ci/default/worker-0", "sind/dev/controller"}, all[:3], "by cluster, then as sind lists them")
	assert.Equal(t, []string{"ci/default/worker-0", "sind/dev/worker-3"}, order("/nodes?state=stopped"))
	// worker-1 has slurmd down, but a command works on dev: not yet a problem.
	assert.Equal(t, []string{"ci/default/worker-0", "sind/dev/worker-3"}, order("/nodes?state=problem"))
	assert.Len(t, order("/nodes?state=running"), 13)
	assert.Equal(t, []string{"sind/dev/worker-3", "ci/default/worker-0", "sind/dev/worker-1"}, order("/nodes?sort=state")[:3])
	assert.Equal(t, "sind/dev/api", order("/nodes?sort=node")[0])
	assert.Equal(t, "sind/dev/worker-3", order("/nodes?sort=-node")[0])
	assert.Equal(t, "sind/dev/worker-3", order("/nodes?sort=-ip")[0])
	assert.Equal(t, "sind/dev/controller", order("/nodes?sort=-realm")[0], "the nodes of a realm keep their order")
	byRole := order("/nodes?sort=role")
	assert.Equal(t, []string{"ci/default/controller", "sind/dev/controller-backup", "sind/dev/controller", "sind/hpc/controller", "sind/dev/db"}, byRole[:5])
	hidden := 0
	for _, k := range order("/nodes?q=slurmd+down") {
		if k[0] == '-' {
			hidden++
		}
	}
	assert.Equal(t, 14, hidden, "only worker-1 of dev has slurmd down")
	assert.Equal(t, http.StatusBadRequest, ts.get("/nodes?sort=issues").Code)
	assert.Equal(t, http.StatusBadRequest, ts.get("/nodes?state=problem2").Code)
	assert.Equal(t, http.StatusBadRequest, ts.get("/nodes?realm=-&q=x").Code)
}

func TestPgListLive(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	// The fragments of a list render with the query of the page.
	v := ts.srv.newView(Params{"q": {"hpc"}, "state": {"running"}})
	for _, name := range []string{"pg-clusters-head", "pg-clusters-chips", "pg-clusters-table"} {
		html, err := ts.srv.renderFragment(ts.srv.reg.fragments[name], v)
		require.NoError(t, err, name)
		assert.NotEmpty(t, html, name)
	}
	html, err := ts.srv.renderFragment(ts.srv.reg.fragments["pg-clusters-table"], v)
	require.NoError(t, err)
	assert.Equal(t, []string{"sind/hpc"}, pgRowOrder(html))
	html, err = ts.srv.renderFragment(ts.srv.reg.fragments["pg-nodes-table"], ts.srv.newView(Params{"q": {"hpc"}}))
	require.NoError(t, err)
	assert.Contains(t, html, `data-href="/nodes/sind/hpc/worker-0" data-pg-text="worker-0.hpc hpc sind worker running 172.20.0.3">`)
	assert.Contains(t, html, `data-href="/nodes/sind/dev/worker-0" data-pg-text="worker-0.dev dev sind worker running 172.20.0.7" hidden>`)
	_, err = ts.srv.renderFragment(ts.srv.reg.fragments["pg-nodes-chips"], ts.srv.newView(Params{"state": {"x"}}))
	require.ErrorIs(t, err, ErrBadRequest)
}

func TestPgListQuery(t *testing.T) {
	q := pgListQuery{Base: "/nodes", def: "cluster", Sort: "cluster"}
	assert.Equal(t, "/nodes?realm=", q.URL())
	assert.Equal(t, "", q.SortParam())
	assert.Equal(t, "/nodes?realm=ci", q.RealmURL("ci"))
	q.Desc = true
	assert.Equal(t, "-cluster", q.SortParam())
	q.Sort, q.Desc, q.Q = "ip", false, "a b"
	assert.Equal(t, "ip", q.SortParam())
	assert.Equal(t, "/nodes?q=a+b&realm=&sort=ip", q.URL())
	assert.Equal(t, "/nodes?realm=&sort=ip", q.URL("q", ""))
	assert.Equal(t, []string{"a", "b"}, q.words())
	assert.True(t, pgMatch("a b c", []string{"c", "a"}))
	assert.False(t, pgMatch("a b c", []string{"d"}))
	assert.Equal(t, "/nodes?realm=", pgList{pgListQuery: q}.ClearURL()[:len("/nodes?realm=")])
}

func TestPgRanks(t *testing.T) {
	down := []pgSvc{{"slurmd", false}}
	for want, c := range []pgCluster{
		{State: cluster.StateStopped},
		{State: cluster.StateUnknown},
		{State: cluster.StateMixed},
		{State: cluster.StatePaused},
		{State: cluster.StateRunning, Nodes: []pgNode{{State: cluster.StateRunning, Services: down}}},
		{State: cluster.StateRunning},
	} {
		assert.Equal(t, want, pgClusterRank(c), c.State)
	}
	for want, n := range []pgNode{
		{State: cluster.StateStopped},
		{State: cluster.StateUnknown},
		{State: cluster.StatePaused},
		{State: cluster.StateRunning, Services: down},
		{State: cluster.StateRunning},
	} {
		assert.Equal(t, want, pgNodeRank(n), n.State)
	}
	assert.True(t, pgCluster{State: cluster.StateRunning, Nodes: []pgNode{{State: cluster.StateStopped}}}.NeedsLook())
	assert.False(t, pgCluster{State: cluster.StateRunning}.NeedsLook())
	assert.Equal(t, 1, pgIP(pgNode{IP: ""}).Compare(pgIP(pgNode{IP: "10.0.0.1"})), "a node without an address sorts last")
	assert.Equal(t, 0, pgClusterCmp("name")(pgCluster{Name: "a", Realm: "r"}, pgCluster{Name: "a", Realm: "r"}))
	assert.Equal(t, -1, pgNodeCmp("role")(pgNode{Role: config.RoleDB}, pgNode{Role: config.RoleWorker}))
}
