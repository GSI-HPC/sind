// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPgOverviewEmpty(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/", 200)
	assert.Contains(t, body, "<h1>Overview</h1>")
	assert.Contains(t, body, "0 realms")
	assert.Contains(t, body, "sind ui has not read Docker yet")
	assert.Contains(t, body, "No realm on this Docker daemon yet")
	assert.Contains(t, body, `href="/dialogs/create-cluster"`)
	assert.Contains(t, body, "Nothing needs you.")
	assert.Contains(t, body, "No runs yet.")

	ts.st.SetDocker(store.DockerState{Checked: true, Err: "Cannot connect to the Docker daemon"})
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, "Docker is not reachable.")
	assert.Contains(t, body, "Cannot connect to the Docker daemon")
	assert.Contains(t, body, `<div class="pg-tt">Docker is not reachable</div>`)
	assert.NotContains(t, body, "has not read Docker yet")
}

func TestPgOverview(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/", 200)

	assert.Contains(t, body, "2 realms")
	assert.Contains(t, body, "3 clusters · 15 nodes")
	assert.Contains(t, body, "2 nodes need a look")
	assert.NotContains(t, body, "Docker is not reachable")

	// Realms with health, clusters as cards.
	assert.Contains(t, body, `<a href="/?realm=sind"><b>sind</b></a>`)
	assert.Contains(t, body, "1 node is not running")
	assert.Contains(t, body, "a command runs")
	assert.Contains(t, body, `<a class="panel pg-ccard" href="/clusters/sind/dev">`)
	assert.Contains(t, body, `<span class="pill warn">mixed</span>`)
	assert.Contains(t, body, "Slurm 26.05.4")
	assert.Contains(t, body, "1 controller · 1 controller-backup · 1 db · 1 api · 1 submitter · 4 workers")
	assert.Contains(t, body, `<span class="chip brand">on</span>`)
	assert.Contains(t, body, `<span class="pill bad">worker-3: stopped</span>`)
	assert.Contains(t, body, `class="pg-nm pg-nm-controller pg-nm-running"`)
	assert.Contains(t, body, `class="pg-nm pg-nm-controller-backup pg-nm-running"`)
	assert.Contains(t, body, `class="pg-nm pg-nm-worker pg-nm-stopped"`)
	assert.Contains(t, body, "create worker dev runs now")

	// Needs you: the worst first, with what to do.
	needs := regexp.MustCompile(`class="pg-tt">([^<]+)<`).FindAllStringSubmatch(body, -1)
	var titles []string
	for _, m := range needs {
		titles = append(titles, m[1])
	}
	assert.Equal(t, []string{
		"worker-3.dev is stopped", "worker-0 is paused",
	}[:1], titles[:1])
	assert.Contains(t, titles, "create cluster hpc2 failed 2 h ago")
	assert.Contains(t, titles, "Doctor: cgroupv2")
	assert.Contains(t, titles, "Doctor: inotify")
	assert.Contains(t, titles, "A command holds the lock of realm sind")
	assert.NotContains(t, titles, "create cluster ancient failed 3 d ago", "a failure of three days ago is old news")
	assert.Equal(t, titles[len(titles)-1], "Doctor: inotify", "warnings come last")
	assert.Contains(t, body, "sind create worker dev (pid 4242 on zeta, since")
	assert.Contains(t, body, `href="/dialogs/power?cluster=dev&amp;nodes=worker-3&amp;op=start&amp;realm=sind" hx-get="/dialogs/power?cluster=dev&amp;nodes=worker-3&amp;op=start&amp;realm=sind" hx-target="#layer"`)
	assert.Contains(t, body, `>Unfreeze…</a>`)
	assert.Contains(t, body, `href="/clusters/sind/dev?tab=logs"`)
	assert.Contains(t, body, `href="/runs/r4"`)
	assert.Contains(t, body, "node controller: timed out")
	assert.Contains(t, body, `href="/doctor"`)

	// Recent runs: newest first, six at most, terminal runs marked.
	assert.Contains(t, body, `<a class="pg-r" href="/runs/r5"><span class="dot run"></span><span class="pg-c">create worker dev</span><span class="chip">terminal</span><span class="pg-tm">1:35.0</span></a>`)
	assert.Contains(t, body, `<span class="dot bad"></span><span class="pg-c">create cluster hpc2</span><span class="pg-tm">2 h ago</span>`)
	assert.Contains(t, body, `<span class="dot ok"></span><span class="pg-c">delete cluster old</span><span class="pg-tm">19 h ago</span>`)
	assert.Contains(t, body, `<span class="dot warn"></span><span class="pg-c">power reboot worker-1.dev</span><span class="chip">terminal</span><span class="pg-tm">1 d ago</span>`)

	// Quick actions: the dialogs of the actions part.
	assert.Contains(t, body, `href="/dialogs/create-cluster"`)
	assert.Contains(t, body, `href="/dialogs/create-worker?cluster=default&amp;realm=ci"`)
	assert.Contains(t, body, `href="/dialogs/connect"`)

	// Narrowed to one realm.
	body = pgBody(t, ts, "/?realm=ci", 200)
	assert.Contains(t, body, "1 cluster · 2 nodes")
	assert.Contains(t, body, "showing <b class=\"mono\">ci</b>")
	assert.Contains(t, body, `href="/clusters/ci/default"`)
	assert.NotContains(t, body, `href="/clusters/sind/dev"`)
	assert.NotContains(t, body, "A command holds the lock")
	assert.Contains(t, body, `<tr class="sel"><td class="mono"><a href="/?realm=ci">`)
	assert.Contains(t, body, `href="/dialogs/create-cluster?realm=ci"`)

	// A realm without clusters.
	body = pgBody(t, ts, "/?realm=none", 200)
	assert.Contains(t, body, `No cluster in realm <b class="mono">none</b> yet.`)
	assert.Contains(t, body, `class="btn primary"`)

	// Only realm name is checked.
	assert.Equal(t, http.StatusBadRequest, ts.get("/?realm=Not%20a%20realm").Code)
}

func TestPgOverviewOneRealmPassesItOn(t *testing.T) {
	ts := pgServer(t)
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "solo"}})
	body := pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `href="/dialogs/create-cluster?realm=solo"`)
	assert.Contains(t, body, "No cluster yet.")
	assert.NotContains(t, body, "create-worker")
}

func TestPgClusters(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/clusters", 200)
	assert.Contains(t, body, "<h1>Clusters</h1>")
	assert.Contains(t, body, "No cluster yet.")
	assert.Contains(t, body, `sse-swap="pg-clusters-table"`)

	pgFillStore(ts.st)
	body = pgBody(t, ts, "/clusters", 200)
	assert.Contains(t, body, "Every cluster of every realm.")
	assert.Contains(t, body, "<th>Realm</th>")
	assert.Contains(t, body, `<tr class="click" data-href="/clusters/sind/dev"><td><a href="/clusters/sind/dev"><b>dev</b></a></td><td class="mono">sind</td><td><span class="pill warn">mixed</span></td><td class="mono">26.05.4</td><td class="num">9</td>`)
	assert.Contains(t, body, `data-href="/clusters/ci/default"`)
	assert.Contains(t, body, `<span class="pill warn">paused</span>`)
	assert.Contains(t, body, `<span class="pill ok">running</span>`)
	assert.Contains(t, body, `href="/dialogs/delete-cluster?cluster=dev&amp;realm=sind" hx-get=`)
	assert.Contains(t, body, "sind get clusters")
	// The filter links to every realm.
	assert.Contains(t, body, `<a class="on" aria-current="true" href="/clusters?realm=">All realms</a>`)
	assert.Contains(t, body, `<a href="/clusters?realm=ci">ci · 1</a>`)
	assert.Contains(t, body, `<a href="/clusters?realm=sind">sind · 2</a>`)

	body = pgBody(t, ts, "/clusters?realm=sind", 200)
	assert.Contains(t, body, "Every cluster of realm <b class=\"mono\">sind</b>.")
	assert.NotContains(t, body, "<th>Realm</th>")
	assert.NotContains(t, body, "/clusters/ci/default")
	assert.Contains(t, body, `<a class="on" aria-current="true" href="/clusters?realm=sind">sind · 2</a>`)
	assert.Contains(t, body, `href="/dialogs/create-cluster?realm=sind"`)

	body = pgBody(t, ts, "/clusters?realm=nowhere", 200)
	assert.Contains(t, body, `No cluster in realm <b class="mono">nowhere</b> yet.`)
	assert.Contains(t, body, `href="/clusters?realm=">All realms</a>`)
	assert.Equal(t, http.StatusBadRequest, ts.get("/clusters?realm=A_B").Code)
}

func TestPgNodes(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/nodes", 200)
	assert.Contains(t, body, "No node yet.")
	pgFillStore(ts.st)
	body = pgBody(t, ts, "/nodes", 200)
	assert.Contains(t, body, "<h1>Nodes</h1>")
	assert.Contains(t, body, `<tr class="click" data-href="/nodes/sind/dev/worker-3"><td class="mono"><a href="/nodes/sind/dev/worker-3"><b>worker-3.dev</b></a></td><td><a href="/clusters/sind/dev">dev</a></td><td class="mono">sind</td>`)
	assert.Contains(t, body, `<span class="pill bad">stopped</span> </td>`)
	assert.Contains(t, body, `<span class="pill warn">paused</span> </td>`)
	assert.Contains(t, body, `<b>worker-0</b>`, "a node of the default cluster has its short name")
	assert.Contains(t, body, `<span class="chip brand">controller</span>`)
	assert.Contains(t, body, "<td class=\"mono\">172.20.0.2</td>")
	assert.Contains(t, body, "sind get nodes")

	body = pgBody(t, ts, "/nodes?realm=ci", 200)
	assert.NotContains(t, body, "<th>Realm</th>")
	assert.NotContains(t, body, "worker-3.dev")
	assert.Contains(t, body, "<b>controller</b>")
	body = pgBody(t, ts, "/nodes?realm=zz", 200)
	assert.Contains(t, body, `No node in realm <b class="mono">zz</b> yet.`)
	assert.Equal(t, http.StatusBadRequest, ts.get("/nodes?realm=-").Code)
}

func TestPgRealmCookie(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)

	// Picking a realm sets the cookie and goes back to the list.
	w := ts.post("/realm", "realm=ci&next=/nodes")
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/nodes", w.Header().Get("Location"))
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		if x.Name == "sind_realm" {
			c = x
		}
	}
	require.NotNil(t, c)
	assert.Equal(t, "ci", c.Value)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, "/", c.Path)
	assert.Positive(t, c.MaxAge)

	// A list page without ?realm= uses the cookie, ?realm= wins, and an
	// empty ?realm= shows all realms.
	get := func(target string, cookie string) string {
		r := request("GET", target)
		r.AddCookie(ts.cookie)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "sind_realm", Value: cookie})
		}
		w := ts.do(r)
		require.Equal(t, 200, w.Code)
		return w.Body.String()
	}
	body := get("/clusters", "ci")
	assert.Contains(t, body, `href="/clusters/ci/default"`)
	assert.NotContains(t, body, `href="/clusters/sind/dev"`)
	assert.Contains(t, body, `<option value="ci" selected>ci · 1</option>`)
	assert.Contains(t, body, `<input type="hidden" name="next" value="/clusters">`)
	body = get("/clusters?realm=sind", "ci")
	assert.Contains(t, body, `href="/clusters/sind/dev"`)
	assert.NotContains(t, body, `href="/clusters/ci/default"`)
	body = get("/clusters?realm=", "ci")
	assert.Contains(t, body, `href="/clusters/sind/dev"`)
	assert.Contains(t, body, `href="/clusters/ci/default"`)
	assert.Contains(t, body, `<option value="" selected>all realms</option>`)
	// The cookie is the user's: a bad one is ignored.
	body = get("/nodes", "Not a realm")
	assert.Contains(t, body, "worker-3.dev")
	body = get("/nodes", "")
	assert.Contains(t, body, "worker-3.dev")
	// The live updates carry the realm of the page.
	m := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(get("/nodes", "ci"))
	require.NotNil(t, m)
	events, err := url.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
	require.NoError(t, err)
	assert.Equal(t, "ci", events.Query().Get("realm"))
	assert.Equal(t, "nodes", events.Query().Get("nav"))
	assert.Contains(t, events.Query()["f"], "pg-nodes-table")

	// The Overview ignores the cookie.
	body = get("/", "ci")
	assert.Contains(t, body, `href="/clusters/sind/dev"`)

	// htmx is sent on by a header, which it follows by itself.
	r := request("POST", "/realm")
	r.Body = nopCloser("realm=sind&next=/nodes")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://127.0.0.1:7878")
	r.Header.Set(csrfHeader, ts.csrf)
	r.Header.Set("HX-Request", "true")
	r.AddCookie(ts.cookie)
	w = ts.do(r)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "/nodes", w.Header().Get("HX-Redirect"))
	assert.Equal(t, "sind", w.Result().Cookies()[0].Value)

	// Every realm: the cookie goes away.
	w = ts.post("/realm", "realm=&next=/clusters")
	require.Equal(t, http.StatusSeeOther, w.Code)
	for _, x := range w.Result().Cookies() {
		if x.Name == "sind_realm" {
			assert.Negative(t, x.MaxAge)
		}
	}
	// Only a list page is a place to return to; a bad name is refused.
	w = ts.post("/realm", "realm=sind&next=https://evil.example/")
	assert.Equal(t, "/clusters", w.Header().Get("Location"))
	w = ts.post("/realm", "realm=sind&next=//evil.example")
	assert.Equal(t, "/clusters", w.Header().Get("Location"))
	w = ts.post("/realm", "realm=sind&next=/topology")
	assert.Equal(t, "/topology", w.Header().Get("Location"))
	assert.Equal(t, http.StatusBadRequest, ts.post("/realm", "realm=a%20b").Code)
	assert.Equal(t, http.StatusBadRequest, ts.post("/realm", "realm="+strings.Repeat("a", 300)).Code)
}

func TestPgRealmPicker(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/clusters", 200)
	assert.Contains(t, body, `<form class="realm" method="post" action="/realm" hx-post="/realm" hx-trigger="change" hx-swap="none">`)
	assert.Contains(t, body, `<select id="realm-sel" name="realm" aria-label="Realm"><option value="" selected>all realms</option></select>`)
	assert.Contains(t, body, `name="csrf" value="`+ts.csrf+`"`)
	pgFillStore(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.Contains(t, body, `<option value="sind" selected>sind · 2</option>`)
	assert.Contains(t, body, `<option value="ci">ci · 1</option>`)
	assert.Contains(t, body, `<input type="hidden" name="next" value="/clusters">`, "a cluster page returns to the clusters")
	// A realm that the snapshot does not hold yet is still the realm shown.
	body = pgBody(t, ts, "/nodes?realm=future", 200)
	assert.Contains(t, body, `<option value="future" selected>future</option>`)
	assert.Contains(t, body, `value="/nodes"`)
}

func TestPgRail(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/", 200)
	assert.NotContains(t, body, `Clusters<span`)
	assert.NotContains(t, body, `Nodes<span`)
	pgFillStore(ts.st)
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `Clusters<span class="n">3</span>`)
	assert.Contains(t, body, `Nodes<span class="n bad">2</span>`)
	assert.Contains(t, body, `<a class="nav on" href="/" aria-current="page">`)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-0", 200)
	assert.Contains(t, body, `<a class="nav on" href="/nodes" aria-current="page">`)
	body = pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.Contains(t, body, `<a class="nav on" href="/clusters" aria-current="page">`)
}

func TestPgClusterSummary(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.Contains(t, body, "<title>dev · sind ui</title>")
	assert.Contains(t, body, `<div class="crumbs"><a href="/clusters">Clusters</a> / <a href="/clusters?realm=sind">sind</a> / dev</div>`)
	assert.Contains(t, body, `<h1>dev <span class="pill warn">mixed</span></h1>`)
	assert.Contains(t, body, `<span class="mono">Slurm 26.05.4</span>`)
	assert.Contains(t, body, `<div class="callout run pg-run">`)
	assert.Contains(t, body, `<b class="mono">create worker dev</b> runs in a terminal.`)
	assert.Contains(t, body, `<a class="btn sm" href="/runs/r5">Show progress</a>`)
	assert.Contains(t, body, `href="/dialogs/create-worker?cluster=dev&amp;realm=sind"`)
	assert.Contains(t, body, `class="btn danger" href="/dialogs/delete-cluster?cluster=dev&amp;realm=sind"`)
	// The tabs, without Slurm.
	assert.Contains(t, body, `<a class="tab on" href="/clusters/sind/dev" aria-current="page">Summary</a>`)
	assert.Contains(t, body, `<a class="tab" href="/clusters/sind/dev?tab=nodes">Nodes<span class="n bad">!</span></a>`)
	assert.Contains(t, body, `<a class="tab" href="/clusters/sind/dev?tab=logs">Logs</a>`)
	assert.Contains(t, body, `>Network and storage</a>`)
	assert.Contains(t, body, `?tab=connect">Connect</a>`)
	assert.NotContains(t, body, "Slurm</a>")
	// Without a status: the tiles and a note.
	assert.Contains(t, body, "Service health appears once sind ui has probed the cluster.")
	assert.Contains(t, body, "Network health appears once")
	assert.Contains(t, body, `<a class="pg-nm pg-nm-worker pg-nm-stopped" href="/nodes/sind/dev/worker-3" title="worker-3 · worker · stopped" aria-label="worker-3.dev, stopped"></a>`)

	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.NotContains(t, body, "Service health appears once")
	assert.Contains(t, body, `<th class="mono pg-svc">munge</th><th class="mono pg-svc">sshd</th><th class="mono pg-svc">slurmctld</th><th class="mono pg-svc">slurmd</th><th class="mono pg-svc">mariadb</th><th class="mono pg-svc">slurmdbd</th><th class="mono pg-svc">slurmrestd</th><th class="mono pg-svc">sackd</th>`)
	// controller: up on munge, sshd and slurmctld, not on the others.
	assert.Contains(t, body, `<span class="pg-ck y">✓</span></td><td><span class="pg-ck y">✓</span></td><td><span class="pg-ck y">✓</span></td><td><span class="pg-ck na">·</span>`)
	assert.Contains(t, body, `<span class="pg-ck n">✗</span>`, "slurmd is down on worker-1")
	assert.Contains(t, body, `<span class="pg-ck na">–</span>`, "a node that does not run has no result")
	assert.Contains(t, body, `class="pg-nm pg-nm-worker pg-nm-running pg-nm-svcdown"`)
	// The cluster, with the data mount and the pair of controllers.
	assert.Contains(t, body, `<dd class="mono">/scratch/dev <span class="faint">(host directory) → /data</span></dd>`)
	assert.Contains(t, body, `<span class="chip">controller: primary, in control</span><span class="chip">controller-backup: backup, standby</span>`)
	assert.Contains(t, body, `<dd class="mono">sind-dev-net <span class="pill ok">ok</span></dd>`)
	assert.Contains(t, body, `<dt>subnet</dt><dd class="mono">172.20.0.0/16</dd>`)
	assert.Contains(t, body, `<dt>DNS</dt><dd><span class="pill ok">ok</span></dd>`)
	assert.NotContains(t, body, "grafana, prometheus", "dev has no monitoring")

	// A cluster with monitoring and a status with a volume as its data.
	ts.st.SetStatus("sind", "hpc", &cluster.Status{
		Name: "hpc", Network: &cluster.NetworkHealth{DNS: false}, Mounts: []cluster.MountPoint{{Path: "/data", Source: "sind-hpc-data", Type: "volume", OK: true}},
	})
	body = pgBody(t, ts, "/clusters/sind/hpc", 200)
	assert.Contains(t, body, `<span class="chip brand">monitoring</span>`)
	assert.Contains(t, body, "grafana, prometheus, loki, alloy")
	assert.Contains(t, body, `<span class="faint">(volume) → /data</span>`)
	assert.Contains(t, body, `<dt>DNS</dt><dd><span class="pill bad">down</span></dd>`)
	assert.NotContains(t, body, "callout run", "no run touches hpc")
	assert.NotContains(t, body, `n bad">!`, "every node of hpc runs")
	ts.st.SetStatus("sind", "hpc", &cluster.Status{Name: "hpc"})
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/hpc", 200), "No network data.")
}

func TestPgClusterNotFound(t *testing.T) {
	ts := pgServer(t)
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev", 404), "Not found")
	pgFillStore(ts.st)
	for _, target := range []string{"/clusters/sind/nope", "/clusters/nope/dev", "/nodes/sind/dev/nope", "/nodes/sind/nope/worker-0", "/nodes/none/dev/worker-0"} {
		assert.Contains(t, pgBody(t, ts, target, 404), "Nothing lives at this address", target)
	}
	body := pgBody(t, ts, "/clusters/sind/dev?tab=slurm", 400)
	assert.Contains(t, body, `unknown tab "slurm"`)
}

func TestPgClusterNodesTab(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200)
	assert.Contains(t, body, `<a class="tab on" href="/clusters/sind/dev?tab=nodes" aria-current="page">Nodes<span class="n bad">!</span></a>`)
	assert.Contains(t, body, `<tr data-pg-node="worker-3" data-pg-role="worker"><td><input type="checkbox" data-pg-sel="worker-3" aria-label="Select worker-3"></td>`)
	assert.Contains(t, body, `<div class="pg-selbar" data-pg-selbar hidden>`)
	assert.Contains(t, body, `<a class="btn sm" href="/dialogs/power?cluster=dev&amp;op=start&amp;realm=sind" data-pg-dialog>Start…</a>`)
	assert.Contains(t, body, `op=stop&amp;realm=sind" data-pg-dialog>Shut down…`)
	assert.Contains(t, body, `op=reboot&amp;realm=sind" data-pg-dialog>Reboot…`)
	assert.Contains(t, body, `op=pause&amp;realm=sind" data-pg-dialog>Freeze…`)
	assert.Contains(t, body, `op=unpause&amp;realm=sind" data-pg-dialog>Unfreeze…`)
	assert.Contains(t, body, `<a class="btn sm danger" href="/dialogs/delete-worker?cluster=dev&amp;realm=sind" data-pg-dialog data-pg-workers>Delete…</a>`)
	assert.Contains(t, body, `<span class="pill bad">stopped</span> </td>`)
	assert.Contains(t, body, `<span class="faint">–</span>`)
	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200)
	assert.Contains(t, body, `<span class="pill bad">slurmd down</span>`)
	assert.Contains(t, body, `<span class="faint">3 up</span>`)
}

func TestPgClusterNetworkTab(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/clusters/sind/dev?tab=network", 200)
	// Before the first probe: the networks and volumes of the realm.
	assert.Contains(t, body, `<tr><td class="mono">sind-mesh</td><td class="mono">bridge</td><td class="mono">172.18.0.0/16</td><td class="mono">172.18.0.1</td></tr>`)
	assert.Contains(t, body, `<td class="mono">sind-dev-net <span class="chip">cluster</span></td>`)
	assert.NotContains(t, body, "sind-hpc-net")
	assert.Contains(t, body, `<tr><td class="mono">sind-dev-config</td><td class="mono">local</td></tr>`)
	assert.NotContains(t, body, "sind-hpc-munge")
	assert.NotContains(t, body, "Jobs write to")

	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=network", 200)
	assert.Contains(t, body, `<tr><td class="mono">/etc/slurm</td><td class="mono">sind-dev-config</td><td><span class="pill ok">ok</span></td></tr>`)
	assert.Contains(t, body, `<tr><td class="mono">/home</td><td class="mono">sind-dev-home</td><td><span class="pill bad">down</span></td></tr>`)
	assert.Contains(t, body, `Jobs write to <span class="mono">/scratch/dev</span> on this host as root.`)

	// A status without the realm's networks in the snapshot: its own.
	ts.st.SetNetworks("sind", nil)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=network", 200)
	assert.Contains(t, body, `<td class="mono">sind-dev-net <span class="chip">cluster</span></td><td class="mono">bridge</td><td class="mono">172.20.0.0/16</td>`)
	assert.Contains(t, body, `<td class="mono">sind-mesh</td>`)

	// Nothing read yet.
	ts.st.SetStatus("sind", "dev", nil)
	ts.st.SetVolumes("sind", nil)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=network", 200)
	assert.Contains(t, body, "No network read yet.")
	assert.Contains(t, body, "No volume or mount read yet.")
}

func TestPgClusterConnectTab(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/clusters/sind/dev?tab=connect", 200)
	cmds := regexp.MustCompile(`<code>([^<]+)</code>`).FindAllStringSubmatch(body, -1)
	var got []string
	for _, m := range cmds {
		got = append(got, m[1])
	}
	assert.Equal(t, []string{
		"sind enter dev", "sind exec dev -- sinfo", "sind ssh worker-0.dev", "sind logs worker-0.dev -f -n 200",
		`ssh -F &#34;$(sind get ssh-config)&#34; worker-0.dev.sind.sind`,
	}, got)
	assert.Contains(t, body, "Open a shell on the submitter")
	assert.Contains(t, body, `<dt>slurmrestd</dt><dd class="mono">http://api.dev.sind.sind:6820</dd>`)
	assert.NotContains(t, body, "Grafana")
	assert.Contains(t, body, `<button type="button" data-pg-copy>Copy</button>`)
	assert.Contains(t, body, "There is no terminal in the browser.")

	// A cluster of another realm and the default cluster, with monitoring.
	body = pgBody(t, ts, "/clusters/ci/default?tab=connect", 200)
	assert.Contains(t, body, "<code>sind --realm ci enter</code>")
	assert.Contains(t, body, "Open a shell on the controller")
	assert.Contains(t, body, "<code>sind --realm ci ssh worker-0</code>")
	assert.Contains(t, body, "No api node, so no slurmrestd.")
	body = pgBody(t, ts, "/clusters/sind/hpc?tab=connect", 200)
	assert.Contains(t, body, `<dt>Grafana</dt><dd class="mono">http://grafana.sind.sind:3000</dd>`)
	assert.Contains(t, body, "No api node")

	// No node, no commands.
	ts.st.SetNodes("sind", nil)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=connect", 200)
	assert.Contains(t, body, "<code>sind enter dev</code>")
	assert.NotContains(t, pgBody(t, ts, "/clusters/sind/dev", 200), "pg-nm pg-nm-")
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev", 200), "no nodes")
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev", 200), "This cluster has no node.")
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200), "This cluster has no node.")
}

// pgLogsTab is what the logs part adds: its block, and any file name.
func TestPgLogsTabBlock(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	// The Logs tab's body is the logs part's block, and only that tab has it.
	body := pgBody(t, ts, "/clusters/sind/dev?tab=logs", 200)
	assert.Contains(t, body, `<a class="tab on" href="/clusters/sind/dev?tab=logs" aria-current="page">Logs</a>`)
	assert.Contains(t, body, `data-logs data-realm="sind" data-cluster="dev"`)
	assert.NotContains(t, pgBody(t, ts, "/clusters/sind/dev", 200), "data-logs ")
}

func TestPgNodePage(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/nodes/sind/dev/worker-3", 200)
	assert.Contains(t, body, "<title>worker-3.dev · sind ui</title>")
	assert.Contains(t, body, `<div class="crumbs"><a href="/nodes">Nodes</a> / <a href="/clusters/sind/dev">dev</a> / worker-3</div>`)
	assert.Contains(t, body, `<h1>worker-3.dev <span class="pill bad">stopped</span></h1>`)
	assert.Contains(t, body, `<span class="chip ">worker</span>`)
	assert.Contains(t, body, `<span class="mono">172.20.0.10</span>`)
	assert.Contains(t, body, `op=start&amp;realm=sind" hx-get=`)
	assert.Contains(t, body, `nodes=worker-3&amp;op=start`)
	assert.Contains(t, body, `class="btn danger" href="/dialogs/delete-worker?cluster=dev&amp;nodes=worker-3&amp;realm=sind"`)
	assert.Contains(t, body, `<h2>Issues</h2>`)
	assert.Contains(t, body, `<div class="pg-issue"><span class="pill bad">stopped</span></div>`)
	assert.Contains(t, body, `<a class="btn sm" href="/clusters/sind/dev?tab=logs">Logs</a>`)
	assert.Contains(t, body, "No probe result yet.")
	assert.Contains(t, body, `<dt>name</dt><dd class="mono">sind-dev-worker-3</dd>`)
	assert.Contains(t, body, `<a href="/clusters/sind/dev">dev</a> <span class="pill warn">mixed</span>`)
	assert.Contains(t, body, `<dd class="mono">worker-3.dev.sind.sind <button type="button" class="btn sm" data-pg-copy-text="worker-3.dev.sind.sind">Copy</button></dd>`)
	assert.Contains(t, body, "<code>sind ssh worker-3.dev</code>")
	assert.Contains(t, body, "<code>sind logs worker-3.dev slurmd -f -n 200</code>")

	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-3", 200)
	assert.Contains(t, body, `<dt class="mono">slurmd</dt><dd><span class="pill">not running</span></dd>`)
	assert.Contains(t, body, `<dt>managed</dt><dd>yes</dd>`)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-1", 200)
	assert.Contains(t, body, `<span class="pill bad">slurmd down</span>`)
	assert.Contains(t, body, `<dt class="mono">slurmd</dt><dd><span class="pill bad">failed</span></dd>`)
	assert.Contains(t, body, `<dt class="mono">munge</dt><dd><span class="pill ok">active</span></dd>`)
	assert.Contains(t, body, `>Shut down…</a>`)
	assert.Contains(t, body, `>Reboot…</a>`)
	assert.Contains(t, body, `>Freeze…</a>`)

	// A controller: no delete, HA position, its service.
	body = pgBody(t, ts, "/nodes/sind/dev/controller", 200)
	assert.Contains(t, body, "HA position primary · in control")
	assert.Contains(t, body, `<span class="chip brand">controller</span>`)
	assert.NotContains(t, body, "delete-worker")
	assert.NotContains(t, body, "<h2>Issues</h2>")
	assert.Contains(t, body, "sind logs controller.dev slurmctld -f")
	body = pgBody(t, ts, "/nodes/sind/dev/controller-backup", 200)
	assert.Contains(t, body, "HA position backup · standby")
	for short, svc := range map[string]string{"db": "slurmdbd", "api": "slurmrestd", "submitter": "sackd"} {
		assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/"+short, 200), "sind logs "+short+".dev "+svc)
	}

	// A paused node of the default cluster, in another realm.
	body = pgBody(t, ts, "/nodes/ci/default/worker-0", 200)
	assert.Contains(t, body, `<span class="pill warn">paused</span>`)
	assert.Contains(t, body, `>Unfreeze…</a>`)
	assert.Contains(t, body, "<code>sind --realm ci ssh worker-0</code>")
	assert.NotContains(t, body, ">Shut down…</a>")
}

func TestPgNodeWithoutHealthData(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	// A status that lists no health for the node, and an unmanaged one.
	ts.st.SetStatus("sind", "hpc", &cluster.Status{Name: "hpc", Nodes: []*cluster.NodeStatus{{Name: "controller.hpc"}}})
	body := pgBody(t, ts, "/nodes/sind/hpc/controller", 200)
	assert.Contains(t, body, "This node has no service that sind checks.")
	assert.Contains(t, body, `<dt>managed</dt><dd>no</dd>`)
}

func TestPgLiveFragments(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	for target, frag := range map[string]string{
		"/":                                 "pg-overview-needs",
		"/clusters":                         "pg-clusters-table",
		"/nodes":                            "pg-nodes-table",
		"/clusters/sind/dev":                "pg-cluster-body",
		"/nodes/sind/dev/worker-0":          "pg-node-body",
		"/clusters/sind/dev?tab=connect":    "pg-cluster-head",
		"/nodes/sind/dev/worker-0?x=ignore": "pg-node-head",
	} {
		body := pgBody(t, ts, target, 200)
		m := regexp.MustCompile(`sse-connect="([^"]+)"`).FindStringSubmatch(body)
		require.NotNil(t, m, target)
		events, err := url.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
		require.NoError(t, err)
		assert.Contains(t, events.Query()["f"], frag, target)
		assert.Contains(t, body, `sse-swap="`+frag+`"`, target)
	}
}

func TestPgLiveCluster(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	ts.srv.coalesce = 10 * time.Millisecond
	body := pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200)
	m := regexp.MustCompile(`sse-connect="/events\?([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, m)
	query := strings.ReplaceAll(m[1], "&amp;", "&")
	c := ts.connect(query)

	// A status changes the body; the head says the same.
	pgFillStatus(ts.st)
	e := c.next()
	assert.Equal(t, "pg-cluster-body", e.name)
	assert.Contains(t, e.data, `<span class="pill bad">slurmd down</span>`)
	c.quiet(60 * time.Millisecond)

	// A run changes the head only.
	ts.st.SetRuns([]store.Run{{ID: "n", Title: "create worker dev", Start: pgNow}})
	e = c.next()
	assert.Equal(t, "pg-cluster-head", e.name)
	assert.Contains(t, e.data, "create worker dev")
	c.quiet(60 * time.Millisecond)

	// The cluster goes away: the page says so.
	ts.st.SetClusters("sind", nil)
	got := map[string]string{}
	for range 3 { // head, body and rail
		e := c.next()
		got[e.name] = e.data
	}
	assert.Contains(t, got["pg-cluster-head"], "no longer exists")
	assert.NotContains(t, got["pg-cluster-body"], "<table")
}

func TestPgLiveLogsTabIsLeftAlone(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	ts.srv.coalesce = 10 * time.Millisecond
	body := pgBody(t, ts, "/clusters/sind/dev?tab=logs", 200)
	m := regexp.MustCompile(`sse-connect="/events\?([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, m)
	c := ts.connect(strings.ReplaceAll(m[1], "&amp;", "&"))
	ts.st.SetNodes("sind", nil)
	for range 2 { // head and rail
		assert.NotEqual(t, "pg-cluster-body", c.next().name)
	}
	c.quiet(60 * time.Millisecond)
}

func TestPgLiveNode(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	ts.srv.coalesce = 10 * time.Millisecond
	body := pgBody(t, ts, "/nodes/sind/dev/worker-3", 200)
	m := regexp.MustCompile(`sse-connect="/events\?([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, m)
	c := ts.connect(strings.ReplaceAll(m[1], "&amp;", "&"))
	ts.st.SetNodes("sind", nil)
	got := map[string]string{}
	for range 3 { // head, body and rail
		e := c.next()
		got[e.name] = e.data
	}
	assert.Contains(t, got["pg-node-head"], "worker-3</b> no longer exists")
	assert.Contains(t, got, "pg-node-body")
}
