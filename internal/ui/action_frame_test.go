// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

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
