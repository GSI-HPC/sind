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
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
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
	assert.NotContains(t, body, "Nothing needs you", "an empty Docker has nothing to say it about")
	assert.NotContains(t, body, `id="needs"`)
	assert.Contains(t, body, "No runs yet.")

	ts.st.SetDocker(store.DockerState{Checked: true, Err: "Cannot connect to the Docker daemon"})
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, "Docker is not reachable.")
	assert.Contains(t, body, "Cannot connect to the Docker daemon")
	assert.Contains(t, body, `<div class="pg-tt">Docker is not reachable</div>`)
	assert.Contains(t, body, `<a class="pill bad nodot pg-sumpill" href="#needs">Docker is not reachable <span aria-hidden="true">→</span></a>`)
	assert.NotContains(t, body, "has not read Docker yet")

	// A realm with a cluster that runs: nothing needs the user.
	ts.st.SetDocker(store.DockerState{Checked: true})
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "solo", Clusters: 1}})
	ts.st.SetClusters("solo", []cluster.Summary{{Name: "default", State: cluster.StateRunning}})
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `<span class="pill ok">Nothing needs you</span>`)
	assert.NotContains(t, body, `id="needs"`)
}

func TestPgOverview(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/", 200)

	assert.Contains(t, body, "2 realms")
	assert.Contains(t, body, "3 clusters · 15 nodes")
	// The head names the problems and goes to "Needs you", which comes
	// before the cards.
	assert.Contains(t, body, `<a class="pill bad nodot pg-sumpill" href="#needs">2 nodes need a look · 1 run failed · 1 host check failed · 1 host warning <span aria-hidden="true">→</span></a>`)
	assert.Less(t, strings.Index(body, `id="needs"`), strings.Index(body, `class="panel pg-ccard"`))
	assert.NotContains(t, body, "Docker is not reachable")

	// Realms with health, clusters as cards.
	assert.Contains(t, body, `<a href="/?realm=sind"><b>sind</b></a>`)
	assert.Contains(t, body, `<a class="btn sm ghost" href="/clusters?realm=sind">Clusters<span class="vh"> of sind</span></a>`)
	assert.Contains(t, body, `<a class="btn sm danger" href="/dialogs/delete-realm?realm=sind" aria-label="Delete realm sind…" hx-get="/dialogs/delete-realm?realm=sind" hx-target="#layer" hx-push-url="false">Delete realm…</a>`)
	assert.Contains(t, body, "1 node needs a look")
	assert.Contains(t, body, "a command runs")
	assert.Contains(t, body, `<a class="panel pg-ccard" href="/clusters/sind/dev">`)
	assert.Contains(t, body, `<span class="pill bad">mixed · 1 needs a look</span>`, "a stopped node is bad, and the pill counts it")
	assert.Contains(t, body, `<span class="pill warn">paused · 1 needs a look</span>`)
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
	assert.Contains(t, body, `href="/clusters/sind/dev?node=worker-3&amp;tab=logs"`, "the logs of the node")
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

	// A probe finds a service down while a command works on dev: it may be
	// starting the service, so it is no problem yet.
	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/", 200)
	assert.NotContains(t, body, "has a service down")
	assert.Contains(t, body, `<span aria-hidden="true">2<span class="u"> need a look</span></span>`)

	// Once the command ended, the node needs a look on every page and in
	// the rail, by the same rule.
	ts.st.SetRuns(nil)
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, "3 nodes need a look · 1 host check failed")
	assert.Contains(t, body, `<div class="pg-tt">worker-1.dev has slurmd down</div><div class="pg-dd">The container runs, but slurmd is not active.</div>`)
	assert.Contains(t, body, `href="/dialogs/power?cluster=dev&amp;nodes=worker-1&amp;op=reboot&amp;realm=sind"`)
	assert.Contains(t, body, `<span class="pill bad">2 nodes need a look</span>`, "the realm row")
	assert.Contains(t, body, `<span aria-hidden="true">3<span class="u"> need a look</span></span><span class="vh">, 3 nodes need a look in all realms</span>`, "the rail: two in sind, one in ci")

	// Narrowed to one realm.
	body = pgBody(t, ts, "/?realm=ci", 200)
	assert.Contains(t, body, "1 cluster · 2 nodes")
	assert.Contains(t, body, `<span class="pg-scope">Showing realm <b class="mono">ci</b> · <a href="/?realm=">Show all</a></span>`)
	assert.Contains(t, body, "<title>Overview · ci · sind ui</title>")
	assert.Contains(t, body, `href="/clusters/ci/default"`)
	assert.NotContains(t, body, `href="/clusters/sind/dev"`)
	assert.NotContains(t, body, "A command holds the lock")
	assert.Contains(t, body, `<tr class="sel"><td class="mono"><a href="/?realm=ci">`)
	assert.Contains(t, body, `href="/dialogs/create-cluster?realm=ci"`)

	// A realm that Docker has no trace of, with the closest realm.
	body = pgBody(t, ts, "/?realm=cix", 404)
	assert.Contains(t, body, "There is no realm cix.")
	assert.Contains(t, body, `href="/?realm=ci">Realm ci</a>`)
	assert.Contains(t, body, `href="/?realm=">All realms</a>`)

	// A realm without clusters.
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "ci", Clusters: 1}, {Name: "none"}})
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
	assert.Contains(t, body, `title="Sort by Realm">Realm`)
	assert.Contains(t, body, `<tr class="click" data-href="/clusters/sind/dev" data-pg-item="sind/dev" data-pg-label="dev (sind)" data-pg-one="/dialogs/delete-cluster?cluster=dev&amp;realm=sind" data-pg-text="dev sind mixed 26.05.4 controller.dev `)
	assert.Contains(t, body, `<td class="c-chk"><input type="checkbox" data-pg-sel="sind/dev" aria-label="Select dev (sind)"></td>
  <td class="c-name"><a href="/clusters/sind/dev"><b>dev</b></a></td><td class="mono" data-label="Realm">sind</td><td class="c-state"><span class="pill bad" data-say="dev is mixed · 1 needs a look." data-say-key="sind/dev">mixed · 1 needs a look</span></td><td class="mono" data-label="Slurm">26.05.4</td><td class="num" data-label="Nodes">9</td>`)
	assert.Contains(t, body, `data-href="/clusters/ci/default"`)
	assert.Regexp(t, `<span class="pill warn" data-say="\S+ is paused · 1 needs a look\." data-say-key="[a-z]+/[a-z0-9-]+">paused · 1 needs a look</span>`, body)
	assert.Regexp(t, `<span class="pill ok" data-say="\S+ is running\." data-say-key="[a-z]+/[a-z0-9-]+">running</span>`, body)
	// A live update that changes the list tells screen readers how many it holds.
	assert.Contains(t, body, `<div class="tw" data-say="3 clusters in every realm." data-say-key="count">`)
	assert.Contains(t, body, `href="/dialogs/delete-cluster?cluster=dev&amp;realm=sind" hx-get=`)
	assert.Contains(t, body, "sind get clusters")
	// The filter links to every realm.
	assert.Contains(t, body, `<a class="on" aria-current="true" href="/clusters?realm=" data-pg-keep>All realms</a>`)
	assert.Contains(t, body, `<a href="/clusters?realm=ci" data-pg-keep>ci · 1</a>`)
	assert.Contains(t, body, `<a href="/clusters?realm=sind" data-pg-keep>sind · 2</a>`)
	// The filter field, the chips and the selection, which deletes several.
	assert.Contains(t, body, `<form class="pg-filter" role="search" method="get" action="/clusters" data-pg-filter-form>
  <input type="hidden" name="realm" value="">
  <input type="search" name="q" value="" placeholder="Filter by name, realm or node" aria-label="Filter the clusters" data-pg-filter`)
	assert.Contains(t, body, `<a class="pg-chip on" href="/clusters?realm=" data-pg-keep aria-current="true">All <span class="n">3</span></a><a class="pg-chip bad" href="/clusters?realm=&amp;state=problem" data-pg-keep>Needs a look <span class="n">2</span></a><a class="pg-chip" href="/clusters?realm=&amp;state=running" data-pg-keep>Running <span class="n">1</span></a><a class="pg-chip" href="/clusters?realm=&amp;state=monitoring" data-pg-keep>With monitoring <span class="n">1</span></a>`)
	assert.Contains(t, body, `<a class="btn sm danger" href="/dialogs/delete-clusters" data-pg-bulk data-pg-verb="Delete">Delete…</a>`)
	assert.Contains(t, body, `<tr class="pg-none" data-pg-none hidden><td colspan="9"><div class="empty">No cluster matches the filter. <a href="/clusters?realm=">Show all</a></div></td></tr>`)
	assert.Contains(t, body, `sse-swap="pg-clusters-head"`)
	assert.Contains(t, body, `sse-swap="pg-clusters-chips"`)
	assert.Contains(t, body, `sse-connect="/events?f=pg-clusters-chips&amp;f=pg-clusters-head&amp;f=pg-clusters-table&amp;f=rail&amp;f=tasks&amp;nav=clusters"`)

	body = pgBody(t, ts, "/clusters?realm=sind", 200)
	assert.Contains(t, body, `<span class="pg-scope">Showing realm <b class="mono">sind</b> · <a href="/clusters?realm=">Show all</a></span>`)
	assert.NotContains(t, body, "Every cluster of every realm.")
	assert.NotContains(t, body, `title="Sort by Realm"`)
	assert.NotContains(t, body, "/clusters/ci/default")
	assert.Contains(t, body, `<a class="on" aria-current="true" href="/clusters?realm=sind" data-pg-keep>sind · 2</a>`)
	assert.Contains(t, body, `href="/dialogs/create-cluster?realm=sind"`)
	assert.Contains(t, body, `data-pg-label="dev"`, "one realm names a cluster by its name alone")

	body = pgBody(t, ts, "/clusters?realm=cix", 404)
	assert.Contains(t, body, "There is no realm cix.")
	assert.Contains(t, body, `href="/clusters?realm=ci">Realm ci</a>`)
	assert.Contains(t, body, `href="/clusters?realm=">All realms</a>`)
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "ci", Clusters: 1}, {Name: "nowhere"}})
	body = pgBody(t, ts, "/clusters?realm=nowhere", 200)
	assert.Contains(t, body, `No cluster in realm <b class="mono">nowhere</b> yet.`)
	assert.Contains(t, body, `<tr class="pg-none" data-pg-none><td`)
	assert.Contains(t, body, `href="/clusters?realm=" data-pg-keep>All realms</a>`)
	assert.Equal(t, http.StatusBadRequest, ts.get("/clusters?realm=A_B").Code)
}

func TestPgNodes(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/nodes", 200)
	assert.Contains(t, body, "No node yet.")
	pgFillStore(ts.st)
	body = pgBody(t, ts, "/nodes", 200)
	assert.Contains(t, body, "<h1>Nodes</h1>")
	assert.Contains(t, body, `<tr class="click" data-href="/nodes/sind/dev/worker-3" data-pg-text="worker-3.dev dev sind worker stopped 172.20.0.10 stopped"><td class="mono c-name"><a href="/nodes/sind/dev/worker-3"><b>worker-3.dev</b></a></td><td data-label="Cluster"><a href="/clusters/sind/dev">dev</a></td><td class="mono" data-label="Realm">sind</td>`)
	assert.Contains(t, body, `<span class="pill bad">stopped</span> </td>`)
	assert.Contains(t, body, `<span class="pill warn">paused</span> </td>`)
	assert.Contains(t, body, `<b>worker-0</b>`, "a node of the default cluster has its short name")
	assert.Contains(t, body, `<span class="chip brand" data-word="controller">controller</span>`)
	assert.Contains(t, body, `<td class="mono" data-label="IP">172.20.0.2</td>`)
	assert.Contains(t, body, "sind get nodes")
	assert.Contains(t, body, `placeholder="Filter by node, cluster, state or IP" aria-label="Filter the nodes"`)
	assert.Contains(t, body, `<a class="pg-chip" href="/nodes?realm=&amp;state=stopped" data-pg-keep>Not running <span class="n">2</span></a>`)
	assert.Contains(t, body, `<th aria-sort="ascending"><a class="pg-sort on" href="/nodes?realm=&amp;sort=-cluster" data-pg-keep title="Sort by Cluster">Cluster`)
	assert.NotContains(t, body, "data-pg-select", "the nodes list selects nothing")

	assert.Contains(t, body, `<span class="pill bad" data-say="worker-3.dev is stopped." data-say-key="sind/worker-3.dev">stopped</span>`)
	assert.Contains(t, body, "Every node of every realm.")

	body = pgBody(t, ts, "/nodes?realm=ci", 200)
	assert.Contains(t, body, `<div class="tw" data-say="2 nodes in realm ci." data-say-key="count">`)
	assert.Contains(t, body, `Showing realm <b class="mono">ci</b> · <a href="/nodes?realm=">Show all</a>`)
	assert.Contains(t, body, "<title>Nodes · ci · sind ui</title>")
	assert.Contains(t, pgBody(t, ts, "/clusters?realm=ci", 200), `<div class="tw" data-say="1 cluster in realm ci." data-say-key="count">`)
	assert.NotContains(t, body, `title="Sort by Realm"`)
	assert.NotContains(t, body, "worker-3.dev")
	assert.Contains(t, body, "<b>controller</b>")
	body = pgBody(t, ts, "/nodes?realm=cix", 404)
	assert.Contains(t, body, `href="/nodes?realm=ci">Realm ci</a>`)
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "ci", Clusters: 1}, {Name: "zz"}})
	body = pgBody(t, ts, "/nodes?realm=zz", 200)
	assert.Contains(t, body, `No node in realm <b class="mono">zz</b> yet.`)
	assert.Equal(t, http.StatusBadRequest, ts.get("/nodes?realm=-").Code)
}

func TestPgRail(t *testing.T) {
	ts := pgServer(t)
	body := pgBody(t, ts, "/", 200)
	assert.NotContains(t, body, `Clusters<span`)
	assert.NotContains(t, body, `Nodes<span`)
	pgFillStore(ts.st)
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `Clusters<span class="n" title="3 clusters in all realms"><span aria-hidden="true">3</span><span class="vh">, 3 clusters in all realms</span></span>`)
	assert.Contains(t, body, `Nodes<span class="n bad" title="2 nodes need a look in all realms"><span aria-hidden="true">2<span class="u"> need a look</span></span><span class="vh">, 2 nodes need a look in all realms</span></span>`)
	assert.Contains(t, body, `<a class="nav on" href="/" aria-current="page">`)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-0", 200)
	assert.Contains(t, body, `<a class="nav on" href="/nodes" aria-current="page">`)
	body = pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.Contains(t, body, `<a class="nav on" href="/clusters" aria-current="page">`)
	// Slurm sends a worker of hpc no jobs: it needs a look too, in the
	// rail, the realm row and the pill of hpc, which runs; the worker of the
	// same name in dev is fine.
	ts.st.SetSlurm("sind", "hpc", &store.Slurm{Info: &cluster.SlurmInfo{Nodes: []slurm.NodeInfo{{Name: "worker-1", State: "drained", Reason: "memory errors"}}}})
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `<span aria-hidden="true">3<span class="u"> need a look</span></span><span class="vh">, 3 nodes need a look in all realms</span>`)
	assert.Contains(t, body, `<span class="pill bad">2 nodes need a look</span>`, "the realm row of sind")
	assert.Contains(t, body, `<span class="pg-name">hpc</span><span class="pill warn">running · 1 needs a look</span>`)
	assert.Contains(t, body, `worker-1.hpc is drained in Slurm`)
	assert.NotContains(t, body, `worker-1.dev is drained`)
	body = pgBody(t, ts, "/nodes?state=problem", 200)
	assert.Contains(t, body, `<span class="pill warn">drained</span> <span class="faint pg-small">memory errors</span>`, "the Issues column, with the reason")
	// The rail says that its counts span every realm.
	assert.Contains(t, body, `Realm<span class="rgs">counts all realms</span></span>`)
	// One needs a look: the unit says so.
	ts.st.SetSlurm("sind", "hpc", nil)
	ts.st.SetNodes("ci", nil)
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `<span aria-hidden="true">1<span class="u"> needs a look</span></span><span class="vh">, 1 node needs a look in all realms</span>`)
	// Every node runs: a dot, with its meaning in words.
	ts.st.SetNodes("ci", nil)
	ts.st.SetNodes("sind", pgNodes("sind", "hpc", map[config.Role]int{config.RoleController: 1}, nil))
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `Nodes<span class="n ok" title="every node in all realms runs"><span class="vh">, every node in all realms runs</span></span>`)
	// One realm: the labels need not say which.
	ts.st.SetNodes("ci", nil)
	ts.st.SetClusters("ci", nil)
	ts.st.SetRealms(nil)
	body = pgBody(t, ts, "/", 200)
	assert.Contains(t, body, `Nodes<span class="n ok" title="every node runs"><span class="vh">, every node runs</span></span>`)
	assert.NotContains(t, body, "counts all realms")
}

func TestPgClusterSummary(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.Contains(t, body, "<title>dev · sind ui</title>")
	assert.Contains(t, body, `<div class="crumbs"><a href="/clusters">Clusters</a> / <a href="/clusters?realm=sind">sind</a> / dev</div>`)
	assert.Contains(t, body, `<h1>dev <span class="pill bad" data-say="dev is mixed · 1 needs a look.">mixed · 1 needs a look</span></h1>`)
	assert.Contains(t, body, `<span class="mono">Slurm 26.05.4</span>`)
	assert.Contains(t, body, `<div class="callout run pg-run">`)
	assert.Contains(t, body, `<b class="mono">create worker dev</b> runs in a terminal.`)
	assert.Contains(t, body, `<a class="btn sm" href="/runs/r5">Show progress</a>`)
	assert.Contains(t, body, `href="/dialogs/create-worker?cluster=dev&amp;realm=sind"`)
	assert.Contains(t, body, `class="btn danger" href="/dialogs/delete-cluster?cluster=dev&amp;realm=sind"`)
	// The tabs, without Slurm.
	assert.Contains(t, body, `<a class="tab on" href="/clusters/sind/dev" aria-current="page">Summary</a>`)
	assert.Contains(t, body, `<a class="tab" href="/clusters/sind/dev?tab=nodes">Nodes<span class="n" title="9 nodes"><span aria-hidden="true">9</span><span class="vh">, 9 nodes</span></span><span class="n bad"><span class="vh">, </span>1 needs a look</span></a>`)
	assert.Contains(t, body, `<a class="tab" href="/clusters/sind/dev?tab=logs">Logs</a>`)
	assert.Contains(t, body, `>Network and storage</a>`)
	assert.Contains(t, body, `?tab=connect">Connect</a>`)
	assert.Contains(t, body, `<a class="tab" href="/clusters/sind/dev?tab=slurm">Slurm</a>`)
	assert.NotContains(t, body, "View slurm.conf", "the controller was not asked yet")
	assert.NotContains(t, body, "<dt>partitions</dt>", "sinfo was not asked yet")
	// Without a status: the tiles and a note.
	assert.Contains(t, body, "Service health appears once sind ui has probed the cluster.")
	assert.Contains(t, body, "Network health appears once")
	assert.Contains(t, body, `<a class="pg-nm pg-nm-worker pg-nm-stopped" href="/nodes/sind/dev/worker-3" title="worker-3 · worker · stopped" aria-label="worker-3.dev, stopped"></a>`)
	// The legend shows a tile of each kind.
	assert.Contains(t, body, `<ul class="pg-legend" aria-label="What the tiles mean">`)
	assert.Contains(t, body, `<li><i class="pg-nm pg-nm-worker pg-nm-running pg-nm-svcdown" aria-hidden="true"></i>a service is down</li>`)

	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.NotContains(t, body, "Service health appears once")
	// Ready for jobs: the verdict, the checks with their state in words,
	// and the first commands.
	assert.Contains(t, body, `<h2>Ready for jobs</h2><span class="pill warn">yes, with 2 of 4 workers</span>`)
	assert.Contains(t, body, `<li><span class="pg-ck warn" aria-hidden="true">!</span><div><span class="vh">warning: </span><b>2 of 4 workers take jobs</b> <span class="muted">worker-1: slurmd is down; worker-3: stopped.</span><div class="pg-fix"><a class="btn sm ghost" href="/clusters/sind/dev?node=worker-1&amp;tab=logs">Logs of worker-1</a>`)
	assert.Contains(t, body, `<b>Your files</b> <span class="muted">/scratch/dev on this host is /data on every node.</span>`)
	assert.Contains(t, body, "<code>sind enter dev</code>")
	assert.Contains(t, body, "<code>sbatch --chdir=/data --wrap hostname &amp;&amp; squeue</code>")
	assert.Contains(t, body, `More commands are on <a href="/clusters/sind/dev?tab=connect">Connect</a>.`)
	assert.Contains(t, body, `<th class="mono pg-svc"><span data-word="munge">munge</span></th><th class="mono pg-svc"><span data-word="sshd">sshd</span></th><th class="mono pg-svc"><span data-word="slurmctld">slurmctld</span></th><th class="mono pg-svc"><span data-word="slurmd">slurmd</span></th><th class="mono pg-svc"><span data-word="mariadb">mariadb</span></th><th class="mono pg-svc"><span data-word="slurmdbd">slurmdbd</span></th><th class="mono pg-svc"><span data-word="slurmrestd">slurmrestd</span></th><th class="mono pg-svc"><span data-word="sackd">sackd</span></th>`)
	// controller: up on munge, sshd and slurmctld, not on the others.
	// Each cell has its state in words, for screen readers and as its tooltip.
	assert.Contains(t, body, `<td title="munge: active"><span class="pg-ck y" aria-hidden="true">✓</span><span class="vh">munge: active</span></td>`)
	assert.Contains(t, body, `<td title="slurmctld: active"><span class="pg-ck y" aria-hidden="true">✓</span><span class="vh">slurmctld: active</span></td><td title="slurmd: not on this node"><span class="pg-ck na" aria-hidden="true">·</span>`)
	assert.Contains(t, body, `<td title="slurmd: down"><span class="pg-ck n" aria-hidden="true">✗</span><span class="vh">slurmd: down</span></td>`, "slurmd is down on worker-1")
	assert.Contains(t, body, `<td title="munge: node not running"><span class="pg-ck na" aria-hidden="true">–</span>`, "a node that does not run has no result")
	assert.Contains(t, body, `<p class="hint pg-hint pg-mx-key">`)
	// A tile names the service that is down.
	assert.Contains(t, body, `<a class="pg-nm pg-nm-worker pg-nm-running pg-nm-svcdown" href="/nodes/sind/dev/worker-1" title="worker-1 · worker · running · slurmd down" aria-label="worker-1.dev, running, slurmd down"></a>`)
	assert.Contains(t, body, `Nodes<span class="n" title="9 nodes"><span aria-hidden="true">9</span><span class="vh">, 9 nodes</span></span><span class="n bad"><span class="vh">, </span>1 needs a look</span>`, "worker-1 may be starting slurmd: a command works on dev")
	ts.st.SetRuns(nil)
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev", 200), `Nodes<span class="n" title="9 nodes"><span aria-hidden="true">9</span><span class="vh">, 9 nodes</span></span><span class="n bad"><span class="vh">, </span>2 need a look</span>`, "the name reads \"Nodes, 9 nodes, 2 need a look\"")
	// The cluster, with the data mount and the pair of controllers.
	assert.Contains(t, body, `<dd class="mono">/scratch/dev <span class="faint">(host directory) → /data</span></dd>`)
	assert.Contains(t, body, `<span class="chip">controller: primary, in control</span><span class="chip">controller-backup: backup, standby</span>`)
	assert.Contains(t, body, `<dd class="mono">sind-dev-net <span class="pill ok">ok</span></dd>`)
	assert.Contains(t, body, `<dt>subnet</dt><dd class="mono">172.20.0.0/16</dd>`)
	assert.Contains(t, body, `<dt>DNS</dt><dd><span class="pill ok">ok</span></dd>`)
	assert.NotContains(t, body, "grafana, prometheus", "dev has no monitoring")

	// A cluster with monitoring and a status with a volume as its data.
	ts.st.SetStatus("sind", "hpc", &cluster.Status{
		Name: "hpc", Network: &cluster.NetworkHealth{DNS: false}, Mounts: []cluster.MountPoint{{Path: "/work", Source: "sind-hpc-data", Type: "volume", OK: true, Data: true}},
	})
	body = pgBody(t, ts, "/clusters/sind/hpc", 200)
	assert.Contains(t, body, `<span class="chip brand">monitoring</span>`)
	assert.Contains(t, body, "grafana, prometheus, loki, alloy")
	assert.Contains(t, body, `<span class="faint">(volume) → /work</span>`)
	assert.Contains(t, body, "sbatch --chdir=/work --wrap hostname", "the data mount of hpc is at /work")
	assert.Contains(t, body, `<dt>DNS</dt><dd><span class="pill bad">down</span></dd>`)
	assert.NotContains(t, body, "callout run", "no run touches hpc")
	assert.NotContains(t, body, `problem</span>`, "every node of hpc runs")
	ts.st.SetStatus("sind", "hpc", &cluster.Status{Name: "hpc"})
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/hpc", 200), "No network data.")
}

func TestPgClusterNotFound(t *testing.T) {
	ts := pgServer(t)
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev", 404), "Not found")
	pgFillStore(ts.st)
	ts.st.SetClusters("ci", []cluster.Summary{{Name: "default"}, {Name: "dev"}})
	for target, want := range map[string][]string{
		// The address and what is missing; a cluster of that name in
		// another realm, a close name, the list.
		"/clusters/sind/devv": {`Nothing lives at <code class="mono miss-addr">/clusters/sind/devv</code>`,
			"Realm sind has no cluster devv.", `<a class="btn primary" href="/clusters/sind/dev">dev</a> <span class="muted">in realm sind</span>`,
			`<a class="btn" href="/clusters?realm=sind">Clusters of realm sind</a>`},
		"/clusters/ci/hpc":    {"Realm ci has no cluster hpc.", `<a class="btn primary" href="/clusters/sind/hpc">hpc</a> <span class="muted">in realm sind</span>`},
		"/clusters/sind/nope": {"Realm sind has no cluster nope.", pgMissGone, `<a class="btn primary" href="/clusters?realm=sind">`},
		// An unknown realm: the realms of close names, and every cluster.
		"/clusters/sindd/dev": {"There is no realm sindd.", `href="/clusters/sind/dev">dev</a> <span class="muted">in realm sind</span>`,
			`href="/clusters/ci/dev">dev</a> <span class="muted">in realm ci</span>`, `<a class="btn" href="/clusters?realm=sind">Clusters of realm sind</a>`,
			`<a class="btn" href="/clusters?realm=">All clusters</a>`},
		"/clusters/zz/x": {"There is no realm zz.", pgMissGone, `<a class="btn primary" href="/clusters?realm=">All clusters</a>`},
		// A node: close names in its cluster, and the cluster's Nodes tab.
		"/nodes/sind/dev/worker-9": {"Cluster dev has no node worker-9.", `<a class="btn primary" href="/nodes/sind/dev/worker-0">worker-0.dev</a>`,
			`<a class="btn" href="/clusters/sind/dev?tab=nodes">Nodes of dev</a>`},
		"/nodes/sind/dev/zzz": {"Cluster dev has no node zzz.", pgMissGone},
		// A node of a cluster that is missing: the node where another
		// realm's cluster of that name has it.
		"/nodes/sind/default/worker-0": {"Realm sind has no cluster default.", `<a class="btn primary" href="/nodes/ci/default/worker-0">worker-0</a> <span class="muted">in realm ci</span>`},
		"/nodes/none/dev/worker-0":     {"There is no realm none."},
	} {
		body := pgBody(t, ts, target, 404)
		for _, s := range want {
			assert.Contains(t, body, s, target)
		}
	}

	// An unknown tab is a missing page too: the tab meant first, then the
	// others.
	body := pgBody(t, ts, "/clusters/sind/dev?tab=storage", 404)
	assert.Contains(t, body, "The page of cluster dev has no tab storage.")
	assert.Contains(t, body, `<a class="btn primary" href="/clusters/sind/dev?tab=network">Network and storage</a>`)
	assert.Contains(t, body, `<a class="btn" href="/clusters/sind/dev">Summary</a>`)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=conect", 404)
	assert.Contains(t, body, `<a class="btn primary" href="/clusters/sind/dev?tab=connect">Connect</a>`)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=jobs", 404)
	assert.Contains(t, body, `<a class="btn primary" href="/clusters/sind/dev">Summary</a>`)
}

func TestPgClusterByName(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	// One realm has the cluster: go there.
	w := ts.get("/clusters/hpc")
	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/clusters/sind/hpc", w.Header().Get("Location"))
	// Two realms have it: list them.
	ts.st.SetClusters("ci", []cluster.Summary{{Name: "default"}, {Name: "dev"}})
	body := pgBody(t, ts, "/clusters/dev", 404)
	assert.Contains(t, body, "More than one realm has a cluster dev.")
	assert.Contains(t, body, `href="/clusters/ci/dev">dev</a> <span class="muted">in realm ci</span>`)
	assert.Contains(t, body, `href="/clusters/sind/dev">dev</a> <span class="muted">in realm sind</span>`)
	// None has it: the close names, in each realm that has one.
	body = pgBody(t, ts, "/clusters/devv", 404)
	assert.Contains(t, body, "No realm has a cluster devv.")
	assert.Contains(t, body, "/clusters/REALM/CLUSTER")
	assert.Contains(t, body, `href="/clusters/ci/dev"`)
	assert.Contains(t, body, `href="/clusters/sind/dev"`)
	assert.Contains(t, body, `<a class="btn" href="/clusters?realm=">All clusters</a>`)
	assert.NotContains(t, body, "Clusters of realm")
	// The name is a realm: its clusters first.
	body = pgBody(t, ts, "/clusters/ci", 404)
	assert.Contains(t, body, "The address names realm ci, but no cluster.")
	assert.Contains(t, body, `<a class="btn primary" href="/clusters?realm=ci">Clusters of realm ci</a>`)
	assert.Contains(t, body, `<a class="btn" href="/clusters?realm=">All clusters</a>`)
	assert.Equal(t, http.StatusBadRequest, ts.get("/clusters/"+strings.Repeat("a", maxParamLen+1)).Code)
}

func TestPgMissForPath(t *testing.T) {
	// An address of the cluster and node pages that no route serves offers
	// what it names.
	ts := pgServer(t)
	pgFillStore(ts.st)
	for target, want := range map[string][]string{
		"/clusters/sind/dev/": {`<a class="btn primary" href="/clusters/sind/dev">dev</a> <span class="muted">in realm sind</span>`,
			`<a class="btn" href="/clusters">Clusters</a>`},
		"/nodes/sind/dev": {`<a class="btn primary" href="/clusters/sind/dev?tab=nodes">Nodes of dev</a>`},
		"/clusters/sind/dev/nodes/controller": {`<a class="btn primary" href="/nodes/sind/dev/controller">controller.dev</a>`,
			`<a class="btn" href="/clusters/sind/dev">dev</a>`},
		"/nodes/ci/default/worker-0/": {`<a class="btn primary" href="/nodes/ci/default/worker-0">worker-0</a>`},
		"/clusters/sind/":             {`<a class="btn primary" href="/clusters?realm=sind">Clusters of realm sind</a>`},
		"/nodes/sind":                 {`<a class="btn primary" href="/nodes?realm=sind">Nodes of realm sind</a>`},
		"/clusters/sind/devv/x":       {`<a class="btn primary" href="/clusters/sind/dev">dev</a>`, `href="/clusters?realm=sind">Clusters of realm sind</a>`},
		"/clusters/nope/dev/":         {`<a class="btn primary" href="/clusters">Clusters</a>`},
	} {
		body := pgBody(t, ts, target, 404)
		for _, s := range want {
			assert.Contains(t, body, s, target)
		}
	}
}

func TestPgClusterNodesTab(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200)
	assert.Contains(t, body, `<a class="tab on" href="/clusters/sind/dev?tab=nodes" aria-current="page">Nodes<span class="n" title="9 nodes"><span aria-hidden="true">9</span><span class="vh">, 9 nodes</span></span><span class="n bad"><span class="vh">, </span>1 needs a look</span></a>`)
	assert.Contains(t, body, `<section class="panel" data-pg-select="nodes">`)
	assert.Contains(t, body, `<tr data-pg-item="worker-3" data-pg-label="worker-3" data-pg-match="stopped worker"><td class="c-chk"><input type="checkbox" data-pg-sel="worker-3" aria-label="Select worker-3"></td>`)
	assert.Contains(t, body, `<div class="pg-selbar" data-pg-selbar hidden>`)
	// The selection offers each verb with the states that it fits; the
	// script shows those that fit a selected node.
	assert.Contains(t, body, `<a class="btn sm" href="/dialogs/power?cluster=dev&amp;op=start&amp;realm=sind" data-pg-dialog data-pg-verb="Start" data-pg-fit="stopped unknown">Start…</a>`)
	assert.Contains(t, body, `op=stop&amp;realm=sind" data-pg-dialog data-pg-verb="Shut down" data-pg-fit="running">Shut down…`)
	assert.Contains(t, body, `op=reboot&amp;realm=sind" data-pg-dialog data-pg-verb="Reboot" data-pg-fit="running">Reboot…`)
	assert.Contains(t, body, `op=pause&amp;realm=sind" data-pg-dialog data-pg-verb="Freeze" data-pg-fit="running">Freeze…`)
	assert.Contains(t, body, `op=unpause&amp;realm=sind" data-pg-dialog data-pg-verb="Unfreeze" data-pg-fit="paused">Unfreeze…`)
	assert.Contains(t, body, `op=drain&amp;realm=sind" data-pg-dialog data-pg-verb="Drain" data-pg-fit="worker" data-pg-noun="worker">Drain…`)
	assert.Contains(t, body, `op=resume&amp;realm=sind" data-pg-dialog data-pg-verb="Resume" data-pg-fit="worker" data-pg-noun="worker">Resume…`)
	assert.Contains(t, body, `<a class="btn sm danger" href="/dialogs/delete-worker?cluster=dev&amp;realm=sind" data-pg-dialog data-pg-verb="Delete" data-pg-fit="worker" data-pg-noun="worker" data-pg-delete>Delete…</a>`)
	assert.Contains(t, body, `hx-push-url="false">Delete cluster…</a>`)
	assert.Contains(t, body, `<span class="pill bad">stopped</span> </td>`)
	assert.Contains(t, body, `<span class="faint">–</span>`)
	assert.Contains(t, body, "<title>Nodes · dev · sind ui</title>", "the tab is in the title")
	assert.Contains(t, body, `<span class="pill bad" data-say="worker-3 is stopped." data-say-key="worker-3">stopped</span>`)
	// Each row has its own buttons: Start for a node that does not run,
	// Logs and Shell for every node.
	assert.Contains(t, body, `<td class="act c-act"><a class="btn sm" href="/dialogs/power?cluster=dev&amp;nodes=worker-3&amp;op=start&amp;realm=sind" hx-get="/dialogs/power?cluster=dev&amp;nodes=worker-3&amp;op=start&amp;realm=sind" hx-target="#layer" hx-push-url="false" aria-label="Start worker-3…">Start…</a><a class="btn sm ghost" href="/clusters/sind/dev?node=worker-3&amp;tab=logs" aria-label="Logs of worker-3">Logs</a><button type="button" class="btn sm ghost" data-pg-copy-text="sind ssh worker-3.dev" aria-label="Shell: copy sind ssh worker-3.dev" title="Copy sind ssh worker-3.dev">Shell</button></td>`)
	assert.Contains(t, body, `<td class="act c-act"><a class="btn sm ghost" href="/clusters/sind/dev?node=worker-0&amp;tab=logs" aria-label="Logs of worker-0">Logs</a>`, "a running node has no Start")
	// The Logs link shows its node.
	body = pgBody(t, ts, "/clusters/sind/dev?tab=logs&node=worker-2", 200)
	assert.Contains(t, body, `<option value="worker-2" data-units="slurmd munge sshd" selected>`)
	body = pgBody(t, ts, "/clusters/ci/default?tab=nodes", 200)
	assert.Contains(t, body, `op=unpause&amp;realm=ci" hx-target="#layer" hx-push-url="false" aria-label="Unfreeze worker-0…">Unfreeze…</a>`, "a paused node unfreezes")
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
	// Each node's address and DNS name, with Copy; the names and keys.
	assert.Contains(t, body, `<tr><td class="mono"><a href="/nodes/sind/dev/worker-0"><b>worker-0</b></a></td><td class="mono">172.20.0.7 <button type="button" class="btn sm ghost pg-copy" data-pg-copy-text="172.20.0.7" aria-label="Copy 172.20.0.7">Copy</button></td><td class="mono">worker-0.dev.sind.sind <button type="button" class="btn sm ghost pg-copy" data-pg-copy-text="worker-0.dev.sind.sind" aria-label="Copy worker-0.dev.sind.sind">Copy</button></td></tr>`)
	assert.Contains(t, body, `<dt>DNS zone</dt><dd><span class="mono">sind.sind</span> <button`)
	assert.Contains(t, body, `<dt>node names</dt><dd><span class="mono">NODE.dev.sind.sind</span><div class="muted pg-small">Such as controller.dev.sind.sind.</div></dd>`)
	assert.Contains(t, body, `<dt>munge key</dt><dd><span class="mono">/etc/munge/munge.key</span> <button`)
	assert.Contains(t, body, "In the volume sind-dev-munge, on every node. sind ui does not show the key.")
	assert.NotContains(t, body, "DNS server", "the mesh was not read yet")

	// With the mesh: its zone, DNS server and SSH relay.
	ts.st.SetMesh("sind", store.Mesh{Info: &mesh.Info{DNSZone: "sind.sind", DNSIP: "172.18.255.254", DNSContainer: "sind-dns", SSHContainer: "sind-ssh"}})
	body = pgBody(t, ts, "/clusters/sind/dev?tab=network", 200)
	assert.Contains(t, body, `<dt>DNS server</dt><dd><span class="mono">172.18.255.254</span> <button`)
	assert.Contains(t, body, "The container sind-dns on the mesh network.")
	assert.Contains(t, body, `<dt>SSH relay</dt><dd><span class="mono">sind-ssh</span>`)

	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=network", 200)
	assert.Contains(t, body, `<tr><td class="mono">/etc/slurm</td><td class="mono">sind-dev-config <button type="button" class="btn sm ghost pg-copy" data-pg-copy-text="sind-dev-config" aria-label="Copy sind-dev-config">Copy</button></td><td>Docker volume</td><td>rw on controllers, ro elsewhere</td><td><span class="pill ok">exists</span></td></tr>`)
	assert.Contains(t, body, `<td>host directory (bind mount)</td><td>read-write</td><td><span class="faint">not checked</span></td>`)
	assert.Contains(t, body, "Volume says whether Docker has the volume. sind does not check a directory of the host.")
	assert.Contains(t, body, `<td class="mono">/home</td><td class="mono">sind-dev-home <button`)
	assert.Contains(t, body, `<td>Docker volume</td><td>read-write</td><td><span class="pill bad">missing</span></td></tr>`)
	assert.Contains(t, body, "None: the nodes use auth/slurm (identity clientIds), not munge.", "the status has no munge mount")

	// A status with a munge volume and a journal volume.
	st := ts.st.Snapshot().Status("sind", "dev")
	withMunge := *st
	withMunge.Mounts = append([]cluster.MountPoint{{Path: "/etc/munge", Source: "sind-dev-munge2", Type: "volume", OK: true}, {Path: "/var/log/journal", Source: "sind-journal", Subpath: "dev", Type: "volume", OK: true}}, st.Mounts...)
	ts.st.SetStatus("sind", "dev", &withMunge)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=network", 200)
	assert.Contains(t, body, "In the volume sind-dev-munge2, on every node.")
	assert.Contains(t, body, `<td class="mono">/etc/munge</td><td class="mono">sind-dev-munge2 <button type="button" class="btn sm ghost pg-copy" data-pg-copy-text="sind-dev-munge2" aria-label="Copy sind-dev-munge2">Copy</button></td><td>Docker volume</td><td>read-only</td>`)
	assert.Contains(t, body, "<td>Docker volume, directory dev</td>")
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
	assert.Contains(t, body, `<dt><span data-word="slurmrestd">slurmrestd</span></dt><dd class="mono">http://api.dev.sind.sind:6820</dd>`)
	assert.NotContains(t, body, "Grafana")
	assert.Contains(t, body, `<button type="button" data-pg-copy aria-label="Copy `)
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
	// The page hands the tab the node, the source and the unit of the
	// address, and its live updates carry them.
	body = pgBody(t, ts, "/clusters/sind/dev?tab=logs&node=worker-2&source=journal&unit=munge", 200)
	assert.Contains(t, body, `<option value="worker-2" data-units="slurmd munge sshd" selected>worker-2</option>`)
	assert.Contains(t, body, "sind logs worker-2.dev munge -f -n 200")
	assert.Contains(t, body, "node=worker-2&amp;realm=sind&amp;source=journal&amp;tab=logs&amp;unit=munge")
}

func TestPgNodeIssueLogs(t *testing.T) {
	d := &pgNodeView{Realm: "sind", Cluster: "dev", Short: "worker-0", Node: pgNode{
		Role: "worker", State: cluster.StateRunning, Services: []pgSvc{{Name: "munge"}, {Name: "slurmd", OK: true}},
	}}
	assert.Equal(t, "/clusters/sind/dev?node=worker-0&source=journal&tab=logs&unit=munge", d.IssueLogsLink(), "the service that is down")
	d.Node.Services[1].OK = false
	assert.Equal(t, "/clusters/sind/dev?node=worker-0&source=journal&tab=logs&unit=slurmd", d.IssueLogsLink(), "the Slurm service first")
}

func TestPgNodePage(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)
	body := pgBody(t, ts, "/nodes/sind/dev/worker-3", 200)
	assert.Contains(t, body, "<title>worker-3.dev · sind ui</title>")
	assert.Contains(t, body, `<div class="crumbs"><a href="/nodes">Nodes</a> / <a href="/clusters/sind/dev">dev</a> / worker-3</div>`)
	assert.Contains(t, body, `<h1>worker-3.dev <span class="pill bad" data-say="worker-3.dev is stopped.">stopped</span></h1>`)
	assert.Contains(t, body, `<span class="chip " data-word="worker">worker</span>`)
	assert.Contains(t, body, `<span class="mono">172.20.0.10</span>`)
	assert.Contains(t, body, `op=start&amp;realm=sind" hx-get=`)
	assert.Contains(t, body, `nodes=worker-3&amp;op=start`)
	assert.Contains(t, body, `class="btn danger" href="/dialogs/delete-worker?cluster=dev&amp;nodes=worker-3&amp;realm=sind"`)
	assert.Contains(t, body, `hx-push-url="false">Delete worker…</a>`)
	assert.Contains(t, body, `nodes=worker-3&amp;op=drain&amp;realm=sind" hx-get=`)
	assert.Contains(t, body, `nodes=worker-3&amp;op=resume&amp;realm=sind" hx-get=`)
	assert.Contains(t, body, `<h2>Issues</h2>`)
	assert.Contains(t, body, `<div class="pg-issue"><span class="pill bad">stopped</span></div>`)
	// A node that does not run: its container output, which Docker keeps.
	assert.Contains(t, body, `<a class="btn sm" href="/clusters/sind/dev?node=worker-3&amp;tab=logs">Logs</a>`)
	assert.Contains(t, body, `<div class="actions"><a class="btn" href="/clusters/sind/dev?node=worker-3&amp;tab=logs">Logs</a>`)
	assert.Contains(t, body, "No probe result yet.")
	assert.Contains(t, body, `<dt>name</dt><dd class="mono">sind-dev-worker-3</dd>`)
	assert.Contains(t, body, `<a href="/clusters/sind/dev">dev</a> <span class="pill bad">mixed · 1 needs a look</span>`)
	assert.Contains(t, body, `<dd class="mono">worker-3.dev.sind.sind <button type="button" class="btn sm" data-pg-copy-text="worker-3.dev.sind.sind">Copy</button></dd>`)
	assert.Contains(t, body, "<code>sind ssh worker-3.dev</code>")
	assert.Contains(t, body, "<code>sind logs worker-3.dev slurmd -f -n 200</code>")

	pgFillStatus(ts.st)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-3", 200)
	assert.Contains(t, body, `<dt class="mono"><span data-word="slurmd">slurmd</span></dt><dd><span class="pill">not running</span></dd>`)
	assert.Contains(t, body, `<dt>managed</dt><dd>yes</dd>`)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-1", 200)
	assert.Contains(t, body, `<span class="pill bad">slurmd down</span>`)
	// The Issues open the journal of the service that is down, and each
	// service has its journal.
	assert.Contains(t, body, `<a class="btn sm" href="/clusters/sind/dev?node=worker-1&amp;source=journal&amp;tab=logs&amp;unit=slurmd">Logs</a>`)
	assert.Contains(t, body, `<dt class="mono"><span data-word="slurmd">slurmd</span></dt><dd><span class="pill bad">down</span> <a class="btn sm ghost" href="/clusters/sind/dev?node=worker-1&amp;source=journal&amp;tab=logs&amp;unit=slurmd" aria-label="Logs of slurmd">Logs</a></dd>`, "the word of every page")
	assert.Contains(t, body, `<dt class="mono"><span data-word="munge">munge</span></dt><dd><span class="pill ok">active</span> <a class="btn sm ghost" href="/clusters/sind/dev?node=worker-1&amp;source=journal&amp;tab=logs&amp;unit=munge" aria-label="Logs of munge">Logs</a></dd>`)
	assert.Contains(t, body, `>Shut down…</a>`)
	assert.Contains(t, body, `>Reboot…</a>`)
	assert.Contains(t, body, `>Freeze…</a>`)

	// A controller: no delete, HA position, its service.
	body = pgBody(t, ts, "/nodes/sind/dev/controller", 200)
	assert.Contains(t, body, "HA position primary · in control")
	assert.Contains(t, body, `<span class="chip brand" data-word="controller">controller</span>`)
	assert.NotContains(t, body, "delete-worker")
	assert.NotContains(t, body, "<h2>Issues</h2>")
	assert.Contains(t, body, "sind logs controller.dev slurmctld -f")
	// A healthy node has its Logs too: the journal of its Slurm service.
	assert.Contains(t, body, `<div class="actions"><a class="btn" href="/clusters/sind/dev?node=controller&amp;source=journal&amp;tab=logs&amp;unit=slurmctld">Logs</a>`)
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
	ts.st.SetRuns(nil) // no command works on dev
	ts.srv.coalesce = 10 * time.Millisecond
	body := pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200)
	m := regexp.MustCompile(`sse-connect="/events\?([^"]+)"`).FindStringSubmatch(body)
	require.NotNil(t, m)
	query := strings.ReplaceAll(m[1], "&amp;", "&")
	c := ts.connect(query)

	// A status changes the body, and the head and the rail, which count
	// the node with a service down now.
	pgFillStatus(ts.st)
	sent := map[string]string{}
	for range 3 {
		e := c.next()
		sent[e.name] = e.data
	}
	assert.Contains(t, sent["pg-cluster-body"], `<span class="pill bad">slurmd down</span>`)
	assert.Contains(t, sent["rail"], "3<span class=\"u\"> need a look")
	assert.Contains(t, sent["pg-cluster-head"], "2 need a look")
	c.quiet(60 * time.Millisecond)

	// A run changes the head, and the rail, which counts no service of a
	// cluster that a command works on.
	ts.st.SetRuns([]store.Run{{ID: "n", Title: "create worker dev", Start: pgNow}})
	sent = map[string]string{}
	for range 2 {
		e := c.next()
		sent[e.name] = e.data
	}
	assert.Contains(t, sent["pg-cluster-head"], "create worker dev")
	assert.Contains(t, sent["rail"], "2<span class=\"u\"> need a look")
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

func TestPgSlurm(t *testing.T) {
	ts := pgServer(t)
	pgFillStore(ts.st)

	// Before the first read.
	body := pgBody(t, ts, "/clusters/sind/dev?tab=slurm", 200)
	assert.Contains(t, body, `<a class="tab on" href="/clusters/sind/dev?tab=slurm" aria-current="page">Slurm</a>`)
	assert.Contains(t, body, "The Slurm state appears once sind ui has asked <span class=\"mono\">sinfo</span> on the controller.")
	assert.NotContains(t, body, "<h2>Partitions</h2>")
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/worker-0", 200), "The Slurm state appears once")
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200), `<span class="pill bad" data-say="worker-3 is stopped." data-say-key="worker-3">stopped</span></td>
    <td data-label="Slurm"><span class="faint">–</span></td>`)

	// sinfo does not answer.
	ts.st.SetSlurm("sind", "dev", &store.Slurm{Err: "slurm_load_node: Unable to contact slurm controller"})
	body = pgBody(t, ts, "/clusters/sind/dev?tab=slurm", 200)
	assert.Contains(t, body, `<div class="callout bad"><b>sinfo on the controller did not answer.</b> <span class="mono">slurm_load_node: Unable to contact slurm controller</span></div>`)
	assert.NotContains(t, body, "<h2>Partitions</h2>")
	assert.NotContains(t, body, "sind wrote these files", "no file to say it of")
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/worker-0", 200), "sinfo on the controller did not answer.")
	assert.NotContains(t, pgBody(t, ts, "/clusters/sind/dev", 200), "View slurm.conf")

	// sinfo does not answer, but the controller reads the files: the tab
	// shows them, and the Summary links to slurm.conf.
	ts.st.SetSlurm("sind", "dev", &store.Slurm{Err: "slurm_load_node: Unable to contact slurm controller",
		Info: &cluster.SlurmInfo{Files: []cluster.SlurmFile{{Path: slurm.SlurmConfPath, Content: "ClusterName=dev\n"}}}})
	body = pgBody(t, ts, "/clusters/sind/dev?tab=slurm", 200)
	assert.Contains(t, body, "sinfo on the controller did not answer.")
	assert.NotContains(t, body, "<h2>Partitions</h2>")
	assert.Contains(t, body, `id="slurm-file-slurm-conf"`)
	assert.Contains(t, body, "sind wrote these files")
	assert.Contains(t, pgBody(t, ts, "/clusters/sind/dev", 200), `<dt>config</dt><dd><a href="/clusters/sind/dev?tab=slurm#slurm-file-slurm-conf">View slurm.conf</a></dd>`)

	pgFillSlurm(ts.st)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=slurm", 200)
	assert.Contains(t, body, `<td class="mono"><b>all</b> <span class="chip brand">default</span></td><td><span class="pill ok">up</span></td><td class="mono">infinite</td><td class="num">4</td><td class="mono">worker-[0-3]</td>`)
	assert.Contains(t, body, `<td class="mono"><b>debug</b></td><td><span class="pill bad">down</span></td>`)
	assert.Contains(t, body, `<td class="mono"><a href="/nodes/sind/dev/worker-0"><b>worker-0</b></a></td><td><span class="pill run">mixed</span></td><td><span class="faint">–</span></td><td>1 of 2 in use</td><td>1 GiB</td><td class="mono">all, debug</td>`)
	assert.Contains(t, body, `<span class="pill warn">drained</span></td><td>disk check</td>`)
	assert.Contains(t, body, `<span class="pill bad">down*</span></td><td>Not responding</td><td>0 of 2 in use</td><td>512 MB</td>`)
	assert.Contains(t, body, `<span class="pill ok">idle</span>`)
	assert.Contains(t, body, `<section class="panel pg-wide" id="slurm-file-slurm-conf" tabindex="-1"><div class="ph"><h2 class="mono">slurm.conf</h2><span class="grow"></span><span class="faint mono">/etc/slurm/slurm.conf</span><button type="button" class="btn sm" data-pg-copy-from="slurm-file-slurm-conf-text">Copy</button></div>`)
	assert.Contains(t, body, `<pre class="pg-conf" id="slurm-file-slurm-conf-text">ClusterName=dev
include /etc/slurm/sind-nodes.conf
</pre>`)
	assert.Contains(t, body, `id="slurm-file-sind-nodes-conf"`)

	// The Summary names the partitions; the nodes tab and the node page
	// show each node's state.
	body = pgBody(t, ts, "/clusters/sind/dev", 200)
	assert.Contains(t, body, `<dt>partitions</dt><dd class="mono">all (default), debug</dd>`)
	body = pgBody(t, ts, "/clusters/sind/dev?tab=nodes", 200)
	assert.Contains(t, body, `<td data-label="Slurm"><span class="pill warn">drained</span> <span class="faint pg-small">disk check</span></td>`)
	body = pgBody(t, ts, "/nodes/sind/dev/worker-0", 200)
	assert.Contains(t, body, `<dt>state</dt><dd><span class="pill run">mixed</span></dd>`)
	assert.Contains(t, body, `<dt>CPUs</dt><dd>1 of 2 in use</dd><dt>memory</dt><dd>1 GiB</dd><dt>partitions</dt><dd class="mono">all, debug</dd>`)
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/worker-1", 200), "<dt>reason</dt><dd>disk check</dd>")
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/controller", 200), "Slurm runs jobs on the workers only. This node is a controller.")

	// A worker that sinfo does not list, and a cluster without partitions.
	ts.st.SetSlurm("sind", "dev", &store.Slurm{Info: &cluster.SlurmInfo{}})
	assert.Contains(t, pgBody(t, ts, "/nodes/sind/dev/worker-0", 200), "sinfo does not list this node.")
	body = pgBody(t, ts, "/clusters/sind/dev?tab=slurm", 200)
	assert.Contains(t, body, "Slurm has no partition.")
	assert.Contains(t, body, "sinfo lists no node of this cluster.")
}

func TestPgSlurmClassAndMemory(t *testing.T) {
	for state, class := range map[string]string{
		"idle": "ok", "allocated": "run", "mixed": "run", "completing": "run",
		"drained": "warn", "draining": "warn", "drained*": "bad", "down*": "bad", "idle*": "bad", "fail": "bad",
	} {
		assert.Equal(t, class, pgSlurmClass(state), state)
	}
	assert.Equal(t, "512 MB", pgMemory(512))
	assert.Equal(t, "1500 MB", pgMemory(1500))
	assert.Equal(t, "2 GiB", pgMemory(2048))
}
