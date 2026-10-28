// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// The frame of every dialog: its name and description, the dialog itself,
// which takes the focus on a touch screen, the question before a close
// loses changes, and the button with its key.
func TestActDialog_Frame(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/dialogs/create-worker?cluster=dev").Body.String()
	for _, want := range []string{
		`role="dialog" aria-modal="true" aria-labelledby="act-title" aria-describedby="act-lead" tabindex="-1"`,
		`<h2 id="act-title">Add workers to dev</h2>`, `<p id="act-lead">New workers take`,
		`<div class="scrim" id="act-dialog" hx-ext="ignore:morph" data-act-scrim>`,
		`<div class="mf" id="act-mf">`,
		`<div class="mf act-ask" id="act-ask" role="group" aria-labelledby="act-ask-q" hidden>`,
		`<span data-act-ask="close">Close and lose your changes<span data-act-ask-of hidden> to <b class="mono" data-act-ask-name></b></span>?</span>`,
		`<button type="button" class="btn" data-act-keep>Keep editing</button><button type="button" class="btn danger solid" data-act-discard>Discard</button>`,
		`<button type="submit" class="btn primary" id="act-ok" aria-keyshortcuts="Control+Enter Meta+Enter" disabled>Add<kbd class="act-kbd" aria-hidden="true"><span class="act-k-ctrl">Ctrl</span><span class="act-k-cmd">⌘</span> ↵</kbd></button>`,
	} {
		assert.Contains(t, body, want)
	}
	for _, not := range []string{`aria-label="Add workers`, "data-act-draft", "act-say", "act-draftbar", "act-confirm"} {
		assert.NotContains(t, body, not)
	}
	// Every dialog has its status in the footer, and the name or the
	// number to type beside the button that it guards.
	assert.Contains(t, body, `<div class="mf" id="act-mf"><span class="act-status" id="act-status"></span><span class="act-checking"`)
	body = f.get("/dialogs/delete-cluster?cluster=dev").Body.String()
	assert.Contains(t, body, `class="btn danger solid" id="act-ok"`)
	assert.Regexp(t, `(?s)<div class="mf" id="act-mf">.*<div class="act-confirm"><label for="act-confirm">Type <b class="mono" id="act-confirm-word">dev</b> to confirm</label>.*data-act-close>Cancel</button>`, body)
	assert.Contains(t, body, `name="confirm" placeholder=" " autocomplete="off"`, "the hint of why shows once the user typed")

	// New cluster keeps a draft, does not close on a click beside it, and
	// has the status in its footer, which act.js says when it changes.
	body = f.get("/dialogs/create-cluster").Body.String()
	assert.Contains(t, body, `<div class="scrim" id="act-dialog" hx-ext="ignore:morph">`)
	assert.Contains(t, body, `tabindex="-1" data-act-draft="create-cluster"`)
	assert.Contains(t, body, `<span class="vh" id="act-say" role="status"></span>`)
	assert.Contains(t, body, `<div class="act-form">`)
	assert.Contains(t, body, `<div class="act-yamled" id="act-yamled" hidden>`)
	assert.NotContains(t, body, "data-act-restored")
}

func TestActDialog_LayoutHasTheNotices(t *testing.T) {
	f := newActFixture(t)
	assert.Contains(t, f.get("/dialogs/connect").Body.String(), `<div id="layer"></div>`+"\n"+`<div class="act-notes" id="act-notes" role="status"></div>`)
}

// draftOf encodes the fields of a draft as the script of the dialogs posts
// it.
func draftOf(kv ...string) string { return actForm("draft", actForm(kv...)) }

func TestActDialog_OpensWithTheDraft(t *testing.T) {
	f := newActFixture(t)

	draft := draftOf("csrf", "old", "shown", "x", "realm", "sind", "name", "mine", "image", "my/image:1",
		"wg", "w0", "w0-count", "3", "wg", "wab", "wab-count", "2", "size", "m", "identity", "local", "stab", "main",
		"dtype", "hostPath", "dir", "/scratch/mine", "wait", "10m", "ur", "u1", "u1-name", "ada")
	w := f.postHTMX("/dialogs/create-cluster", draft)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	for _, want := range []string{
		`data-act-draft="create-cluster" data-act-restored`,
		`<div class="callout act-draftbar"><span>This is your draft from before.</span><button type="button" class="btn sm" data-act-restart>Start over</button></div>`,
		`id="act-name" name="name" value="mine"`, `value="my/image:1"`, `id="act-w0-count" name="w0-count" value="3"`, `id="act-wab-count" name="wab-count" value="2"`,
		`value="m" checked`, `value="10m" checked`, `name="u1-name" value="ada"`, `value="/scratch/mine"`,
		// What the user cleared stays clear: the defaults do not fill the
		// draft in.
		`id="act-db" name="db" aria-`, `id="act-submitter" name="submitter" aria-`, `id="act-pull" name="pull" aria-`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, `name="pull_auto"`)
	assert.NotContains(t, body, `value="old"`, "a draft has no CSRF token of its own")

	// A draft in YAML mode opens the editor with its text.
	body = f.postHTMX("/dialogs/create-cluster", draftOf("realm", "sind", "yaml_on", "1", "yaml", "kind: Cluster\nname: y\n", "wait", "5m")).Body.String()
	assert.Contains(t, body, `<div class="act-form act-yamlmode">`)
	assert.Contains(t, body, `<div class="act-yamled" id="act-yamled">`)
	assert.Contains(t, body, `name="yaml_on" value="1">`)
	assert.Contains(t, body, `aria-describedby="act-err-yaml act-hint-yaml">kind: Cluster`+"\n"+`name: y`+"\n"+`</textarea>`)
}

func TestActDialog_IgnoresADraftThatDoesNotFit(t *testing.T) {
	f := newActFixture(t)

	for _, c := range []struct{ name, target, form string }{
		{"no draft", "/dialogs/create-cluster", ""},
		{"another realm", "/dialogs/create-cluster?realm=ci", draftOf("realm", "sind", "name", "mine")},
		{"a link with fields", "/dialogs/create-cluster?name=linked", draftOf("realm", "sind", "name", "mine")},
		{"not a form", "/dialogs/create-cluster", "draft=%zz"},
		{"too long", "/dialogs/create-cluster", draftOf("realm", "sind", "name", strings.Repeat("x", actMaxValue+1))},
		{"a dialog without drafts", "/dialogs/connect", draftOf("realm", "sind", "container", "mine")},
	} {
		body := f.postHTMX(c.target, c.form).Body.String()
		assert.NotContains(t, body, "data-act-restored", c.name)
		assert.NotContains(t, body, `value="mine"`, c.name)
	}
	// A draft that fits needs no realm in the link: sind is the default.
	assert.Contains(t, f.postHTMX("/dialogs/create-cluster?realm=sind", draftOf("name", "mine")).Body.String(), `value="mine"`)
}

func TestActDraftOf(t *testing.T) {
	spec := actSpec{Draft: true}
	link := url.Values{"realm": {"ci"}}

	f, ok := actDraftOf(spec, actForm("realm", "ci", "name", "x", "nodes", "a", "nodes", "b", "confirm", "x", "shown", "s", "csrf", "c", "draft", "d"), link)
	require.True(t, ok)
	assert.Equal(t, url.Values{"realm": {"ci"}, "name": {"x"}, "nodes": {"a", "b"}}, f)

	_, ok = actDraftOf(actSpec{}, actForm("realm", "ci"), link)
	assert.False(t, ok, "a dialog without drafts")
	_, ok = actDraftOf(spec, "", link)
	assert.False(t, ok, "no draft")
}

// postFrom is postHTMX from the page at path, which htmx names in the
// header HX-Current-URL.
func (ts *testServer) postFrom(path, target, form string) *httptest.ResponseRecorder {
	r := request("POST", target)
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-Current-URL", "http://127.0.0.1:7878"+path)
	r.Body = nopCloser(form)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://127.0.0.1:7878")
	r.Header.Set(csrfHeader, ts.csrf)
	r.AddCookie(ts.cookie)
	return ts.do(r)
}

func TestActRun_LeavesThePageOfWhatItRemoves(t *testing.T) {
	f := newActFixture(t)
	form := actForm("cluster", "dev")
	run := form + "&shown=" + f.shown(t, "delete-cluster", form) + "&confirm=dev"

	// On the page of the cluster, or of one of its nodes, the dialog
	// follows its run, as the page would only say that the cluster went.
	for _, path := range []string{"/clusters/sind/dev", "/nodes/sind/dev/worker-0"} {
		w := f.postFrom(path, "/actions/delete-cluster/run", run)
		assert.Equal(t, "/tasks/task1", w.Header().Get("HX-Redirect"), path)
		assert.JSONEq(t, `{"act-started": {"url": "/tasks/task1", "follow": true}}`, w.Header().Get("HX-Trigger"), path)
	}

	// Elsewhere it stays, with a notice.
	w := f.postFrom("/clusters", "/actions/delete-cluster/run", run)
	assert.Empty(t, w.Header().Get("HX-Redirect"))
	assert.Equal(t, "#act-notes", w.Header().Get("HX-Retarget"))
}

func TestActOnPageOf(t *testing.T) {
	for _, c := range []struct {
		url, realm, cluster string
		want                bool
	}{
		{"http://h/clusters/ci/dev", "ci", "dev", true},
		{"http://h/clusters/ci/dev/logs?x=1", "ci", "dev", true},
		{"http://h/nodes/ci/dev/worker-0", "ci", "dev", true},
		{"http://h/clusters/ci/devx", "ci", "dev", false},
		{"http://h/clusters/ci", "ci", "dev", false},
		{"http://h/nodes/ci/dev", "ci", "dev", false},
		{"http://h/clusters/ci/dev", "ci", "", true},
		{"http://h/nodes/ci/hpc/db", "ci", "", true},
		{"http://h/clusters/cix/dev", "ci", "", false},
		{"http://h/clusters", "ci", "", false},
		{"", "ci", "dev", false},
		{"%zz", "ci", "dev", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("HX-Current-URL", c.url)
		assert.Equal(t, c.want, actOnPageOf(r, c.realm, c.cluster), c.url+" "+c.cluster)
	}
}

func TestActPreview_StatusLeadsToTheFirstProblem(t *testing.T) {
	f := newActFixture(t)
	f.st.SetDocker(store.DockerState{Checked: true, MemTotal: 32 << 30})

	body := f.post("/actions/create-cluster/preview", actForm("name", "My Cluster", "w0-count", "2", "dir", "/scratch/x")).Body.String()
	assert.Contains(t, body, `<span class="act-status" id="act-status" hx-swap-oob="innerHTML"><button type="button" class="pill bad nodot act-go" data-act-go="name" data-act-say="Name: `)
	assert.Contains(t, body, `data-act-key="1 name" aria-label="1 problem: Name. Go to the field." title="Go to the field">1 problem: Name <span aria-hidden="true">↑</span></button>`)
	assert.Contains(t, body, `<span class="hint act-tot">3 containers · 3 CPUs · 1.5 GiB of 32 GiB</span></span>`)
	assert.Contains(t, body, `<div class="callout bad act-probs" id="act-probs" tabindex="-1">`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "dev", "w0-count", "20", "cpus", "0", "dir", "/scratch/dev")).Body.String()
	assert.Contains(t, body, `>2 problems, first: Name <span aria-hidden="true">↑</span></button>`)
	assert.Contains(t, body, ` 2 problems in all." data-act-key="2 name"`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/hpc")).Body.String()
	assert.Contains(t, body, `<span class="pill ok" data-act-say="Ready. Create runs the command." data-act-key="ready 0">Ready</span><span class="hint act-tot">2 containers · 2 CPUs · 1 GiB of 32 GiB</span>`)

	// An error has no field: the status shows the list of problems.
	f.state.configDir = ""
	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `data-act-go="" data-act-say="sind ui has no state directory`)
	assert.Contains(t, body, `aria-label="1 problem. Show the problems." title="Show the problems">1 problem <span`)

}

// A dialog without a plan has its status in the footer too: the problems,
// what the form lacks, or the warnings, each beside the button.
func TestActPreview_FooterStatus(t *testing.T) {
	f := newActFixture(t)
	status := func(form string) string {
		body := f.post("/actions/power/preview", form).Body.String()
		_, rest, ok := strings.Cut(body, `<span class="act-status" id="act-status" hx-swap-oob="innerHTML">`)
		require.True(t, ok, body)
		s, _, _ := strings.Cut(rest, "</span>\n")
		return s
	}
	assert.Empty(t, status(actForm("cluster", "dev", "op", "reboot", "nodes", "worker-0")), "nothing to say")
	assert.Equal(t, `<span class="hint act-tot">Pick at least one node of dev.</span>`, status(actForm("cluster", "dev", "op", "on")))
	assert.Contains(t, status(actForm("cluster", "dev", "op", "on", "nodes", "worker-9")),
		`<button type="button" class="pill bad nodot act-go" data-act-go="" aria-label="1 problem. Show the problems." title="Show the problems">1 problem <span aria-hidden="true">↑`)
	// The first problem and the first warning say themselves beside
	// their count, so that they stay in view.
	assert.Contains(t, status(actForm("cluster", "dev", "op", "on", "nodes", "worker-9")),
		`<span class="act-fnote" title="nodes: cluster dev has no node worker-9">nodes: cluster dev has no node worker-9</span>`)
	assert.Contains(t, status(actForm("cluster", "dev", "op", "shutdown", "nodes", "controller")),
		`<button type="button" class="pill warn nodot act-go" data-act-go="@warn" aria-label="1 warning. Show them." title="Show the warnings">1 warning <span aria-hidden="true">↑</span></button><span class="act-fnote" title="controller runs slurmctld. While it is down, dev takes no jobs, and sbatch, squeue and sinfo fail.">`)
	assert.Equal(t, "2 warnings", actPreview{Cmd: action.Command{Warns: []string{"a", "b"}}}.WarningsText())
	assert.Equal(t, "1 problem", actPreview{Cmd: action.Command{Problems: []action.Problem{{}}}}.ProblemsText())
}

func TestActPreview_StatusParts(t *testing.T) {
	spec := actSpecs["create-cluster"]
	v := &View{Snap: store.New().Snapshot()}

	ready := actPreview{View: v, Spec: spec}
	assert.Equal(t, "Ready. Create runs the command.", ready.Say())
	assert.Equal(t, "ready 0", ready.SayKey())
	warned := actPreview{View: v, Spec: spec, Cmd: action.Command{Warns: []string{"a", "b"}}}
	assert.Equal(t, "Ready, with 2 warnings. Create runs the command.", warned.Say())
	assert.Equal(t, "ready 2", warned.SayKey())
	assert.Empty(t, ready.Totals(), "no plan")

	one := actPreview{View: v, Spec: spec, Blocked: true, Cmd: action.Command{
		Containers: 1, CPUs: 1, MemoryMB: 512,
		Problems: []action.Problem{{Text: "the config is wrong"}},
	}}
	assert.Equal(t, "1 container · 1 CPU · 512 MiB", one.Totals(), "without the memory of the host")
	assert.Equal(t, "1 problem", one.StatusText())
	assert.Equal(t, "Show the problems", one.StatusGo())
	assert.Equal(t, "the config is wrong", one.Say())
	assert.Equal(t, "1 ", one.SayKey())

	two := actPreview{View: v, Spec: spec, Blocked: true, Cmd: action.Command{Problems: []action.Problem{
		{Field: "cpus", Label: "CPUs", Text: "want 1 or more"}, {Field: "name", Label: "Name", Text: "taken"},
	}}}
	assert.Equal(t, "2 problems, first: CPUs", two.StatusText())
	assert.Equal(t, "Go to the field", two.StatusGo())
	assert.Equal(t, "CPUs: want 1 or more 2 problems in all.", two.Say())
	assert.Equal(t, "2 cpus", two.SayKey())

	// A form that lacks a value is not ready, with no problem to show.
	missing := actPreview{View: v, Spec: spec, Blocked: true}
	assert.Equal(t, 0, missing.Problems())
	assert.Equal(t, action.Problem{}, missing.First())
}

// The parts of a preview that the dialog shows in a way of its own: the
// first problem and the first warning in the footer, the names in the notes
// and the words of the command.
func TestActPreview_FooterParts(t *testing.T) {
	assert.Empty(t, actPreview{}.FirstWarn())
	assert.Equal(t, "a", actPreview{Cmd: action.Command{Warns: []string{"a", "b"}}}.FirstWarn())

	assert.Equal(t, "How many: too many", actPreview{Cmd: action.Command{Problems: []action.Problem{{Label: "How many", Text: "too many"}}}}.FirstText())
	assert.Equal(t, "it broke", actPreview{Err: "it broke"}.FirstText())

	assert.Equal(t, []string{"sind", "power", "reboot", "worker-4.dev"}, actPreview{Cmd: action.Command{Display: "sind power reboot worker-4.dev"}}.CmdWords())
}

// The picker of the Power dialog shows the Slurm state of each node, with
// the reason, and the quick picks count the nodes they tick.
func TestActDialog_PowerPickerShowsSlurm(t *testing.T) {
	f := newActFixture(t)
	f.st.SetStatus("sind", "dev", &cluster.Status{Nodes: []*cluster.NodeStatus{
		{Name: "controller.dev", Role: config.RoleController, Managed: true, Health: &cluster.NodeHealth{State: cluster.StateRunning}},
		{Name: "worker-0.dev", Role: config.RoleWorker, Managed: true, Health: &cluster.NodeHealth{State: cluster.StateRunning}},
		{Name: "worker-1.dev", Role: config.RoleWorker, Managed: true, Health: &cluster.NodeHealth{State: cluster.StateRunning}},
	}})
	f.st.SetSlurm("sind", "dev", &store.Slurm{Info: &cluster.SlurmInfo{Nodes: []slurm.NodeInfo{
		{Name: "worker-0", State: "idle"}, {Name: "worker-1", State: "drained", Reason: "memory errors"},
	}}})
	body := f.get("/dialogs/power?cluster=dev").Body.String()
	assert.Contains(t, body, `<span class="pill warn">drained</span> <span class="faint act-why2">memory errors</span>`)
	assert.Contains(t, body, `data-act-pick="worker-0">Idle <span class="act-qn">1</span>`)
	assert.Contains(t, body, `data-act-pick="worker-1">Drained <span class="act-qn">1</span>`)
	assert.Contains(t, body, `data-act-pick="worker-1">Need a look <span class="act-qn bad">1</span>`)
	assert.NotContains(t, body, "checked", "no action and no node is picked for the user")

	// A short confirmation names the node with its state.
	body = f.get("/dialogs/power?cluster=dev&op=resume&nodes=worker-1").Body.String()
	assert.Contains(t, body, `<li><b class="mono">worker-1</b> <span class="chip ">worker</span> <span class="pill warn" title="Slurm state">drained</span> <span class="faint act-why2">memory errors</span></li>`)
}
