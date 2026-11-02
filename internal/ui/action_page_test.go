// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The link of the dialog of New cluster opens its page, with the fields
// of the link: a browser follows the redirect, htmx asks the browser to.
func TestActDialog_NewClusterOpensThePage(t *testing.T) {
	f := newActFixture(t)

	w := f.get("/dialogs/create-cluster?name=x&realm=ci")
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/clusters/new?name=x&realm=ci", w.Header().Get("Location"))
	w = f.get("/dialogs/create-cluster")
	assert.Equal(t, "/clusters/new", w.Header().Get("Location"))

	r := request("GET", "/dialogs/create-cluster?name=x")
	r.Header.Set("HX-Request", "true")
	r.AddCookie(f.cookie)
	w = f.do(r)
	assert.Equal(t, "/clusters/new?name=x", w.Header().Get("HX-Redirect"))
}

func TestActPage_NewCluster(t *testing.T) {
	f := newActFixture(t)

	w := f.get("/clusters/new")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, want := range []string{
		`<title>New cluster`, `<div class="crumbs"><a href="/clusters">Clusters</a> / New cluster</div>`,
		`<h1 id="act-title">New cluster</h1><button type="button" class="iconbtn" data-help-open="create-cluster"`,
		`<div class="sub muted" id="act-lead">Pick a start, name the cluster and check the plan.`,
		`aria-current="page"`,
		// The sections, which CSS numbers.
		`<h2 id="act-h-start">`, `<h2 id="act-h-name">`, `<h2 id="act-h-nodes">`, `<h2 id="act-h-files">`, `<h2 id="act-h-more">`,
		// The starts, and the clusters to copy.
		`<b>Quick test</b>`, `<b>With job history</b>`, `<b>Like a site</b>`,
		`<b>Copy a cluster</b><small>The nodes of a cluster you have: default, dev</small><span class="act-tmeta" id="act-copy-foot">pick one</span>`,
		`<button type="button" class="act-cprow" data-act-start="copy:sind/dev" data-act-set="api=&amp;backup=&amp;db=&amp;submitter=&amp;workers=1">`,
		// The plan with its footer, and the line of the footer on a phone.
		`<aside class="act-pcard" id="act-plan" aria-label="Plan">`,
		`<a class="act-pfplan" href="#act-preview">Show the plan</a></span>`,
		`<button type="button" class="btn" data-act-close>Cancel</button>`,
		`<span data-act-ask="close">Leave and lose your changes`,
		`<span id="act-start-on" data-start="quick" hidden></span>`,
		// The nodes under More nodes, folded while the form has none.
		`<details class="act-morenodes" id="act-morenodes"><summary><span class="act-st">More nodes</span><span class="act-ss" id="act-sum-more">none</span></summary>`,
		`<button type="button" class="btn sm" data-act-wgadd>+ Add worker group</button>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "data-act-scrim", "a page is no dialog")

	// htmx gets the form alone, as the script of the dialogs opens the
	// page again with a draft.
	body = f.postHTMX("/clusters/new?name=lab", "").Body.String()
	assert.Contains(t, body, `<div class="act-page" id="act-dialog" data-act-page`)
	assert.NotContains(t, body, "<!doctype html>")

	// A link that is wrong fails as a dialog does.
	assert.Equal(t, http.StatusBadRequest, f.get("/clusters/new?realm=Bad+Realm").Code)

	// More nodes opens for the nodes it has.
	body = f.get("/clusters/new?backup=on").Body.String()
	assert.Contains(t, body, `<details class="act-morenodes" id="act-morenodes" open><summary><span class="act-st">More nodes</span><span class="act-ss" id="act-sum-more">backup</span>`)
}

// The realm of a new cluster: the realm of the link, else the scope of
// the page, else sind; a realm that sind ui does not see is a new one,
// which the plan says, with a realm of a close name.
func TestActPage_NewClusterRealm(t *testing.T) {
	f := newActFixture(t)
	f.st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "lab", Clusters: 0}})
	f.st.SetClusters("lab", nil)

	body := pgGetWithRealm(f.testServer, "/clusters/new", "lab").Body.String()
	assert.Contains(t, body, `<option value="lab" selected>lab · 0 clusters</option><option value="sind">sind · 2 clusters</option><option value="">New realm…</option>`)
	assert.Contains(t, body, `id="act-realm" name="realm" value="lab" hidden`)
	body = pgGetWithRealm(f.testServer, "/clusters/new?realm=sind", "lab").Body.String()
	assert.Contains(t, body, `<option value="sind" selected>`, "the link wins over the scope")
	body = f.get("/clusters/new?realm=sinf").Body.String()
	assert.Contains(t, body, `<option value="" selected>New realm…</option>`)
	assert.Contains(t, body, `id="act-realm" name="realm" value="sinf" placeholder`)

	body = f.post("/actions/create-cluster/preview", actForm("realm", "sinf", "name", "hpc", "w0-count", "2", "dir", "/scratch/hpc")).Body.String()
	assert.Contains(t, body, `<div class="callout warn"><b>Realm <span class="mono tok">sinf</span> is new.</b> sind creates it with its own mesh network, DNS server and SSH relay. Did you mean <span class="mono tok">sind</span>?</div>`)
	assert.Contains(t, body, `<span class="pill warn">Ready · 1 warning</span>`)
	body = f.post("/actions/create-cluster/preview", actForm("realm", "zzzzzz", "name", "hpc", "w0-count", "2", "dir", "/scratch/hpc")).Body.String()
	assert.Contains(t, body, `sind creates it with its own mesh network, DNS server and SSH relay.</div>`, "no realm of a close name")
	body = f.post("/actions/create-cluster/preview", actForm("realm", "lab", "name", "hpc", "dir", "/scratch/hpc")).Body.String()
	assert.NotContains(t, body, "is new.")

	// The first realm of all is no surprise; a realm that is wrong is a
	// problem of its own.
	spec := actSpecs["create-cluster"]
	none := &View{Snap: store.New().Snapshot()}
	assert.Nil(t, actPreview{View: none, Spec: spec, Form: action.Form{"realm": {"lab"}}}.RealmNote())
	assert.Nil(t, actPreview{View: f.view(), Spec: spec, Form: action.Form{"realm": {"Bad"}}}.RealmNote())
	assert.Nil(t, actPreview{View: f.view(), Spec: actSpecs["power"], Form: action.Form{"realm": {"lab2"}}}.RealmNote())

	// The card of Copy a cluster names three clusters, and counts the
	// rest.
	f.st.SetClusters("lab", []cluster.Summary{{Name: "a"}, {Name: "b"}, {Name: "c"}})
	body = f.get("/clusters/new").Body.String()
	assert.Contains(t, body, `<small>The nodes of a cluster you have: a, b, c and 2 more</small>`)

	// Without a realm that sind ui sees, the field is a plain one.
	f.st.SetRealms(nil)
	f.st.SetClusters("sind", nil)
	f.st.SetClusters("lab", nil)
	f.st.SetNodes("sind", nil)
	body = f.get("/clusters/new").Body.String()
	assert.Contains(t, body, `<label for="act-realm">Realm</label><input type="text" id="act-realm" name="realm" value="sind"`)
	assert.NotContains(t, body, "act-realm-pick")
	assert.NotContains(t, body, "Copy a cluster")
}

// view is a view of the snapshot of the fixture.
func (f *actFixture) view() *View { return &View{Snap: f.st.Snapshot()} }

func TestActPresets(t *testing.T) {
	sn := store.New()
	sn.SetClusters("ci", []cluster.Summary{{Name: "pr", Controllers: 2, DBs: 1, APIs: 1, Submitters: 1, Workers: 3}})
	snap := sn.Snapshot()

	s, ok := actPresetOf(snap, "copy:ci/pr")
	require.True(t, ok)
	assert.Equal(t, actPreset{ID: "copy:ci/pr", Title: "pr", Roles: []string{"backup", "db", "api", "submitter"}, Workers: 3}, s)
	assert.Equal(t, 8, s.Nodes())
	assert.Len(t, s.Squares(), 8, "at most eight squares")
	for _, id := range []string{"copy:ci/gone", "copy:ci", "ci/pr", "nope", ""} {
		_, ok := actPresetOf(snap, id)
		assert.False(t, ok, id)
	}

	history, _ := actPresetOf(snap, "history")
	assert.Equal(t, "api=&backup=&db=on&submitter=on&workers=2", history.Set())
	assert.Equal(t, []string{"controller", "db", "submitter", "worker", "worker"}, history.Squares())

	// The start that the form names, while the form has its nodes; else
	// the first whose nodes it has; else none.
	form := func(kv ...string) url.Values { q, _ := url.ParseQuery(actForm(kv...)); return q }
	assert.Equal(t, "quick", actStartOn(snap, form("w0-count", "2")))
	assert.Equal(t, "history", actStartOn(snap, form("start", "history", "db", "on", "submitter", "on", "w0-count", "2")))
	assert.Equal(t, "history", actStartOn(snap, form("start", "quick", "db", "on", "submitter", "on", "w0-count", "2")), "the nodes no longer fit the start")
	assert.Equal(t, "copy", actStartOn(snap, form("start", "copy:ci/pr", "backup", "on", "db", "on", "api", "on", "submitter", "on", "w0-count", "3")))
	assert.Empty(t, actStartOn(snap, form("w0-count", "5")))
	assert.Empty(t, actStartOn(snap, form("wg", "w0", "wg", "w1", "w0-count", "2", "w1-count", "1")), "two groups fit no start")
}

func TestActPage_StartOfTheLink(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/clusters/new?start=history").Body.String()
	assert.Contains(t, body, `id="act-db" name="db" checked`)
	assert.Contains(t, body, `id="act-submitter" name="submitter" checked`)
	assert.Contains(t, body, `id="act-w0-count" name="w0-count" value="2"`)
	assert.Contains(t, body, `data-act-start="history" data-act-set="api=&amp;backup=&amp;db=on&amp;submitter=on&amp;workers=2" aria-pressed="true"`)

	body = f.get("/clusters/new?start=site&db=").Body.String()
	assert.Contains(t, body, `id="act-db" name="db" aria-`, "the nodes of the link win")
	assert.Contains(t, body, `id="act-backup" name="backup" aria-`)

	body = f.get("/clusters/new?start=copy:sind/dev").Body.String()
	assert.Contains(t, body, `id="act-w0-count" name="w0-count" value="1"`, "a cluster without workers starts with one")
	assert.Contains(t, body, `<span class="act-tmeta" id="act-copy-foot">dev · realm sind</span>`, "the card names the cluster it copies")
	assert.Contains(t, f.get("/clusters/new?start=nope").Body.String(), `id="act-w0-count" name="w0-count" value="2"`)
}

func TestActPreview_NewClusterPageParts(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "w0-count", "2", "dir", "/scratch/hpc", "backup", "on", "wg", "w0", "wg", "w1", "w1-count", "1")).Body.String()
	for _, want := range []string{
		`<span class="act-good" id="act-ok-name" hx-swap-oob="innerHTML">✓ Free in realm sind.</span>`,
		`<span class="act-sr" id="act-ncount" hx-swap-oob="innerHTML">5 containers</span>`,
		`<span class="act-ss" id="act-sum-more" hx-swap-oob="innerHTML">backup, 1 more worker group</span>`,
		`<span id="act-start-on" data-start="" hidden hx-swap-oob="true"></span>`,
		`<li><span class="pg-ck y" aria-hidden="true">✓</span><span>Name <span class="mono tok">hpc</span> is free in realm <span class="mono tok">sind</span>.</span></li>`,
		`<div class="cmdlabel">Runs</div>`,
	} {
		assert.Contains(t, body, want)
	}

	// A name that is taken is not free, and a plan without a config counts
	// no nodes.
	body = f.post("/actions/create-cluster/preview", actForm("name", "dev")).Body.String()
	assert.Contains(t, body, `<span class="act-good" id="act-ok-name" hx-swap-oob="innerHTML"></span>`)
	f.state.configDir = ""
	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `<span class="act-sr" id="act-ncount" hx-swap-oob="innerHTML"></span>`)
	assert.Contains(t, body, `<div class="callout bad" id="act-probs" tabindex="-1">sind ui has no state directory`)
}

// The files of a new cluster, as its form has them: the directory of the
// host, or the volume, which a copy reaches through its submitter or its
// controller.
func TestActFilesOf(t *testing.T) {
	form := func(kv ...string) url.Values { q, _ := url.ParseQuery(actForm(kv...)); return q }
	f := actFilesOf(form("name", "hpc", "dir", "/scratch/my data", "mount", "/work"))
	assert.Equal(t, pgFiles{Kind: "On this host", Host: "/scratch/my data", Node: "/work",
		Text: "Put your batch script in /scratch/my data. Every node sees it at /work, and the output of your jobs lands there too.",
		Copy: "cp job.sh '/scratch/my data/'"}, f)
	f = actFilesOf(form("realm", "ci", "name", "hpc", "dtype", "volume", "submitter", "on"))
	assert.Equal(t, pgText("ci-hpc-data"), f.Host)
	assert.Equal(t, pgText("docker cp job.sh ci-hpc-submitter:/data/"), f.Copy)
	f = actFilesOf(form("name", "Bad Name", "dtype", "volume"))
	assert.Equal(t, pgText("docker cp job.sh sind-NAME-controller:/data/"), f.Copy, "a name that is wrong is a placeholder")
}

func TestActPlanParts(t *testing.T) {
	assert.Equal(t, []string{"/", "x/", "y"}, actSlashSegs("/x/y"))
	assert.Equal(t, []string{"a/"}, actSlashSegs("a/"))
	assert.Empty(t, actSlashSegs(""))

	text := func(ps []pgPiece) string {
		var b strings.Builder
		for _, p := range ps {
			if p.Space {
				b.WriteString("_")
			} else {
				b.WriteString("[" + p.Text + "]")
			}
		}
		return b.String()
	}
	assert.Equal(t, "[cp]_[job.sh]_[~/][a-b/][c]", text(pgText("cp job.sh ~/a-b/c").Pieces()))

	lines := func(p actPreview) []string {
		var out []string
		for _, l := range p.RunLines() {
			out = append(out, fmt.Sprintf("%s %v %v", text(l.Pieces), l.More, l.Sub))
		}
		return out
	}
	p := actPreview{Cmd: action.Command{Argv: []string{"sind", "--realm", "ci", "create", "cluster", "--config", "/c/x.yaml", "--pull", "--wait", "5m"}}}
	assert.Equal(t, []string{`[sind]_[--realm]_[ci \] true false`, `[create]_[cluster \] true true`, `[--config]_[/][c/][x.yaml \] true true`, `[--pull \] true true`, "[--wait]_[5m] false true"}, lines(p),
		"the global flags, the words, then an argument to a line")
	p = actPreview{Cmd: action.Command{Argv: []string{"sind", "create", "cluster", "--pull"}}}
	assert.Equal(t, []string{`[sind]_[create]_[cluster \] true false`, "[--pull] false true"}, lines(p))
	assert.Equal(t, []string{"[sind]_[power]_[on] false false"}, lines(actPreview{Cmd: action.Command{Argv: []string{"sind", "power", "on"}}}))

	home := actPreview{Home: "/home/ada"}
	assert.Equal(t, pgText("~/x/y"), home.Tilde("/home/ada/x/y"))
	assert.Equal(t, pgText("/home/adam/x"), home.Tilde("/home/adam/x"))
	assert.Equal(t, pgText("/x"), actPreview{}.Tilde("/x"))
	home.Cmd.Argv = []string{"sind", "create", "cluster", "--config", "/home/ada/c.yaml", "--wait", "/home/ada/a b"}
	assert.Equal(t, []string{`[sind]_[create]_[cluster \] true false`, `[--config]_[~/][c.yaml \] true true`, "[--wait]_['/][home/][ada/][a]_[b'] false true"}, lines(home),
		"a path that needs quotes stays as it is")

	conf := actPreview{Form: action.Form{"slurm-main": {"# a comment\nMaxJobCount=10\n\n"}}, Cmd: action.Command{SlurmConf: "ClusterName=x\nMaxJobCount=10\n"}}.ConfLines()
	assert.Equal(t, []actConfLine{{Text: "ClusterName=x"}, {Text: "MaxJobCount=10", Own: true}}, conf)

	assert.Equal(t, "Create", actPageOK(action.Form{"name": {"Bad"}}))
	assert.Equal(t, "Create hpc", actPageOK(action.Form{"name": {" hpc "}}))
	assert.Equal(t, "sind-node:latest", actPreview{}.ImageShort("ghcr.io/gsi-hpc/sind-node:latest"))
}

// The bars of the host: the memory, the CPUs, which the nodes share, and
// each limit of the Doctor that holds from a number of nodes on.
func TestActPreview_HostBars(t *testing.T) {
	assert.Nil(t, actPreview{}.HostBars())
	assert.Nil(t, actPreview{}.HostWarns())

	b := &action.Budget{HostMB: 4096, NowMB: 1024, AddMB: 1024, NowNodes: 8, AddNodes: 3,
		Limits: []action.HostWarn{{Check: "inotify", Nodes: 10, Detail: "d", Remediation: "Run:\n\n    sudo sysctl x=1"}}}
	bars := actPreview{Cmd: action.Command{Budget: b}}.HostBars()
	require.Len(t, bars, 2, "no CPUs of the host, no bar of them")
	assert.Equal(t, actBar{Label: "Memory", NowW: 250, AddW: 250, MarkX: -1, Value: "+1 GiB of 4 GiB",
		Say: "Memory: the nodes that run now take 1 GiB, the new ones 1 GiB, of the 4 GiB of the host."}, bars[0])
	assert.Equal(t, actBar{Label: "inotify", NowW: 615, AddW: 230, MarkX: 692, Class: "over", Value: "11 nodes · max 9",
		Say: "inotify: the host then runs 11 nodes; the Doctor says that 10 and more can fail to start."}, bars[1])

	b.HostCPUs, b.NowCPUs, b.AddCPUs, b.AddMB = 4, 3, 2, 3500
	bars = actPreview{Cmd: action.Command{Budget: b}}.HostBars()
	assert.Equal(t, "over", bars[0].Class)
	assert.Equal(t, actBar{Label: "CPUs", NowW: 600, AddW: 400, MarkX: 800, Value: "+2 of 4",
		Say: "CPUs: the nodes that run now ask for 3, the new ones 2, of the 4 of the host. Nodes share the CPUs."}, bars[1], "more CPUs than the host has is no problem")
	b.AddMB = 2500
	assert.Equal(t, "high", actPreview{Cmd: action.Command{Budget: b}}.HostBars()[0].Class)

	warns := actPreview{ID: "create-worker", Cmd: action.Command{Budget: b}}.HostWarns()
	require.Len(t, warns, 1)
	assert.Equal(t, actHostWarn{Check: "inotify", Title: "The host can run out of inotify instances",
		Text:  "With the new workers, this host runs 11 nodes. The Doctor says that 10 and more can fail to start.",
		Short: "With 11 nodes, the inotify limit of the host can run out.", Detail: "d", Steps: moreFixSteps("Run:\n\n    sudo sysctl x=1")}, warns[0])
	v := &View{Snap: store.New().Snapshot()}
	assert.Contains(t, actPreview{View: v, ID: "create-cluster", Cmd: action.Command{Budget: b}}.HostWarns()[0].Text, "With the new cluster,", "a name that is wrong")
	b.Limits[0].Check = "other"
	assert.Equal(t, "The Doctor warns of other", actPreview{ID: "create-worker", Cmd: action.Command{Budget: b}}.HostWarns()[0].Title)
}
