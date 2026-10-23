// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

func TestActDialog_LayoutHasTheNotices(t *testing.T) {
	f := newActFixture(t)
	assert.Contains(t, f.get("/dialogs/connect").Body.String(), `<div id="layer"></div>`+"\n"+`<div class="act-notes" id="act-notes" role="status"></div>`)
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
	assert.Contains(t, body, `<span class="act-tot">3 containers · 3 CPUs · 1.5 GiB of 32 GiB</span></span>`)
	assert.Contains(t, body, `<div class="callout bad act-probs" id="act-probs" tabindex="-1">`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "dev", "w0-count", "20", "cpus", "0", "dir", "/scratch/dev")).Body.String()
	assert.Contains(t, body, `>2 problems, first: Name <span aria-hidden="true">↑</span></button>`)
	assert.Contains(t, body, ` 2 problems in all." data-act-key="2 name"`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/hpc")).Body.String()
	assert.Contains(t, body, `<span class="pill ok" data-act-say="Ready. Create runs the command." data-act-key="ready">Ready</span><span class="act-tot">2 containers · 2 CPUs · 1 GiB of 32 GiB</span>`)

	// An error has no field: the status shows the list of problems.
	body = f.post("/actions/create-cluster/preview", actForm("realm", "Bad Realm")).Body.String()
	assert.Contains(t, body, `data-act-go="" data-act-say="realm: `)
	assert.Contains(t, body, `aria-label="1 problem. Show the problems." title="Show the problems">1 problem <span`)

	// Dialogs without a plan have no status.
	assert.NotContains(t, f.post("/actions/power/preview", actForm("cluster", "dev", "op", "on", "nodes", "worker-0")).Body.String(), "act-status")
}

func TestActPreview_StatusParts(t *testing.T) {
	spec := actSpecs["create-cluster"]
	v := &View{Snap: store.New().Snapshot()}

	ready := actPreview{View: v, Spec: spec}
	assert.Equal(t, "Ready. Create runs the command.", ready.Say())
	assert.Equal(t, "ready", ready.SayKey())
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
