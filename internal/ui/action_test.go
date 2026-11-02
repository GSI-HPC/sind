// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// actFakeStarter records what the routes start and stop.
type actFakeStarter struct {
	mu        sync.Mutex
	specs     []run.Spec
	canceled  []string
	startErr  error
	cancelErr error
	shutdown  []time.Duration
}

func (f *actFakeStarter) Start(s run.Spec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, s)
	return "task1", f.startErr
}

func (f *actFakeStarter) Cancel(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canceled = append(f.canceled, id)
	return f.cancelErr
}

func (f *actFakeStarter) Shutdown(g time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdown = append(f.shutdown, g)
}

// actFixture is a server with the routes of the actions, a store holding a
// realm with two clusters, and a starter that starts nothing.
type actFixture struct {
	*testServer
	starter *actFakeStarter
	state   *actState
	saved   map[string]string
	// made are the directories that the actions made.
	made []string
}

func actNode(clusterName, short string, role config.Role) cluster.NodeSummary {
	return cluster.NodeSummary{
		Container: "sind-" + clusterName + "-" + short, Cluster: clusterName, Role: role,
		FQDN: cluster.DNSName(short, clusterName, "sind"), State: cluster.StateRunning,
	}
}

func newActFixture(t *testing.T) *actFixture {
	t.Helper()
	cfg, st := testConfig(t)
	for _, rt := range actRoutes() {
		cfg.Registry.AddRoute(rt)
	}
	f := &actFixture{starter: &actFakeStarter{}, saved: map[string]string{}}
	f.state = &actState{
		starter: f.starter, home: "/home/ada", configDir: "/state/sind/ui/configs",
		writeFile: func(path string, data []byte) error { f.saved[path] = string(data); return nil },
		stat: func(name string) (fs.FileInfo, error) {
			if strings.Contains(name, "missing") {
				return nil, fs.ErrNotExist
			}
			return os.Stat("/")
		},
		mkdirAll: func(path string) error { f.made = append(f.made, path); return nil },
	}
	cfg.Env.Put(actKey{}, f.state)
	st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}})
	st.SetClusters("sind", []cluster.Summary{{Name: "default"}, {Name: "dev"}})
	st.SetNodes("sind", []cluster.NodeSummary{
		actNode("dev", "controller", config.RoleController),
		actNode("dev", "worker-0", config.RoleWorker), actNode("dev", "worker-1", config.RoleWorker),
		actNode("default", "controller", config.RoleController),
	})
	f.testServer = newTestServerFrom(t, cfg, st).login()
	// No command creates a cluster of the fixture, so that the actions
	// fit its nodes; a test that needs one sets its runs.
	st.SetRuns(nil)
	return f
}

// actCode is the command of a preview as the dialog shows it: each line
// of display a line of its own, each of its words html-escaped, whole.
func actCode(display string) string {
	lines := strings.Split(display, "\n")
	for l, line := range lines {
		words := strings.Split(line, " ")
		for i, w := range words {
			words[i] = `<span class="act-cw">` + w + `</span>`
		}
		lines[l] = `<span class="act-cl">` + strings.Join(words, " ") + `</span>`
	}
	return `<code class="nop act-cls">` + strings.Join(lines, "") + "</code>"
}

// form encodes key and value pairs.
func actForm(kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Add(kv[i], kv[i+1])
	}
	return q.Encode()
}

// shown returns the fingerprint that the preview of the form holds.
func (f *actFixture) shown(t *testing.T, id string, form string) string {
	t.Helper()
	w := f.post("/actions/"+id+"/preview", form)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	_, rest, ok := strings.Cut(w.Body.String(), `name="shown" value="`)
	require.True(t, ok, w.Body.String())
	v, _, _ := strings.Cut(rest, `"`)
	require.Len(t, v, 64)
	return v
}

func TestActRoutes_AreRegistered(t *testing.T) {
	var patterns []string
	for _, rt := range defaultRegistry.snapshot().routes {
		patterns = append(patterns, rt.Pattern)
	}
	assert.Subset(t, patterns, []string{
		"GET /dialogs/{action}", "POST /dialogs/{action}", "POST /actions/{action}/preview", "POST /actions/{action}/run", "POST /tasks/{id}/cancel",
		"POST /tasks/{id}/retry",
	})
	var services []string
	for _, s := range defaultRegistry.snapshot().services {
		services = append(services, s.Name)
	}
	assert.Contains(t, services, "act")
}

func TestActDialog_EveryActionOpens(t *testing.T) {
	f := newActFixture(t)
	for _, tc := range []struct {
		id, query string
		want      []string
	}{
		{"create-cluster", "", []string{"New cluster", `id="act-name" name="name" value="hpc"`, `data-act-step="w0-count"`, `id="act-w0-count" name="w0-count" value="2"`,
			`id="act-db" name="db" aria-`, `id="act-submitter" name="submitter" aria-`, `id="act-backup" name="backup" aria-`, `id="act-pull" name="pull" checked`,
			`data-act-start="quick" data-act-set="api=&amp;backup=&amp;db=&amp;submitter=&amp;workers=2" aria-pressed="true"`, `data-act-start="history"`, `<input type="hidden" id="act-start" name="start" value="">`,
			`<aside class="act-pcard" id="act-plan" aria-label="Plan">`, `<span class="act-status" id="act-status"></span>`, `id="act-say" role="status"`, `value="~/sind/hpc"`, `data-act-follow`,
			"ghcr.io/gsi-hpc/sind-node", `value="5m" checked`, `id="act-pull-auto" name="pull_auto" value="1"`,
			`<option value="sind" selected>sind · 2 clusters</option><option value="">New realm…</option></select>`, `id="act-realm" name="realm" value="sind" hidden`, "A new name makes a new realm.",
			`<div class="pg-files"><div class="pg-fmap"><span class="pg-fside"><small>On this host</small><b class="mono"><span class="tok">~/</span><span class="tok">sind/</span><span class="tok">hpc</span></b></span>`,
			// A size chip is named by its node and its value; the controls of a
			// worker group by the workers it makes.
			`aria-labelledby="act-czl-controller act-chip-controller"><span class="vh" id="act-czl-controller">Size of controller</span>`,
			`aria-labelledby="act-czl-w0 act-range-w0 act-chip-w0"><span class="vh" id="act-czl-w0">Size of workers</span>`,
			`aria-label="Fewer in workers" aria-labelledby="act-w0-less act-range-w0"`, `aria-label="More in workers" aria-labelledby="act-w0-more act-range-w0"`,
			`aria-label="Remove workers" aria-labelledby="act-w0-del act-range-w0"`}},
		{"create-cluster", "wait=7m&pull=&image=ghcr.io/gsi-hpc/sind-node:26.05.4", []string{`value="other" checked`, `name="wait_other" value="7m"`, `id="act-pull" name="pull" aria-`}},
		{"create-cluster", "wait=0&image=ghcr.io/gsi-hpc/sind-node:26.05.4", []string{`value="0" checked`, `id="act-pull" name="pull" aria-`, `name="pull_auto"`}},
		{"create-cluster", "realm=ci&name=big", []string{`value="big"`, `value="~/sind/big"`, `name="realm" value="ci" placeholder`, `<option value="" selected>New realm…</option>`}},
		{"create-cluster", "name=dev", []string{`value="dev"`}},
		{"create-cluster", "dir=/scratch", []string{`name="name" value="hpc"`, `value="/scratch"`}},
		{"create-worker", "cluster=dev", []string{"Add workers to dev", `name="count" value="1"`, `name="cluster" value="dev"`, "of worker-1, the newest worker", `placeholder="as worker-1"`,
			`<span class="act-checking" id="act-checking" aria-hidden="true">Checking…</span>`, `hx-indicator="#act-checking"`}},
		{"create-worker", "cluster=default", []string{"sind&#39;s sizes and the image of the controller", `placeholder="sind&#39;s default"`}},
		{"delete-cluster", "cluster=dev", []string{"Delete cluster dev", "Deletes cluster dev of realm sind. This cannot be undone.", `Type <b class="mono" id="act-confirm-word">dev</b><span id="act-confirm-what"></span> to confirm`,
			`autocapitalize="none" autocorrect="off" spellcheck="false" aria-describedby="act-why"`, `btn danger solid`, `disabled aria-describedby="act-why"><span class="act-okl">Delete cluster</span><kbd`}},
		{"delete-worker", "cluster=dev&nodes=worker-1", []string{"Delete worker-1.dev", `value="worker-1" id="act-n-worker-1" checked`, `value="worker-0" id="act-n-worker-0">`, `id="act-confirm-word">worker-1</b><span id="act-confirm-what">, the name of the worker,</span> to confirm`,
			"<th>Slurm</th>", `<span class="lbl">Workers of dev</span>`}},
		{"delete-worker", "cluster=dev&nodes=worker-0,worker-1", []string{"Delete 2 workers of dev", `id="act-confirm-word">2</b><span id="act-confirm-what">, the number of workers,</span> to confirm`}},
		{"delete-worker", "cluster=gone", []string{"Delete workers of gone", "sind ui sees no worker of gone."}},
		// Opened for an action and nodes, the dialog is a short confirmation,
		// with the picker folded away.
		{"power", "cluster=dev&nodes=worker-0,worker-1&op=cut", []string{"Cut power to 2 workers of dev", "Kills the containers at once", `value="worker-0" id="act-n-worker-0" checked`, `value="worker-1" id="act-n-worker-1" checked`, `value="controller" id="act-n-controller">`,
			`value="cut" checked`, `<div class="act-og danger"><span class="act-ogl" id="act-og-danger" aria-hidden="true">Can lose data</span><div class="seg act-seg" role="group" aria-labelledby="act-og-danger">`, `<li><b class="mono">worker-0</b> <span class="chip ">worker</span>`,
			`data-act-change aria-controls="act-pick" aria-expanded="false">Change nodes…</button>`, `<div class="act-pick" id="act-pick" hidden>`,
			// The closed section names the other actions that fit the nodes.
			`<summary><b>Other actions</b><span class="pg-caret" aria-hidden="true"></span> <span class="faint act-otherops">Shut down · Reboot · Freeze · Power-cycle</span></summary>`,
			`id="act-nodeset" value="worker-[0-1]"`}},
		{"power", "cluster=dev&nodes=worker-0&op=reboot", []string{"Reboot worker-0.dev", "Stops, then starts the container. Running jobs on it fail.", `<div class="act-pick" id="act-pick" hidden>`}},
		// An action that fits none of the nodes, as of a stale link, opens
		// the picker, which says why.
		{"power", "cluster=dev&nodes=worker-0&op=on", []string{"Start worker-0.dev", `<div class="act-pick" id="act-pick">`, `value="worker-0" id="act-n-worker-0" checked`}},
		// Without nodes, or with one that is gone, it is the picker.
		{"power", "cluster=dev&op=drain&reason=disk", []string{"Drain nodes of dev", `value="drain" checked`, `name="reason" value="disk"`, `<div class="fld act-reason"><label for="act-reason">`,
			`<div class="act-pick" id="act-pick">`, `<fieldset class="fld act-opseg"><legend class="lbl">Action</legend>`,
			`<button type="button" class="btn sm act-qp" data-act-pick="worker-0,worker-1">All workers <span class="act-qn">2</span></button>`,
			`<button type="button" class="btn sm act-qp" data-act-pick="" disabled>Idle <span class="act-qn">0</span></button>`,
			`<tr data-act-node="controller" data-act-offs="{`}},
		{"power", "cluster=dev&nodes=worker-9&op=reboot", []string{"Reboot nodes of dev", `<div class="act-pick" id="act-pick">`}},
		{"power", "cluster=dev&nodes=worker-0&op=reboot&nodes=worker-1", []string{"Reboot 2 workers of dev"}},
		{"power", "cluster=dev&nodes=worker-0,controller&op=reboot", []string{"Reboot 2 nodes of dev"}},
		{"power", "cluster=dev&op=resume", []string{`value="resume" checked`}},
		{"power", "cluster=gone&op=resume", []string{"Resume nodes of gone", "sind ui sees no node of gone yet."}},
		{"power", "cluster=dev&op=stop", []string{`value="shutdown" checked`}},
		// No action is picked for the user.
		{"power", "cluster=dev&op=melt", []string{"Power and drain: nodes of dev", "Drain and Resume change what Slurm does", `name="op" value="shutdown">`}},
		{"connect", "container=kind-cp", []string{"Connect a container to realm sind", `name="other" value="kind-cp"`, `<input type="hidden" name="realm" value="sind">`, "Write the name of one that starts later."}},
		{"disconnect", "", []string{"Disconnect a container", "connected to realm sind"}},
		{"delete-realm", "", []string{"Delete realm sind", "Deletes every cluster of realm sind", `Type <b class="mono" id="act-confirm-word">sind</b><span id="act-confirm-what"></span> to confirm`}},
	} {
		t.Run(tc.id+"?"+tc.query, func(t *testing.T) {
			at := cmp.Or(actPages[tc.id], "/dialogs/"+tc.id)
			w := f.get(at + "?" + tc.query)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := w.Body.String()
			assert.Contains(t, body, `hx-post="/actions/`+tc.id+`/preview"`)
			assert.Contains(t, body, `hx-post="/actions/`+tc.id+`/run"`)
			assert.Contains(t, body, `name="csrf" value="`+f.csrf+`"`)
			for _, want := range tc.want {
				assert.Contains(t, body, want)
			}
		})
	}
}

func TestActDialog_CreateClusterKeepsTheLinksValues(t *testing.T) {
	f := newActFixture(t)
	w := f.get("/clusters/new?name=x&memory=3g&dtype=volume&monitoring=on")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `name="memory" value="3g"`)
	assert.Contains(t, body, `value="volume" checked`)
	assert.Contains(t, body, `name="monitoring" checked`)
	assert.Contains(t, body, `<div class="fld act-wide" data-act-dtype="hostPath" hidden>`, "a volume needs no directory")
	assert.Contains(t, body, `<label class="act-nr act-box" data-act-dtype="hostPath" hidden>`)
	assert.Contains(t, body, `<p class="hint act-wide" data-act-dtype="volume">Docker keeps the files in the volume above`)
}

func TestActDialog_NewClusterTakesAFreeName(t *testing.T) {
	f := newActFixture(t)
	f.st.SetClusters("sind", []cluster.Summary{{Name: "hpc"}, {Name: "hpc-2"}})

	body := f.get("/clusters/new").Body.String()

	assert.Contains(t, body, `id="act-name" name="name" value="hpc-3" data-act-select`, "the script selects a default name")
	// The roles lead with what the node does, and explain their words
	// outside the label of the checkbox, which say what runs on it.
	assert.Contains(t, body, `<span class="act-purp"><label for="act-db"><b data-word="db">db</b> keeps job history for sacct</label> <span class="act-d" id="act-hint-db"><span data-word="mariadb">MariaDB</span>, <span data-word="slurmdbd">slurmdbd</span></span><span class="act-opt">optional</span></span>`)
	assert.Contains(t, body, `<label for="act-backup"><b data-word="controller-backup">backup</b> takes over when the controller fails</label> <small>controller-backup</small>`)
	assert.Contains(t, body, `<span class="act-purp"><b data-word="controller">controller</b> schedules your jobs <span class="act-d"><span data-word="slurmctld">slurmctld</span></span></span>`)
	assert.Contains(t, body, `value="~/sind/hpc-3" autocapitalize="off" spellcheck="false" data-act-follow`)
	assert.Contains(t, body, `<b data-word="worker">workers</b> run your jobs`)
	assert.NotContains(t, f.get("/clusters/new?dir=/x").Body.String(), "data-act-follow", "a typed directory stays")
	assert.Contains(t, f.get("/clusters/new?name=lab").Body.String(), `id="act-name" name="name" value="lab" autocomplete="off"`, "a name of the link stays as it is")

	body = f.get("/clusters/new?wg=w0&wg=wabc&w0-count=1&wabc-count=2").Body.String()
	assert.Contains(t, body, `<b data-word="worker">workers 1</b> run your jobs`)
	assert.Contains(t, body, `<span class="vh" id="act-czl-wabc">Size of workers 2</span>`)
	assert.Contains(t, body, `aria-label="Fewer in workers 2" aria-labelledby="act-wabc-less act-range-wabc">`)
	assert.Contains(t, body, `aria-label="More in workers 2" aria-labelledby="act-wabc-more act-range-wabc">`)
	assert.Contains(t, body, `aria-label="Remove workers 2" aria-labelledby="act-wabc-del act-range-wabc">`)
	assert.Contains(t, body, `<template id="act-wg-tpl"><div class="act-nr act-wgrow" id="act-wg-__WG__">`)
}

func TestActDialog_Errors(t *testing.T) {
	f := newActFixture(t)
	for _, tc := range []struct {
		target string
		code   int
		want   string
	}{
		{"/dialogs/nope", http.StatusNotFound, ""},
		{"/dialogs/power", http.StatusBadRequest, "cluster: say which cluster"},
		{"/dialogs/power?cluster=No!", http.StatusBadRequest, "cluster: invalid cluster name"},
		{"/dialogs/connect?realm=No!", http.StatusBadRequest, "realm: invalid realm name"},
		{"/dialogs/connect?container=" + strings.Repeat("x", 300), http.StatusBadRequest, "longer than"},
	} {
		w := f.get(tc.target)
		assert.Equal(t, tc.code, w.Code, tc.target)
		assert.Contains(t, w.Body.String(), tc.want, tc.target)
	}
}

func TestActPreview_ShowsWhoHoldsTheLock(t *testing.T) {
	f := newActFixture(t)
	f.st.SetLock("sind", &state.LockHolder{Command: "sind create cluster x", Host: "node1", PID: 77, Since: time.Unix(0, 0)})

	body := f.post("/actions/delete-cluster/preview", actForm("cluster", "dev")).Body.String()
	assert.Contains(t, body, `holds the lock of realm <b class="mono">sind</b>`)
	assert.Contains(t, body, "sind create cluster x (pid 77 on node1, since ")
	assert.Contains(t, body, "waits until it ends")

	// A power command that only stops containers takes no lock.
	power := actForm("cluster", "dev", "nodes", "worker-0")
	assert.NotContains(t, f.post("/actions/power/preview", power+"&op=shutdown").Body.String(), "holds the lock")
	assert.Contains(t, f.post("/actions/power/preview", power+"&op=reboot").Body.String(), "holds the lock")
	assert.NotContains(t, f.post("/actions/delete-realm/preview", actForm("realm", "ci")).Body.String(), "holds the lock")
}

func TestActPreview_ShowsTheCommand(t *testing.T) {
	f := newActFixture(t)

	w := f.post("/actions/power/preview", actForm("cluster", "dev", "op", "reboot", "nodes", "worker-0", "nodes", "worker-1"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, actCode("sind power reboot &#39;worker-[0-1].dev&#39;"))
	assert.Contains(t, body, `data-pg-copy-text="sind power reboot &#39;worker-[0-1].dev&#39;"`)
	assert.Contains(t, body, `id="act-ok" aria-keyshortcuts="Control+Enter Meta+Enter" hx-swap-oob="true"><span class="act-okl">Reboot 2 workers</span><kbd`)
	assert.Contains(t, body, `<h2 id="act-title" hx-swap-oob="true">Reboot 2 workers of dev</h2><p id="act-lead" hx-swap-oob="true">Stops, then starts the containers`, "the title follows the form")
	assert.NotContains(t, body, `disabled`)
	assert.NotContains(t, body, "act-yaml")
}

func TestActPreview_DangerPaintsTheButton(t *testing.T) {
	f := newActFixture(t)
	power := actForm("cluster", "dev", "nodes", "worker-0")

	for op, danger := range map[string]bool{"cut": true, "cycle": true, "on": false, "melt": false} {
		body := f.post("/actions/power/preview", power+"&op="+op).Body.String()
		assert.Equal(t, danger, strings.Contains(body, `class="btn danger solid" id="act-ok"`), op)
	}
	body := f.post("/actions/delete-cluster/preview", actForm("cluster", "dev")).Body.String()
	assert.Contains(t, body, `class="btn danger solid" id="act-ok"`)
	body = f.post("/actions/connect/preview", actForm("container", "x")).Body.String()
	assert.Contains(t, body, `class="btn primary" id="act-ok"`)
}

func TestActPreview_Refusals(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/power/preview", actForm("cluster", "dev", "op", "on", "nodes", "worker-9")).Body.String()
	assert.Contains(t, body, `callout bad`)
	assert.Contains(t, body, "nodes: cluster dev has no node worker-9")
	assert.Contains(t, body, `disabled><span class="act-okl">Start worker-9</span><kbd`, "the button names the action")
	assert.NotContains(t, body, `name="shown"`)

	// An action that fits none of the ticked nodes does not run.
	body = f.post("/actions/power/preview", actForm("cluster", "dev", "op", "on", "nodes", "worker-0")).Body.String()
	assert.Contains(t, body, `<div class="callout bad">Start fits none of the ticked nodes: worker-0 runs already. Pick another action or other nodes.</div>`)
	assert.Contains(t, body, `disabled><span class="act-okl">Start worker-0</span><kbd`)
	assert.NotContains(t, body, `name="shown"`)

	// A value that the form lacks is a hint in the footer, not an error.
	for _, c := range []struct{ id, form, hint string }{
		{"power", actForm("cluster", "dev", "op", "on"), "Pick at least one node of dev."},
		{"connect", actForm("container", " "), "Pick a container, or write the name of one, such as kind-control-plane."},
	} {
		body = f.post("/actions/"+c.id+"/preview", c.form).Body.String()
		assert.Contains(t, body, `<span class="hint act-tot">`+c.hint+`</span>`, c.id)
		assert.NotContains(t, body, `act-missing`, c.id)
		assert.NotContains(t, body, `callout bad`, c.id)
		assert.Regexp(t, `id="act-ok"[^>]* disabled>`, body, c.id)
		assert.NotContains(t, body, `name="shown"`, c.id)
	}

	// The typed confirmation unlocks the button.
	del := actForm("cluster", "dev")
	body = f.post("/actions/delete-cluster/preview", del).Body.String()
	assert.Contains(t, body, `disabled aria-describedby="act-why"><span class="act-okl">Delete cluster dev</span><kbd`)
	assert.Contains(t, body, `<span id="act-why" hx-swap-oob="innerHTML">The button stays off until the name matches.</span>`)
	assert.Contains(t, body, `<b class="mono" id="act-confirm-word" hx-swap-oob="true">dev</b>`)
	body = f.post("/actions/delete-cluster/preview", del+"&confirm=dev").Body.String()
	assert.NotContains(t, body, `disabled`)
	assert.Contains(t, body, `<span id="act-why" hx-swap-oob="innerHTML"></span>`)
	assert.Contains(t, f.post("/actions/delete-cluster/preview", del+"&confirm=Dev").Body.String(), "Case matters: type dev, not Dev.")
	assert.Contains(t, f.post("/actions/delete-cluster/preview", del+"&confirm=de").Body.String(), "de is not dev. Type the name as it is shown.")

	// One worker is confirmed with its own name, more with their count:
	// the cluster stays.
	body = f.post("/actions/delete-worker/preview", actForm("cluster", "dev", "nodes", "worker-0")).Body.String()
	assert.Contains(t, body, `id="act-confirm-word" hx-swap-oob="true">worker-0</b><span id="act-confirm-what" hx-swap-oob="true">, the name of the worker,</span>`)
	assert.Contains(t, body, `><span class="act-okl">Delete worker-0</span><kbd`)
	assert.NotContains(t, f.post("/actions/delete-worker/preview", actForm("cluster", "dev", "nodes", "worker-0", "confirm", "worker-0 ")).Body.String(), "disabled")
	body = f.post("/actions/delete-worker/preview", actForm("cluster", "dev", "nodes", "worker-0,worker-1")).Body.String()
	assert.Contains(t, body, `id="act-confirm-word" hx-swap-oob="true">2</b><span id="act-confirm-what" hx-swap-oob="true">, the number of workers,</span>`)
	assert.Contains(t, body, `><span class="act-okl">Delete 2 workers</span><kbd`)
	assert.Contains(t, body, `<span id="act-why" hx-swap-oob="innerHTML">The button stays off until you type the number.</span>`)
	assert.Contains(t, body, `<h2 id="act-title" hx-swap-oob="true">Delete 2 workers of dev</h2>`)
	assert.NotContains(t, f.post("/actions/delete-worker/preview", actForm("cluster", "dev", "nodes", "worker-0,worker-1", "confirm", "2")).Body.String(), "disabled")
	// Before a worker is picked, there is nothing to type.
	body = f.post("/actions/delete-worker/preview", actForm("cluster", "dev")).Body.String()
	assert.Contains(t, body, `id="act-confirm-word" hx-swap-oob="true"></b><span id="act-confirm-what" hx-swap-oob="true"></span>`, "nothing to type, nothing to say about it")
	assert.Contains(t, body, `<h2 id="act-title" hx-swap-oob="true">Delete workers of dev</h2>`)
}

func TestActPreview_ListsWhatGoesAndStays(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/delete-cluster/preview", actForm("cluster", "dev")).Body.String()
	assert.Contains(t, body, `<div class="callout bad act-list"><b>Goes</b><ul><li>3 containers: controller, worker-0, worker-1</li>`)
	assert.Contains(t, body, `<div class="callout act-list"><b>Stays</b><ul><li>the host directories it mounts, if any</li><li>the other cluster of realm sind, and its mesh</li></ul></div>`)

	body = f.post("/actions/delete-realm/preview", actForm("realm", "sind")).Body.String()
	assert.Contains(t, body, `<div class="act-lw"><table class="act-loss"><thead><tr><th>Cluster</th><th>Goes</th><th>Stays</th></tr></thead><tbody>
<tr><td class="act-lc"><b class="mono">default</b><small>1 node</small></td>
  <td class="act-lg" data-label="Goes">1 container, its network</td>
  <td class="act-ls" data-label="Stays"><span class="act-lok">the host directories it mounts, if any</span></td></tr>`)
	assert.Contains(t, body, `<span class="act-lok">the containers that sind did not make, if any</span><small>sind detaches them</small>`)
	assert.Contains(t, body, `<p class="hint act-also">Also stays: the images.</p>`)
	assert.Contains(t, body, actCode("sind --realm sind delete realm"))
	assert.NotContains(t, body, "act-ack", "no data volume goes")

	// Where nothing stays of a row, the row says so.
	f.st.SetMesh("sind", store.Mesh{Info: &mesh.Info{}})
	body = f.post("/actions/delete-realm/preview", actForm("realm", "sind")).Body.String()
	assert.Contains(t, body, `<td class="act-ls" data-label="Stays"><span class="faint">nothing</span></td>`)
}

func TestActDelete_TickForTheData(t *testing.T) {
	f := newActFixture(t)
	f.st.SetVolumes("sind", []cluster.VolumeSummary{{Name: "sind-dev-data"}, {Name: "sind-dev-config"}})

	// The data volume is named in red in its row, and the button stays off
	// until the box about it is ticked.
	form := actForm("clusters", "sind/dev")
	body := f.post("/actions/delete-clusters/preview", form+"&confirm=1").Body.String()
	assert.Contains(t, body, `<td class="act-lg" data-label="Goes"><span class="act-lbad">the data volume sind-dev-data: every file in /data</span><small>3 containers, its network, its config volume</small></td>`)
	assert.Contains(t, body, `<label class="callout bad act-ack"><input type="checkbox" id="act-ack" name="ack" data-act-hold><span>I know that the data volume <b class="mono">sind-dev-data</b> of dev goes, with every file in /data.</span></label>`)
	assert.Contains(t, body, `<span id="act-why" hx-swap-oob="innerHTML">The button stays off until you tick the box about the data.</span>`)
	assert.Regexp(t, `id="act-ok"[^>]* disabled `, body)
	body = f.post("/actions/delete-clusters/preview", form+"&confirm=1&ack=on").Body.String()
	assert.Contains(t, body, `name="ack" data-act-hold checked>`)
	assert.NotRegexp(t, `id="act-ok"[^>]* disabled `, body)

	// A run without the tick runs nothing.
	shown := f.shown(t, "delete-clusters", form)
	w := f.postHTMX("/actions/delete-clusters/run", form+"&shown="+shown+"&confirm=1")
	assert.Contains(t, w.Body.String(), "The button stays off until you tick the box about the data.")
	assert.Empty(t, f.starter.specs)
	w = f.postHTMX("/actions/delete-clusters/run", form+"&shown="+shown+"&confirm=1&ack=on")
	assert.Equal(t, "/clusters", w.Header().Get("HX-Redirect"), w.Body.String())
	require.Len(t, f.starter.specs, 1)

	// The single delete asks for the tick too.
	body = f.post("/actions/delete-cluster/preview", actForm("cluster", "dev", "confirm", "dev")).Body.String()
	assert.Contains(t, body, "I know that the data volume")
	assert.Regexp(t, `id="act-ok"[^>]* disabled `, body)
}

func TestActConnect_ProblemsAtTheirFields(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/dialogs/connect").Body.String()
	assert.Contains(t, body, `aria-describedby="act-err-container act-hint-container"><span class="err" id="act-err-container"></span>`)
	assert.NotContains(t, body, `id="act-realm"`, "the dialog connects to the realm of its link")
	assert.NotContains(t, body, "act-realms", "one realm, none to switch to")

	body = f.post("/actions/connect/preview", actForm("realm", "No", "container", "kind-cp")).Body.String()
	assert.Contains(t, body, `<div class="callout bad">realm: use `)
	assert.Regexp(t, `id="act-ok"[^>]* disabled>`, body)

	body = f.post("/actions/connect/preview", actForm("other", "a;b")).Body.String()
	assert.Contains(t, body, `<span class="err" id="act-err-container" hx-swap-oob="innerHTML">use letters, digits, _, . and - in the name of a container, and start it with a letter or a digit</span>`)
	assert.Contains(t, body, `<p class="hint act-missing">Fix the field above to see the command.</p>`)

	body = f.post("/actions/connect/preview", actForm("realm", "new", "other", "kind-cp")).Body.String()
	assert.Contains(t, body, actCode("sind --realm new connect container kind-cp"))
	assert.Contains(t, body, `<div class="callout warn">sind ui sees no realm new. sind connects a container only to a realm with a cluster, whose mesh it joins; create a cluster in new first.</div>`)
	assert.Contains(t, body, `><span class="act-okl">Connect kind-cp</span><kbd`)

	// A dialog without fields of its own keeps the error in the preview.
	body = f.post("/actions/disconnect/preview", actForm("realm", "No", "container", "kind-cp")).Body.String()
	assert.Contains(t, body, `<div class="callout bad">realm: use `)
	assert.NotContains(t, body, "act-err-realm")
}

func TestActConnect_PicksAContainer(t *testing.T) {
	f := newActFixture(t)
	f.st.SetRealms([]cluster.RealmSummary{{Name: "ci", Clusters: 1}, {Name: "sind", Clusters: 2}})
	f.st.SetMesh("ci", store.Mesh{External: []string{"kind-control-plane"}})
	f.st.SetMesh("sind", store.Mesh{
		Info:    &mesh.Info{Network: "sind-mesh"},
		Records: []mesh.DNSRecord{{Hostname: "controller.dev.sind.sind"}, {Hostname: "worker-0.dev.sind.sind"}},
		Connectable: []store.Outside{
			{Name: "db", Image: "postgres:16", State: "exited", Status: "Exited (0) 2 days ago", Networks: []string{"bridge"}},
			{Name: "kind-control-plane", Image: "kindest/node:v1.31.0", State: "running", Status: "Up 5 hours", Networks: []string{"kind", "ci-mesh"}},
			{Name: "my-app", Image: "python:3.12-slim", State: "running", Status: "Up 2 hours"},
		},
	})

	body := f.get("/dialogs/connect?realm=sind&container=my-app").Body.String()
	assert.Contains(t, body, "<h2 id=\"act-title\">Connect a container to realm sind</h2>")
	assert.Contains(t, body, "The container joins the mesh and the cluster networks of realm sind. It then reaches the nodes by name.")
	// The realms to switch to open the dialog again, with the container
	// picked so far.
	assert.Contains(t, body, `<button type="button" hx-get="/dialogs/connect?realm=ci" hx-include=".act-olist input[name=container], #act-container" hx-target="#layer" hx-swap="innerHTML" aria-pressed="false">ci</button>`)
	assert.Contains(t, body, `aria-pressed="true" class="on">sind</button>`)
	// The containers that run come first, with their image, networks and
	// uptime, the one of the link picked; one that another realm has says
	// so, and one that does not run is off.
	assert.Contains(t, body, `<input type="radio" name="container" value="my-app" checked data-act-focus>`)
	assert.Contains(t, body, `<small>python:3.12-slim · up 2 hours</small>`)
	assert.Contains(t, body, `<small>kindest/node:v1.31.0 · networks kind, ci-mesh · up 5 hours</small></span><span class="chip">also in realm ci</span>`)
	assert.Contains(t, body, `<label class="act-orow off"><input type="radio" name="container" value="db" disabled>`)
	assert.Contains(t, body, `<small>postgres:16 · stopped. Start it first.</small></span><span class="pill">exited</span>`)
	assert.Less(t, strings.Index(body, `value="my-app"`), strings.Index(body, `value="db"`))
	assert.Contains(t, body, `<input type="radio" name="container" value="" id="act-other-pick">`)

	// A name of the link that the list does not offer is another name.
	body = f.get("/dialogs/connect?realm=sind&container=db").Body.String()
	assert.Contains(t, body, `value="" id="act-other-pick" checked>`)
	assert.Contains(t, body, `name="other" value="db"`)
	assert.NotContains(t, body, `value="db" disabled checked`)
	body = f.get("/dialogs/connect?realm=sind&container=&other=later-app").Body.String()
	assert.Contains(t, body, `value="" id="act-other-pick" checked>`)
	assert.Contains(t, body, `name="other" value="later-app"`)

	body = f.post("/actions/connect/preview", actForm("realm", "sind", "container", "my-app")).Body.String()
	assert.Contains(t, body, `<div class="callout"><span class="mono">my-app</span> joins <span class="mono">sind-mesh</span> and the networks of default and dev. It can then reach <span class="mono">controller.dev.sind.sind</span> and the other node by name.</div>`)
	assert.Contains(t, body, `><span class="act-okl">Connect my-app</span><kbd`)
}

func TestActPreview_AddWorkersShowsTheirSize(t *testing.T) {
	f := newActFixture(t)
	f.state.workers = newActWorkers(func(_ context.Context, name docker.ContainerName) (*docker.ContainerInfo, error) {
		info := &docker.ContainerInfo{Name: name}
		info.HostConfig.NanoCPUs, info.HostConfig.Memory = 2e9, 1<<30
		return info, nil
	}, nil)

	body := f.post("/actions/create-worker/preview", actForm("cluster", "dev", "count", "3")).Body.String()
	assert.Contains(t, body, `<p class="act-sum">3 × 2 CPUs, 1 GiB: worker-2 … worker-4</p>`)
	assert.Contains(t, body, `><span class="act-okl">Add 3 workers</span><kbd`)
	body = f.post("/actions/create-worker/preview", actForm("cluster", "dev", "count", "1", "memory", "2g")).Body.String()
	assert.Contains(t, body, `<p class="act-sum">1 × 2 CPUs, 2 GiB: worker-2</p>`)
}

func TestActPreview_AddWorkersShowsTheHost(t *testing.T) {
	f := newActFixture(t)
	f.state.workers = newActWorkers(func(_ context.Context, name docker.ContainerName) (*docker.ContainerInfo, error) {
		info := &docker.ContainerInfo{Name: name}
		info.HostConfig.NanoCPUs, info.HostConfig.Memory = 2e9, 1<<30
		return info, nil
	}, nil)
	f.st.SetDocker(store.DockerState{Checked: true, MemTotal: 8 << 30, NCPU: 4})
	f.st.SetDoctor(&store.DoctorReport{Checks: []store.DoctorCheck{{Name: "inotify", Status: "warning", Detail: "max_user_instances 128"}}})

	// 4 nodes of 1 GiB run; 3 more come to 7 GiB of 8 GiB.
	body := f.post("/actions/create-worker/preview", actForm("cluster", "dev", "count", "3")).Body.String()
	assert.Contains(t, body, `<div class="act-hb high">`)
	assert.Contains(t, body, `<b>Memory of the Docker host</b><span class="mono">7 GiB of 8 GiB</span>`)
	assert.Contains(t, body, `<rect class="now" width="500" height="10"/><rect class="add" x="500" width="375" height="10"/></svg>`)
	assert.Contains(t, body, `the 4 nodes that run now: 4 GiB</span>`)
	assert.Contains(t, body, `the new workers: 3 GiB</span>`)
	assert.Contains(t, body, `<span>Nodes on the host then</span><span class="mono">7</span>`)
	assert.Contains(t, body, `<div class="callout warn">The memory limits of the nodes then come to 7 GiB, 87% of the 8 GiB of the Docker host`)
	assert.Contains(t, body, `><span class="act-okl">Add 3 workers</span><kbd`)
	assert.NotContains(t, body, "inotify")
	assert.Contains(t, body, `>1 warning <span aria-hidden="true">↑</span></button><span class="act-fnote" title="The memory limits of the nodes then come to 7 GiB`)
	assert.NotContains(t, body, `name="overcommit"`)
	assert.Contains(t, body, `<div class="callout">The new workers join the partition <span class="mono">all</span>. Your own <span class="mono">PartitionName</span> lines do not change.`)

	// 6 more come to 10 GiB, and to 10 nodes: the host is a mark on the
	// bar, and How many says how many fit.
	body = f.post("/actions/create-worker/preview", actForm("cluster", "dev", "count", "6")).Body.String()
	assert.Contains(t, body, `<div class="act-hb over">`)
	assert.Contains(t, body, `<rect class="add" x="400" width="600" height="10"/><rect class="host" x="800" width="5" height="10"/></svg>`)
	assert.Contains(t, body, `the new workers: 6 GiB, more than the host has</span>`)
	assert.Contains(t, body, `<li><a href="#act-count" data-act-jump="count">How many</a>: the host has room for about 4 more workers of 1 GiB. Add fewer,`)
	assert.Contains(t, body, `<span class="err" id="act-err-count" hx-swap-oob="innerHTML">the host has room for about 4 more workers of 1 GiB.`)
	assert.Contains(t, body, `aria-label="1 problem: How many.`)
	assert.Contains(t, body, `<span class="act-fnote" title="the host has room for about 4 more workers of 1 GiB.`, "the pill names the field, the note leaves it out")
	assert.Contains(t, body, `<input type="checkbox" id="act-overcommit" name="overcommit" data-act-hold`)
	assert.Contains(t, body, `<div class="callout warn act-hw"><b>The host can run out of inotify instances.</b> With the new workers, this host runs 10 nodes. The Doctor says that 10 and more can fail to start.`)
	assert.Contains(t, body, `<details class="act-fix" id="act-fix-inotify" data-act-hold><summary>Show fix</summary>`)
	assert.Contains(t, body, `<a href="/doctor">The Doctor</a>: <span class="mono">max_user_instances 128</span>`)
	assert.Contains(t, body, `id="act-ok" aria-keyshortcuts="Control+Enter Meta+Enter" hx-swap-oob="true" disabled>`)

	// Added anyway, the limits over the host are a warning.
	body = f.post("/actions/create-worker/preview", actForm("cluster", "dev", "count", "6", "overcommit", "on")).Body.String()
	assert.Contains(t, body, `>2 warnings <span aria-hidden="true">↑</span></button><span class="act-fnote" title="You add them anyway: the memory limits come to 10 GiB, more than the 8 GiB of the Docker host.">`)
	assert.Contains(t, body, `<div class="callout warn">You add them anyway:`)
	body = f.post("/actions/create-worker/preview", actForm("cluster", "dev", "count", "1", "overcommit", "on", "memory", "8g")).Body.String()
	assert.Contains(t, body, `name="overcommit" checked data-act-hold`)
	assert.Contains(t, body, `the new worker: 8 GiB, more than the host has</span>`)
	assert.Contains(t, body, `<span class="mono">5</span>`)
	assert.NotContains(t, body, "act-probs")
	assert.NotContains(t, body, `id="act-ok" aria-keyshortcuts="Control+Enter Meta+Enter" hx-swap-oob="true" disabled>`)

	// A field that is wrong says so in place.
	body = f.post("/actions/create-worker/preview", actForm("cluster", "dev", "count", "1", "memory", "lots")).Body.String()
	assert.Contains(t, body, `<span class="err" id="act-err-memory" hx-swap-oob="innerHTML">write a size such as 2g or 512m</span>`)
	assert.Contains(t, body, "The command takes the defaults for Memory until you fix them.")
	assert.Contains(t, body, `<div class="cmdlabel">Runs</div>`, "the command stays")
	assert.NotContains(t, body, "Fix the field above to see the command.")
}

func TestActPreview_Warnings(t *testing.T) {
	f := newActFixture(t)
	f.st.SetLock("sind", &state.LockHolder{Command: "sind power on x", Host: "h", PID: 1, Since: time.Unix(0, 0)})

	body := f.post("/actions/power/preview", actForm("cluster", "dev", "op", "cut", "nodes", "worker-0")).Body.String()
	assert.Contains(t, body, "Cannot be undone: processes on these nodes stop without warning.")
	assert.NotContains(t, body, "holds the lock", "cut takes no lock")

	body = f.post("/actions/power/preview", actForm("cluster", "dev", "op", "reboot", "nodes", "worker-0")).Body.String()
	assert.Contains(t, body, `holds the lock of realm <b class="mono">sind</b>: <span class="mono">sind power on x (pid 1 on h, since `)

	// An error says nothing of the lock.
	body = f.post("/actions/power/preview", actForm("cluster", "dev", "op", "reboot", "nodes", "worker-9")).Body.String()
	assert.NotContains(t, body, "lock")
	// A form that lacks a value does: before the user picks the action,
	// it names those that wait.
	body = f.post("/actions/power/preview", actForm("cluster", "dev", "op", "reboot")).Body.String()
	assert.Contains(t, body, "The command waits until it ends")
	body = f.post("/actions/power/preview", actForm("cluster", "dev", "nodes", "worker-0")).Body.String()
	assert.Contains(t, body, "Start, Reboot and Power-cycle wait until it ends; the other actions do not take the lock.")
	assert.Contains(t, body, `<span class="hint act-tot">Pick an action.</span>`)
	body = f.post("/actions/power/preview", actForm("cluster", "dev", "op", "cut")).Body.String()
	assert.NotContains(t, body, "lock", "cut takes no lock")
}

func TestActPreview_NewClusterFillsThePanels(t *testing.T) {
	f := newActFixture(t)

	w := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "w0-count", "2", "dir", "/scratch/hpc", "ur", "u0", "u0-name", "ada"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `<span class="act-rl"><span class="tok">sind</span> <span class="tok">create</span> <span class="tok">cluster \</span></span><span class="act-rl act-rlsub"><span class="tok">--config</span> <span class="tok">/</span><span class="tok">state/</span><span class="tok">sind/</span><span class="tok">ui/</span><span class="tok">configs/</span><span class="tok">hpc.yaml</span></span></code>`)
	assert.Contains(t, body, `<div class="act-tiles"><span class="act-tile controller" title="controller">controller</span><span class="act-tile worker" title="worker-0">worker-0</span><span class="act-tile worker" title="worker-1">worker-1</span></div>`)
	assert.Contains(t, body, `<span class="act-path">/state/sind/ui/configs/hpc.yaml</span>`)
	assert.Contains(t, body, `<span class="pill ok">Ready</span>`)
	assert.Contains(t, body, `<span class="err" id="act-err-name" hx-swap-oob="innerHTML"></span>`)
	assert.Contains(t, body, `id="act-ok" aria-keyshortcuts="Control+Enter Meta+Enter" hx-swap-oob="true"><span class="act-okl">Create hpc</span><kbd`)
	assert.Contains(t, body, "kind: Cluster\nname: hpc\n")
	assert.Contains(t, body, "- name: ada")
	assert.Contains(t, body, "sind ui saves the config (mode 0600) and keeps it, then runs the command. Copy script does the same.")
}

func TestActRoutes_WithoutTheRunner(t *testing.T) {
	f := newActFixture(t)
	f.srv.env.values.Delete(actKey{})

	assert.Equal(t, http.StatusInternalServerError, f.post("/actions/power/preview", "").Code)
	assert.Equal(t, http.StatusInternalServerError, f.postHTMX("/actions/power/run", "").Code)
	assert.Equal(t, http.StatusInternalServerError, f.post("/tasks/t/cancel", "").Code)
	assert.Equal(t, http.StatusInternalServerError, f.post("/tasks/t/retry", "").Code)
}

func TestActPreviewAndRun_UnknownAction(t *testing.T) {
	f := newActFixture(t)
	assert.Equal(t, http.StatusNotFound, f.post("/actions/nope/preview", "").Code)
	assert.Equal(t, http.StatusNotFound, f.postHTMX("/actions/nope/run", "").Code)
}

func TestActPreviewAndRun_RefuseAFormThatIsTooBig(t *testing.T) {
	f := newActFixture(t)
	long := "cluster=" + strings.Repeat("x", actMaxValue+1)
	many := strings.Repeat("nodes=a&", actMaxValues+1)
	for _, form := range []string{long, many} {
		assert.Equal(t, http.StatusBadRequest, f.post("/actions/power/preview", form).Code)
		assert.Equal(t, http.StatusBadRequest, f.postHTMX("/actions/power/run", form).Code)
	}
	assert.Empty(t, f.starter.specs)
	assert.NoError(t, actCheckForm(url.Values{"nodes": {strings.Repeat("x", actMaxValue)}}))
	assert.NoError(t, actCheckForm(url.Values{"slurm-main": {strings.Repeat("x", actMaxText)}}), "a field of text may be longer")
	assert.ErrorContains(t, actCheckForm(url.Values{"slurm-main": {strings.Repeat("x", actMaxText+1)}}), "longer than 65536 bytes")
	assert.NoError(t, actCheckForm(url.Values{"yaml": {strings.Repeat("x", actMaxText)}}))
}

func TestActRun_StartsTheCommand(t *testing.T) {
	f := newActFixture(t)
	form := actForm("realm", "sind", "cluster", "dev", "op", "reboot", "nodes", "worker-0")
	form += "&shown=" + f.shown(t, "power", form)

	w := f.postHTMX("/actions/power/run", form)

	// The dialog closes, and the strip of the task dock shows the run.
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Empty(t, w.Header().Get("HX-Redirect"))
	assert.Empty(t, w.Header().Get("HX-Retarget"))
	assert.JSONEq(t, `{"act-started": {"url": "/tasks/task1", "follow": false}}`, w.Header().Get("HX-Trigger"))
	assert.Empty(t, w.Body.String())
	require.Len(t, f.starter.specs, 1)
	assert.Equal(t, run.Spec{
		Action: "power", Argv: []string{"sind", "power", "reboot", "worker-0.dev"},
		Display: "sind power reboot worker-0.dev", Realm: "sind", Cluster: "dev",
		Form: map[string][]string{"realm": {"sind"}, "cluster": {"dev"}, "op": {"reboot"}, "nodes": {"worker-0"}},
	}, f.starter.specs[0], "the task keeps the form, without the fingerprint")
	assert.Empty(t, f.saved)
}

func TestActRun_WithoutScriptRedirectsToTheTask(t *testing.T) {
	f := newActFixture(t)
	form := actForm("realm", "sind", "cluster", "dev", "op", "reboot", "nodes", "worker-0")
	form += "&shown=" + f.shown(t, "power", form)

	w := f.post("/actions/power/run", form)

	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.Equal(t, "/tasks/task1", w.Header().Get("Location"))
	assert.Empty(t, w.Header().Get("HX-Redirect"))
	require.Len(t, f.starter.specs, 1)
}

func TestActRun_SavesTheConfigFirst(t *testing.T) {
	f := newActFixture(t)
	form := actForm("realm", "ci", "name", "hpc", "w0-count", "1", "dir", "/scratch/hpc", "wait", "2m")
	form += "&shown=" + f.shown(t, "create-cluster", form)

	w := f.postHTMX("/actions/create-cluster/run", form)

	// A new cluster follows its run, and its draft goes.
	require.Equal(t, "/tasks/task1", w.Header().Get("HX-Redirect"), w.Body.String())
	assert.JSONEq(t, `{"act-started": {"url": "/tasks/task1", "follow": true}}`, w.Header().Get("HX-Trigger"))
	assert.Equal(t, []string{"/state/sind/ui/configs/hpc.yaml"}, slices.Collect(maps.Keys(f.saved)))
	assert.Contains(t, f.saved["/state/sind/ui/configs/hpc.yaml"], "name: hpc\nrealm: ci\n")
	require.Len(t, f.starter.specs, 1)
	assert.Equal(t, "sind --realm ci create cluster --config /state/sind/ui/configs/hpc.yaml --wait 2m", f.starter.specs[0].Display)
	assert.Equal(t, "ci", f.starter.specs[0].Realm)
	assert.Equal(t, "hpc", f.starter.specs[0].Cluster)
	assert.Equal(t, "/state/sind/ui/configs/hpc.yaml", f.starter.specs[0].ConfigPath)
	assert.Equal(t, f.saved["/state/sind/ui/configs/hpc.yaml"], f.starter.specs[0].Config, "the task keeps the config it ran with")
}

func TestActRun_CreatesTheDataDirectoryFirst(t *testing.T) {
	f := newActFixture(t)
	form := actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/missing", "mkdir", "on")
	form += "&shown=" + f.shown(t, "create-cluster", form)

	w := f.postHTMX("/actions/create-cluster/run", form)

	require.Equal(t, "/tasks/task1", w.Header().Get("HX-Redirect"), w.Body.String())
	assert.Equal(t, []string{"/scratch/missing"}, f.made)

	// A directory that cannot be made stops the run before the config.
	f.state.mkdirAll = func(string) error { return errors.New("read-only file system") }
	f.saved, f.starter.specs = map[string]string{}, nil
	w = f.postHTMX("/actions/create-cluster/run", form)
	assert.Contains(t, w.Body.String(), "creating the directory /scratch/missing: read-only file system")
	assert.Empty(t, f.saved)
	assert.Empty(t, f.starter.specs)
}

func TestActPreview_NewClusterPullsAsTheImageSays(t *testing.T) {
	f := newActFixture(t)
	form := actForm("name", "hpc", "pull_auto", "1")

	body := f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<span class="hint" id="act-pull-hint" data-act-pull="on" hx-swap-oob="true">On, as the tag latest can move to a newer image. Create gets the newest image first.</span>`)
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">Pull, wait 5m (default)</span>`)

	body = f.post("/actions/create-cluster/preview", form+"&pull=on&image=ghcr.io/gsi-hpc/sind-node:26.05.4&wait=0").Body.String()
	assert.Contains(t, body, `data-act-pull="off" hx-swap-oob="true">Off, as the tag 26.05.4 is fixed.</span>`)
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">No pull, no wait limit</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "pull", "on", "wait", "other", "wait_other", "20m")).Body.String()
	assert.Contains(t, body, `data-act-pull="on" hx-swap-oob="true">Gets the newest image first, even if Docker has one.</span>`)
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">Pull, wait 20m</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "wait", "2m")).Body.String()
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">No pull, wait 2m</span>`)
	assert.Contains(t, body, `data-pg-copy-text="cfg=`, "Copy gives the script")
}

func TestActDialog_NewClusterNodes(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/clusters/new").Body.String()
	assert.Contains(t, body, `<input type="hidden" name="wg" value="w0">`)
	assert.Contains(t, body, `<span id="act-range-w0">worker-0 … worker-1</span>`)
	assert.Contains(t, body, `<span id="act-chip-controller">defaults</span>`)
	assert.Contains(t, body, `<div class="act-ned" id="act-ed-db" hidden>`)
	assert.Contains(t, body, `id="act-db-cpus" name="db-cpus" value="" min="1" placeholder="1"`)
	assert.Contains(t, body, `name="size" value="s" checked id="act-size"`)
	assert.Contains(t, body, `<template id="act-wg-tpl"><div class="act-nr act-wgrow" id="act-wg-__WG__">`)
	assert.Contains(t, body, `id="act-controller-unmanaged"`)
	assert.NotContains(t, body, `id="act-submitter-unmanaged"`, "the submitter is always managed")
	assert.Contains(t, body, "Makes the cluster unmanaged")
	assert.Contains(t, body, "The workers start without slurmd.")
	assert.Contains(t, body, "The node starts without its daemons.")

	body = f.get("/clusters/new?workers=abc&cpus=3").Body.String()
	assert.Contains(t, body, `<span id="act-range-w0">worker-0</span>`, "a wrong count is one worker")
	assert.Contains(t, body, `name="size" value="x" checked`, "a link with a size gives custom values")
	assert.Contains(t, body, `placeholder="3" data-act-ph="cpus"`)

	body = f.get("/clusters/new?workers=5").Body.String()
	assert.Contains(t, body, `id="act-w0-count" name="w0-count" value="5"`)
}

func TestActPreview_NewClusterNodes(t *testing.T) {
	f := newActFixture(t)
	form := actForm("name", "hpc", "db", "on", "size", "l", "image", "ghcr.io/x/y:1",
		"db-cpus", "1", "db-memory", "2g", "db-tmp", "1g", "db-image", "ghcr.io/x/y:2", "db-unmanaged", "on", "db-capAdd", "SYS_ADMIN",
		"api-devices", "/dev/fuse", "w0-count", "2", "w1-count", "3", "w1-cpus", "8")
	form += "&wg=w0&wg=w1&wg=bad+key"

	body := f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<span id="act-chip-db" class="own" hx-swap-oob="true">1 CPU · 2g · /tmp 1g · own image · unmanaged · more</span>`)
	assert.Contains(t, body, `<span id="act-chip-api" class="own" hx-swap-oob="true">more</span>`)
	assert.Contains(t, body, `<span id="act-chip-w1" class="own" hx-swap-oob="true">8 CPUs</span>`)
	assert.Contains(t, body, `<span id="act-chip-controller" hx-swap-oob="true">defaults</span>`)
	assert.Contains(t, body, `<span id="act-range-w0" hx-swap-oob="innerHTML">worker-0 … worker-1</span>`)
	assert.Contains(t, body, `<span id="act-range-w1" hx-swap-oob="innerHTML">worker-2 … worker-4</span>`)
	assert.NotContains(t, body, "act-chip-bad", "a key that the dialog did not make gets no swap")
	assert.Contains(t, body, `<span id="act-eff" hidden hx-swap-oob="true" data-image="ghcr.io/x/y:1" data-cpus="4" data-memory="4g" data-tmp="1g"></span>`)
	assert.Contains(t, body, `id="act-err-w1-cpus" hx-swap-oob="innerHTML"></span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "size", "x", "cpus", "3", "w0-count", "1")).Body.String()
	assert.Contains(t, body, `data-image="ghcr.io/gsi-hpc/sind-node:latest" data-cpus="3" data-memory="512m" data-tmp="256m"`)
	assert.Contains(t, body, `id="act-range-w0" hx-swap-oob="innerHTML">worker-0</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("realm", "Bad Realm")).Body.String()
	assert.Contains(t, body, `id="act-range-w0" hx-swap-oob="innerHTML">worker-0</span>`, "a wrong realm is a problem; the plan shows")

	f.state.configDir = ""
	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `id="act-range-w0" hx-swap-oob="innerHTML"></span>`, "no plan, no range")
}

func TestActPreview_NewClusterHostBudget(t *testing.T) {
	f := newActFixture(t)
	form := actForm("name", "hpc", "w0-count", "3")

	assert.NotContains(t, f.post("/actions/create-cluster/preview", form).Body.String(), "act-host", "a host of unknown size")

	f.st.SetDocker(store.DockerState{Checked: true, MemTotal: 2 << 30, NCPU: 2})
	body := f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<div class="act-host"><div class="cmdlabel">This host</div>`)
	assert.Contains(t, body, `<div class="act-br high"><span class="act-bk" aria-hidden="true">Memory</span>`)
	assert.Contains(t, body, `<span class="act-bv" aria-hidden="true">&#43;2 GiB of 2 GiB</span>`)
	assert.Contains(t, body, `<span class="act-bv" aria-hidden="true">&#43;4 of 2</span>`, "the CPUs, which the nodes share")
	assert.NotContains(t, body, "act-overcommit")

	form = actForm("name", "hpc", "w0-count", "4", "dir", "/scratch/hpc")
	body = f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<a href="#act-overcommit" data-act-jump="overcommit">Memory</a>: with the nodes that run now, the memory limits come to 2.5 GiB, more than the 2 GiB of the Docker host; give the nodes less memory, or tick Create anyway`)
	assert.Contains(t, body, `<div class="act-br over"><span class="act-bk" aria-hidden="true">Memory</span><svg class="act-hbar" viewBox="0 0 1000 10" preserveAspectRatio="none" aria-hidden="true"><rect class="now" width="0" height="10"/><rect class="add" x="0" width="1000" height="10"/><rect class="host" x="800" width="6" height="10"/></svg>`)
	assert.Contains(t, body, `<input type="checkbox" id="act-overcommit" name="overcommit" data-act-hold aria-describedby="act-hint-overcommit"><span class="act-lab"><b>Create anyway</b>`)
	assert.Contains(t, body, `<span class="pill bad">Not ready · 1 problem</span>`)

	form = actForm("name", "hpc", "w0-count", "4", "overcommit", "on", "dir", "/scratch/hpc")
	body = f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<input type="checkbox" id="act-overcommit" name="overcommit" checked data-act-hold aria-describedby="act-hint-overcommit">`)
	assert.Contains(t, body, `<span class="pill warn">Ready · 1 warning</span>`)
	assert.Contains(t, body, `<span class="act-status" id="act-status" hx-swap-oob="innerHTML"><span class="vh" data-act-say="Ready, with 1 warning. Create hpc runs the command." data-act-key="ready 1"></span>`)

	form = actForm("name", "hpc", "w0-count", "4", "overcommit", "on", "slurm-main", "ClusterName=prod", "dir", "/scratch/hpc")
	body = f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<span class="pill warn">Ready · 2 warnings</span>`)

	// The nodes reach a warning of the Doctor: the plan says so, with the
	// fix, and counts it.
	f.st.SetDoctor(&store.DoctorReport{Checks: []store.DoctorCheck{{Name: "inotify", Status: "warning", Detail: "max_user_instances 128", Remediation: "sudo sysctl fs.inotify.max_user_instances=1024"}}})
	f.st.SetDocker(store.DockerState{Checked: true, MemTotal: 64 << 30, NCPU: 16})
	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "w0-count", "5", "dir", "/scratch/hpc")).Body.String()
	assert.Contains(t, body, `<b>The host can run out of inotify instances.</b> With hpc, this host runs 10 nodes. The Doctor says that 10 and more can fail to start.`)
	assert.Contains(t, body, `<div class="act-br over"><span class="act-bk" aria-hidden="true">inotify</span>`)
	assert.Contains(t, body, `<span class="act-bv" aria-hidden="true">10 nodes · max 9</span>`)
	assert.Contains(t, body, `<code>sudo sysctl fs.inotify.max_user_instances=1024</code>`)
	assert.Contains(t, body, `<span class="pill warn">Ready · 1 warning</span>`)
	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/hpc")).Body.String()
	assert.Contains(t, body, `<div class="act-br"><span class="act-bk" aria-hidden="true">inotify</span>`, "the limit stays in view")
	assert.NotContains(t, body, "act-hw")
}

func TestActPreview_NewClusterSumsUpTheSections(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "dir", "~/sind/missing", "mkdir", "on", "mount", "/work")).Body.String()
	assert.Contains(t, body, `<div class="act-fmap" id="act-fmap" hx-swap-oob="innerHTML"><div class="pg-files"><div class="pg-fmap"><span class="pg-fside"><small>On this host</small><b class="mono"><span class="tok">~/</span><span class="tok">sind/</span><span class="tok">missing</span></b></span><span class="pg-farrow" aria-hidden="true">→</span><span class="pg-fside"><small>In every node</small><b class="mono"><span class="tok">/</span><span class="tok">work</span></b></span></div>`)
	assert.Contains(t, body, `<code><span class="tok">cp</span> <span class="tok">job.sh</span> <span class="tok">~/</span><span class="tok">sind/</span><span class="tok">missing/</span></code>`)
	assert.Contains(t, body, `<span class="act-sb" id="act-bad-data" hx-swap-oob="innerHTML"></span>`)
	assert.Contains(t, body, `, creates <span class="mono"><span class="tok">~/</span><span class="tok">sind/</span><span class="tok">missing</span></span>, which stays if the create fails, then runs the command.`, "below home as ~/")

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "dtype", "volume", "mount", "work,x")).Body.String()
	assert.Contains(t, body, `<small>Docker volume</small><b class="mono"><span class="tok">sind-hpc-data</span></b>`)
	assert.Contains(t, body, `<code><span class="tok">docker</span> <span class="tok">cp</span> <span class="tok">job.sh</span> <span class="tok">sind-hpc-controller:work,x/</span></code>`)
	assert.Contains(t, body, `id="act-bad-data" hx-swap-oob="innerHTML">1 problem</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "dir", "rel", "dtype", "nfs")).Body.String()
	assert.Contains(t, body, `<small>On this host</small><b class="mono"><span class="tok">rel</span></b>`)
	assert.Contains(t, body, `id="act-bad-data" hx-swap-oob="innerHTML">2 problems</span>`)
}

func TestActRun_RefusesTheFormThatDoesNotBuild(t *testing.T) {
	f := newActFixture(t)

	w := f.postHTMX("/actions/power/run", actForm("cluster", "dev", "op", "on")+"&shown=x")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "#act-preview", w.Header().Get("HX-Retarget"))
	assert.Equal(t, "innerHTML", w.Header().Get("HX-Reswap"))
	assert.Contains(t, w.Body.String(), `<div class="callout bad">Pick at least one node of dev.</div>`)
	assert.NotContains(t, w.Body.String(), "act-missing")
	assert.Contains(t, w.Body.String(), `disabled><span class="act-okl">Start nodes</span><kbd`)
	assert.Empty(t, f.starter.specs)
}

func TestActRun_RefusesWithoutTheTypedName(t *testing.T) {
	f := newActFixture(t)
	form := actForm("cluster", "dev")
	shown := f.shown(t, "delete-cluster", form)

	for _, typed := range []string{"", "de", "default"} {
		w := f.postHTMX("/actions/delete-cluster/run", form+"&shown="+shown+"&confirm="+typed)
		assert.Contains(t, w.Body.String(), "Type dev to confirm.", typed)
		assert.Equal(t, "#act-preview", w.Header().Get("HX-Retarget"))
	}
	assert.Empty(t, f.starter.specs)

	w := f.postHTMX("/actions/delete-cluster/run", form+"&shown="+shown+"&confirm=dev")
	assert.Contains(t, w.Header().Get("HX-Trigger"), `"/tasks/task1"`)
	assert.Equal(t, "sind delete cluster dev", f.starter.specs[0].Display)
}

func TestActRun_RefusesACommandThatChanged(t *testing.T) {
	f := newActFixture(t)
	form := actForm("cluster", "dev", "op", "reboot", "nodes", "worker-0")
	shown := f.shown(t, "power", form)

	// The browser shows one command and posts the fields of another.
	w := f.postHTMX("/actions/power/run", actForm("cluster", "dev", "op", "cut", "nodes", "worker-0", "shown", shown))

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "The command changed while you looked. Check it, then press the button again.")
	assert.Contains(t, body, "sind power cut worker-0.dev")
	assert.NotContains(t, body, "disabled", "the button works once the new command was seen")
	assert.Empty(t, f.starter.specs)

	// Without any preview, nothing runs either.
	w = f.postHTMX("/actions/power/run", form)
	assert.Contains(t, w.Body.String(), "The command changed while you looked")
	assert.Empty(t, f.starter.specs)
}

func TestActRun_ShownCoversTheConfig(t *testing.T) {
	f := newActFixture(t)
	form := actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/hpc", "ur", "u0", "u0-name", "ada")
	shown := f.shown(t, "create-cluster", form)

	// The command line is the same; the config is not.
	w := f.postHTMX("/actions/create-cluster/run", actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/hpc", "ur", "u0", "u0-name", "eve", "shown", shown))

	assert.Contains(t, w.Body.String(), "The command changed while you looked")
	assert.Empty(t, f.saved)
	assert.Empty(t, f.starter.specs)
}

func TestActRun_StartFailures(t *testing.T) {
	f := newActFixture(t)
	form := actForm("cluster", "dev", "op", "reboot", "nodes", "worker-0")
	form += "&shown=" + f.shown(t, "power", form)
	f.starter.startErr = errors.New("not a sind command")

	w := f.postHTMX("/actions/power/run", form)

	assert.Empty(t, w.Header().Get("HX-Redirect"))
	assert.Equal(t, "#act-preview", w.Header().Get("HX-Retarget"))
	assert.Contains(t, w.Body.String(), "not a sind command")

	createForm := actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/hpc")
	createForm += "&shown=" + f.shown(t, "create-cluster", createForm)
	f.state.writeFile = func(string, []byte) error { return errors.New("disk full") }
	f.starter.specs = nil

	w = f.postHTMX("/actions/create-cluster/run", createForm)

	assert.Contains(t, w.Body.String(), "saving the config: disk full")
	assert.Empty(t, f.starter.specs)
}

func TestActCancel(t *testing.T) {
	f := newActFixture(t)
	f.st.AddTask(store.Task{ID: "task1", Status: store.TaskRunning})

	w := f.post("/tasks/task1/cancel", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, []string{"task1"}, f.starter.canceled)

	assert.Equal(t, http.StatusNotFound, f.post("/tasks/other/cancel", "").Code)

	f.starter.cancelErr = fmt.Errorf("task1: %w", run.ErrNotRunning)
	w = f.post("/tasks/task1/cancel", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "not running")

	f.starter.cancelErr = errors.New("operation not permitted")
	assert.Equal(t, http.StatusInternalServerError, f.post("/tasks/task1/cancel", "").Code)
}

func TestActWrites_NeedTheSession(t *testing.T) {
	f := newActFixture(t)
	for _, target := range []string{"/actions/power/run", "/actions/power/preview", "/tasks/t/cancel"} {
		r := request("POST", target)
		r.AddCookie(f.cookie)
		r.Header.Set("Origin", "http://127.0.0.1:7878")
		w := f.do(r)
		assert.Equal(t, http.StatusForbidden, w.Code, "no CSRF header: "+target)
	}
	assert.Empty(t, f.starter.specs)
}

func TestActLocks(t *testing.T) {
	power, _ := action.Lookup("power")
	for op, want := range map[string]bool{"on": true, "start": true, "reboot": true, "cycle": true, "shutdown": false, "cut": false, "freeze": false, "unfreeze": false, "": false} {
		assert.Equal(t, want, actLocks(power, url.Values{"op": {op}}), op)
	}
	del, _ := action.Lookup("delete-cluster")
	assert.True(t, actLocks(del, url.Values{}))
}

func TestActWaits_AreTheOnesTheActionTakes(t *testing.T) {
	var values []string
	for _, w := range actWaits {
		values = append(values, w.Value)
		assert.NotEmpty(t, w.Label)
	}
	assert.Equal(t, append(slices.Clone(action.Waits), "other"), values)
	assert.Contains(t, action.Waits, action.DefaultWait)
}

func TestActSpecs_CoverEveryAction(t *testing.T) {
	for _, id := range action.IDs() {
		spec, ok := actSpecs[id]
		require.True(t, ok, id)
		assert.NotEmpty(t, spec.OK, id)
		assert.NotNil(t, spec.Title, id)
		assert.NotNil(t, spec.Lead, id)
	}
	assert.Len(t, actSpecs, len(action.IDs()))
}

func TestActService_Setup(t *testing.T) {
	cfg, _ := testConfig(t)
	env := cfg.Env
	actSeams(t)
	actExecutable = func() (string, error) { return "/usr/bin/sind", nil }
	actStateDir = func() (string, error) { return "/state/sind", nil }
	actActivityLog = func() (string, error) { return "/state/sind/activity.jsonl", nil }
	actHome = func() (string, error) { return "/home/ada", nil }
	actEnviron = func() []string { return []string{"A=1"} }

	require.NoError(t, actService().Setup(env))

	got, ok := env.Get(actKey{}).(*actState)
	require.True(t, ok)
	assert.Equal(t, "/home/ada", got.home)
	assert.Equal(t, "/state/sind/ui/configs", got.configDir)
	assert.NotNil(t, got.starter)
	assert.NotNil(t, got.writeFile)
}

func TestActService_SetupErrors(t *testing.T) {
	cfg, _ := testConfig(t)
	actSeams(t)

	actExecutable = func() (string, error) { return "", errors.New("no /proc") }
	assert.EqualError(t, actService().Setup(cfg.Env), "finding the sind executable: no /proc")

	actExecutable = func() (string, error) { return "/usr/bin/sind", nil }
	actStateDir = func() (string, error) { return "", errors.New("no home") }
	assert.EqualError(t, actService().Setup(cfg.Env), "finding the state directory: no home")
	assert.Nil(t, cfg.Env.Get(actKey{}))
}

func TestActService_SetupSurvivesWhatSindDefaults(t *testing.T) {
	cfg, _ := testConfig(t)
	actSeams(t)
	actExecutable = func() (string, error) { return "/usr/bin/sind", nil }
	actStateDir = func() (string, error) { return "/state/sind", nil }
	actActivityLog = func() (string, error) { return "", errors.New("read-only home") }
	actHome = func() (string, error) { return "", errors.New("no home") }

	require.NoError(t, actService().Setup(cfg.Env))

	got := cfg.Env.Get(actKey{}).(*actState)
	assert.Empty(t, got.home)
}

// actSeams puts the seams of Setup back when the test ends.
func actSeams(t *testing.T) {
	e, d, l, h, v := actExecutable, actStateDir, actActivityLog, actHome, actEnviron
	t.Cleanup(func() { actExecutable, actStateDir, actActivityLog, actHome, actEnviron = e, d, l, h, v })
}

func TestActService_RefreshesTheCollectorAfterATask(t *testing.T) {
	cfg, st := testConfig(t)
	actSeams(t)
	actExecutable = func() (string, error) { return "/bin/sh", nil }
	actStateDir = func() (string, error) { return "/state/sind", nil }
	actActivityLog = func() (string, error) { return "/state/sind/activity.jsonl", nil }
	actHome = func() (string, error) { return "/home/ada", nil }
	actEnviron = func() []string { return []string{"PATH=/bin"} }
	refreshed := make(chan string, 2)
	cfg.Env.Put("collect.refresh", func(realm string) { refreshed <- realm })
	cfg.Env.Exec = &mock.Executor{OnCall: func([]string, string) mock.Result { return mock.Result{} }}
	require.NoError(t, actService().Setup(cfg.Env))

	id, err := cfg.Env.Get(actKey{}).(*actState).starter.Start(run.Spec{Action: "power", Argv: []string{"sind", "power", "on", "x"}, Realm: "ci"})

	require.NoError(t, err)
	select {
	case realm := <-refreshed:
		assert.Equal(t, "ci", realm)
	case <-time.After(5 * time.Second):
		t.Fatal("the collector was not asked to read the realm again")
	}
	assert.Eventually(t, func() bool { task, _ := st.Snapshot().Task(id); return task.Done() }, 5*time.Second, time.Millisecond)

	// A collector that is not there is no reason to fail.
	cfg.Env.Put("collect.refresh", nil)
	id, err = cfg.Env.Get(actKey{}).(*actState).starter.Start(run.Spec{Argv: []string{"sind", "x"}})
	require.NoError(t, err)
	assert.Eventually(t, func() bool { task, _ := st.Snapshot().Task(id); return task.Done() }, 5*time.Second, time.Millisecond)
}

func TestActService_RunShutsTheRunnerDownWithTheServer(t *testing.T) {
	cfg, _ := testConfig(t)
	starter := &actFakeStarter{}
	cfg.Env.Put(actKey{}, &actState{starter: starter})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { actService().Run(ctx, cfg.Env); close(done) }()

	cancel()
	<-done

	assert.Equal(t, []time.Duration{actGrace}, starter.shutdown)

	// Without a runner (Setup failed or did not run) there is nothing to stop.
	cfg.Env.Put(actKey{}, nil)
	actService().Run(ctx, cfg.Env)
	assert.Len(t, starter.shutdown, 1)
}

func TestActActivityLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	path, err := activityLogForChildren()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "sind", "activity.jsonl"), path)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// A state directory that cannot be made still gives the path.
	blocker := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	t.Setenv("XDG_STATE_HOME", blocker)
	path, err = activityLogForChildren()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(blocker, "sind", "activity.jsonl"), path)
}

func TestActMkdirAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sind", "hpc")

	require.NoError(t, actMkdirAll(path))
	require.NoError(t, actMkdirAll(path), "a directory that exists is fine")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestActSaveFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui", "configs", "hpc.yaml")

	require.NoError(t, actSaveFile(path, []byte("kind: Cluster\n")))
	require.NoError(t, actSaveFile(path, []byte("kind: Cluster\nname: x\n")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "kind: Cluster\nname: x\n", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	// A file that exists keeps its mode.
	require.NoError(t, os.Chmod(path, 0o640))
	require.NoError(t, actSaveFile(path, []byte("kind: Cluster\n")))
	info, err = os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	// The directory cannot be made below a file.
	assert.Error(t, actSaveFile(filepath.Join(path, "x", "y.yaml"), nil))
}

func TestActDialog_RendersAsAFragmentOfTheLayerOnly(t *testing.T) {
	f := newActFixture(t)
	r := request("GET", "/dialogs/connect")
	r.AddCookie(f.cookie)
	r.Header.Set("HX-Request", "true")
	w := f.do(r)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(w.Body.String()), `<div class="scrim" id="act-dialog"`))
	assert.NotContains(t, w.Body.String(), "<html")
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
}

func TestActDialog_OpenedByItsURLIsAPage(t *testing.T) {
	f := newActFixture(t)
	body := f.get("/dialogs/connect").Body.String()
	assert.Contains(t, body, "<title>Connect a container to realm sind · sind ui</title>")
	assert.Contains(t, body, `<main class="main" id="main" tabindex="-1">`+"\n"+`<div class="scrim" id="act-dialog"`)
}

func TestActPreview_NewClusterShowsThePlanWithProblems(t *testing.T) {
	f := newActFixture(t)

	w := f.post("/actions/create-cluster/preview", actForm("name", "dev", "w0-count", "20", "cpus", "0", "dir", "/scratch/dev"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `<span class="pill bad">Not ready · 2 problems</span>`)
	assert.Contains(t, body, `<a href="#act-name" data-act-jump="name">Name</a>: realm sind has a cluster dev already`)
	assert.Contains(t, body, `<span class="err" id="act-err-name" hx-swap-oob="innerHTML">realm sind has a cluster dev already; pick another name, or delete that cluster first</span>`)
	assert.Contains(t, body, `id="act-err-cpus" hx-swap-oob="innerHTML">`)
	assert.Contains(t, body, "The plan takes the defaults for CPUs per node until you fix them.")
	assert.Contains(t, body, `<span class="act-tile more">16 more</span>`, "21 nodes show as 5 tiles and the rest")
	assert.Contains(t, body, "kind: Cluster\nname: dev\n", "the plan still shows the config")
	assert.Contains(t, body, `disabled><span class="act-okl">Create dev</span><kbd`)
}

func TestActPreview_NewClusterThatCannotBuild(t *testing.T) {
	f := newActFixture(t)

	f.state.configDir = ""
	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()

	assert.Contains(t, body, `<span class="pill bad">Not ready · 1 problem</span>`)
	assert.Contains(t, body, `<div class="callout bad" id="act-probs" tabindex="-1">sind ui has no state directory`)
	assert.NotContains(t, body, "act-yaml")
}

func TestActRun_RefusesANewClusterWithProblems(t *testing.T) {
	f := newActFixture(t)
	form := actForm("name", "dev", "w0-count", "1", "dir", "/scratch/dev")
	form += "&shown=" + f.shown(t, "create-cluster", form)

	w := f.postHTMX("/actions/create-cluster/run", form)

	assert.Empty(t, w.Header().Get("HX-Redirect"))
	assert.Equal(t, "#act-preview", w.Header().Get("HX-Retarget"))
	assert.Contains(t, w.Body.String(), "realm sind has a cluster dev already")
	assert.Empty(t, f.saved)
	assert.Empty(t, f.starter.specs)
}

func TestActDialog_NewClusterUsers(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/clusters/new").Body.String()
	assert.Contains(t, body, `<details class="act-sec act-noyaml" id="act-sec-users"><summary>`, "no rows: the section is closed")
	assert.Contains(t, body, `<template id="act-tpl-user"><div class="act-row" id="act-row-__ROW__" data-act-row><input type="hidden" name="ur" value="__ROW__">`)
	assert.Contains(t, body, `<template id="act-tpl-limit"><div class="act-rowf act-lim" data-act-row>
  <input type="text" name="__ROW__-lk" value=""`)
	assert.Contains(t, body, `<option value="MaxJobs"></option>`)

	body = f.get("/clusters/new?users=ada,bob").Body.String()
	assert.Contains(t, body, `<details class="act-sec act-noyaml" id="act-sec-users" open>`)
	assert.Contains(t, body, `id="act-u0-name" name="u0-name" value="ada"`)
	assert.Contains(t, body, `id="act-u1-name" name="u1-name" value="bob"`)
	// Each remove button is named after the name in its row.
	assert.Contains(t, body, `id="act-u1-del" data-act-rowdel aria-label="Remove the user" aria-labelledby="act-u1-del act-u1-name"`)

	body = f.get("/clusters/new?" + actForm("ar", "a0", "a0-name", "physics", "a0-lk", "MaxJobs", "a0-lv", "4", "a0-lk", "GrpTRES",
		"ur", "u0", "u0-admin", "operator", "gr", "g0", "gr", "api", "g0-name", "hpc", "evil", "x")).Body.String()
	assert.Contains(t, body, `name="a0-lk" value="MaxJobs"`)
	assert.Contains(t, body, `aria-label="Value of MaxJobs"`, "the value is named after its limit")
	assert.Contains(t, body, `data-act-rowdel aria-label="Remove MaxJobs" data-act-limname="Remove ">`)
	assert.Contains(t, body, `data-act-rowdel aria-label="Remove the limit" data-act-limname="Remove ">`, "the blank row of the template")
	assert.Contains(t, body, `name="a0-lv" value="4"`)
	assert.Contains(t, body, `name="a0-lk" value="GrpTRES"`, "a limit without its value")
	assert.Contains(t, body, `id="act-g0-name" name="g0-name" value="hpc"`)
	assert.Contains(t, body, `aria-label="Remove the group" aria-labelledby="act-g0-del act-g0-name"`)
	assert.Contains(t, body, `aria-label="Remove the account" aria-labelledby="act-a0-del act-a0-name"`)
	assert.NotContains(t, body, `id="act-row-api"`, "a key that the dialog does not make")
	assert.Contains(t, body, `<option value="operator" selected>Operator</option>`)
	assert.Contains(t, body, `<details class="act-more" open><summary>More: groups, coordinator, admin level</summary>`)
}

func TestActPreview_NewClusterUsers(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "db", "on", "ur", "u0", "u0-name", "ada", "ur", "u1", "u1-name", "bob",
		"gr", "g0", "g0-name", "hpc", "ar", "a0", "a0-name", "physics", "ar", "a1")).Body.String()
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">2 users, 1 group, 1 account · local · no monitoring</span>`)
	assert.Contains(t, body, `<datalist id="act-dl-groups" hx-swap-oob="true"><option value="hpc"></option></datalist>`)
	assert.Contains(t, body, `<datalist id="act-dl-accounts" hx-swap-oob="true"><option value="physics"></option></datalist>`)
	assert.Contains(t, body, `id="act-err-u1-name" hx-swap-oob="innerHTML"></span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "ur", "u0", "u0-name", "Ada")).Body.String()
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">1 user · local · no monitoring</span>`)
	assert.Contains(t, body, `id="act-bad-users" hx-swap-oob="innerHTML">1 problem</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">No users · local · no monitoring</span>`)
	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "identity", "clientIds", "ctlusers", "on", "monitoring", "on")).Body.String()
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">No users · clientIds, users on the controllers too · monitoring</span>`)
}

func TestActDialog_NewClusterSlurmAndIdentity(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/clusters/new").Body.String()
	assert.Contains(t, body, `<details class="act-sec act-noyaml" id="act-sec-users"><summary><span class="act-st">Users, identity, monitoring</span>`)
	assert.Contains(t, body, `<input type="radio" name="identity" value="local" checked id="act-identity">`)
	assert.Contains(t, body, `<details class="act-sec act-slurm act-noyaml" id="act-sec-slurm"><summary>`)
	assert.Contains(t, body, `<input type="radio" name="stab" value="main" checked>`)
	assert.Contains(t, body, `<textarea id="act-slurm-gres" name="slurm-gres" rows="6" placeholder="such as Name=gpu File=/dev/null Count=1"`)
	assert.Contains(t, body, `id="act-cvmfs" name="cvmfs"`)
	assert.Contains(t, body, `<div class="act-fed" id="act-fed" hidden>`, "the data storage is folded")
	assert.True(t, (&actDialog{Form: url.Values{"cvmfs": {"on"}}}).DataOpen(), "CVMFS, which the map does not show, opens it")
	assert.Contains(t, f.get("/clusters/new?mount=/work").Body.String(), `<div class="act-fed" id="act-fed">`)
	assert.Contains(t, f.get("/clusters/new?mount=/work").Body.String(), `aria-expanded="true" aria-controls="act-fed">Change…</button>`)
}

func TestActPreview_NewClusterSlurmAndIdentity(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "slurm-main", "MaxJobCount=100", "slurm-gres", " ",
		"identity", "clientIds", "ctlusers", "on", "cvmfs", "on", "dtype", "volume")).Body.String()
	assert.Contains(t, body, `id="act-sum-slurm" hx-swap-oob="innerHTML">Adds to slurm.conf</span>`)
	assert.Contains(t, body, `<span class="act-dot" id="act-dot-main" hx-swap-oob="true"> •</span>`)
	assert.Contains(t, body, `<span class="act-dot" id="act-dot-gres" hx-swap-oob="true"></span>`)
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">No users · clientIds, users on the controllers too · no monitoring</span>`)
	assert.Contains(t, body, `<small>Docker volume</small><b class="mono"><span class="tok">sind-hpc-data</span></b>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "identity", "nssSlurm", "ctlusers", "on")).Body.String()
	assert.Contains(t, body, `id="act-sum-slurm" hx-swap-oob="innerHTML">No additions</span>`)
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">No users · nssSlurm · no monitoring</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">No users · local · no monitoring</span>`)
}

func TestActPreview_NewClusterYAML(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/clusters/new").Body.String()
	assert.Contains(t, body, `<textarea id="act-yaml" name="yaml" rows="26" placeholder="kind: Cluster&#10;name: hpc`)
	assert.Contains(t, body, `aria-describedby="act-err-yaml act-hint-yaml" disabled></textarea>`)
	assert.Contains(t, body, `<input type="hidden" id="act-yaml-on" name="yaml_on" value="1" disabled>`)
	assert.Contains(t, body, `<button type="button" class="btn sm" data-act-yaml-start>Start from YAML…</button>`)
	assert.Contains(t, body, `<button type="button" class="btn sm" data-act-yaml-open aria-describedby="act-hint-yaml">Open a file…</button>`)
	assert.Contains(t, body, `<input type="file" id="act-yaml-file" accept=".yaml,.yml,application/yaml,text/yaml,text/plain" hidden>`)
	assert.Less(t, strings.Index(body, `id="act-name"`), strings.Index(body, `id="act-yamled"`), "Name comes first, and stays beside the YAML")

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `<button type="button" class="btn sm" data-act-fold="act-yamlv" aria-expanded="false" aria-controls="act-yamlv">Show YAML</button>`)
	assert.Contains(t, body, `<button type="button" class="btn sm" data-act-yaml-edit>Edit YAML</button><button type="button" class="btn sm" data-pg-copy-text="kind: Cluster`+"\nname: hpc\n")

	body = f.post("/actions/create-cluster/preview", actForm("yaml_on", "1", "yaml", "kind: Cluster\nname: lab # mine\n", "name", "hpc")).Body.String()
	assert.NotContains(t, body, "data-act-yaml-edit")
	assert.Contains(t, body, `</span><button type="button" class="btn sm" data-pg-copy-text="kind: Cluster`+"\nname: lab # mine\n"+`">Copy YAML</button></div>`)
	assert.Contains(t, body, "The YAML that you edit, as it is.")
	assert.Contains(t, body, `<span class="act-rl act-rlsub"><span class="tok">--config</span> <span class="tok">/</span><span class="tok">state/</span><span class="tok">sind/</span><span class="tok">ui/</span><span class="tok">configs/</span><span class="tok">lab.yaml</span></span>`)
	assert.Contains(t, body, `<span class="pill ok">Ready</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("yaml_on", "1", "yaml", "kind: [")).Body.String()
	assert.Contains(t, body, `<a href="#act-yaml" data-act-jump="yaml">YAML</a>`)
	assert.Contains(t, body, `<span class="err" id="act-err-yaml" hx-swap-oob="innerHTML">`)

	body = f.post("/actions/create-cluster/preview", actForm("yaml_on", "1", "yaml", "kind: Cluster\nname: dev\n")).Body.String()
	assert.Contains(t, body, `<span class="err" id="act-err-name" hx-swap-oob="innerHTML">realm sind has a cluster dev already;`, "a problem of the name shows at Name, which stays beside the YAML")
}

func TestActPreview_NewClusterAfterCreate(t *testing.T) {
	f := newActFixture(t)

	form := actForm("name", "hpc", "w0-count", "1", "dir", "/scratch/hpc", "db", "on", "ur", "u0", "u0-name", "alice", "u0-accounts", "physics", "ar", "a0", "a0-name", "physics")
	body := f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<div class="cmdlabel">Then sind runs</div><pre class="term act-after"># On controller,db,worker-0: the users and groups
groupadd --gid 1000 alice
useradd --uid 1000 --gid 1000 --home-dir /home/alice --no-create-home --shell /bin/bash alice

# On the controller, once slurmdbd knows the cluster: the Slurm accounts
sacctmgr -i add account physics
sacctmgr -i add user alice account=physics defaultaccount=physics
</pre>`)
	assert.Contains(t, body, `<button type="button" class="btn sm" data-act-fold="act-confv" aria-expanded="false" aria-controls="act-confv">Preview slurm.conf</button>`)
	assert.Contains(t, body, `<div class="act-fpanel" id="act-confv" hidden><pre class="term act-confpre">`)
	assert.Contains(t, body, "<span>ClusterName=hpc</span>\n")

	body = f.post("/actions/create-cluster/preview", actForm("yaml_on", "1", "yaml", "kind: Cluster\nname: lab\n")).Body.String()
	assert.Contains(t, body, `<div class="act-fpanel" id="act-confv" hidden>`, "the YAML has the preview too")
	assert.NotContains(t, body, "Then sind runs")

	body = f.post("/actions/create-cluster/preview", actForm("name", "Hpc")).Body.String()
	assert.NotContains(t, body, "act-conf", "no preview while the name is wrong")
}

func TestActSameCommand(t *testing.T) {
	task := store.Task{Display: "sind create cluster --config /c/x.yaml", ConfigPath: "/c/x.yaml", Config: "name: x\n"}
	cmd := action.Command{Display: task.Display, Config: &action.File{Path: "/c/x.yaml", Content: "name: x\n"}}
	assert.True(t, actSameCommand(cmd, task))
	cmd.Config.Content = "name: y\n"
	assert.False(t, actSameCommand(cmd, task), "another config")
	assert.False(t, actSameCommand(action.Command{Display: task.Display}, task), "no config now")
	assert.True(t, actSameCommand(action.Command{Display: "sind power on x"}, store.Task{Display: "sind power on x"}))
	assert.False(t, actSameCommand(action.Command{Display: "sind power on y"}, store.Task{Display: "sind power on x"}))
}

// actEndedTask adds a task of the action that failed, with the form.
func (f *actFixture) actEndedTask(id, act, display string, kv ...string) {
	form, _ := url.ParseQuery(actForm(kv...))
	end := time.Unix(10, 0)
	f.st.AddTask(store.Task{ID: id, Action: act, Display: display, Form: form, Realm: "sind", Start: time.Unix(0, 0), End: &end, Status: store.TaskFailed})
}

func TestActRetry_RunsTheSameCommandAgain(t *testing.T) {
	f := newActFixture(t)
	f.actEndedTask("t0", "power", "sind power reboot worker-0.dev", "realm", "sind", "cluster", "dev", "op", "reboot", "nodes", "worker-0")

	w := f.postHTMX("/tasks/t0/retry", "")

	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "/tasks/task1", w.Header().Get("HX-Redirect"))
	require.Len(t, f.starter.specs, 1)
	assert.Equal(t, "sind power reboot worker-0.dev", f.starter.specs[0].Display)
	assert.Equal(t, []string{"reboot"}, f.starter.specs[0].Form["op"])
}

func TestActRetry_OpensTheDialogWhenTheCommandChanged(t *testing.T) {
	f := newActFixture(t)
	f.actEndedTask("t0", "power", "sind power reboot worker-9.dev", "realm", "sind", "cluster", "dev", "op", "reboot", "nodes", "worker-0")
	f.actEndedTask("t1", "delete-cluster", "sind delete cluster dev", "realm", "sind", "cluster", "dev")

	w := f.post("/tasks/t0/retry", "")
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	assert.Equal(t, "/dialogs/power?task=t0", w.Header().Get("Location"))
	w = f.postHTMX("/tasks/t1/retry", "")
	assert.Equal(t, "/dialogs/delete-cluster?task=t1", w.Header().Get("HX-Redirect"), "the dialog asks for the name")
	assert.Empty(t, f.starter.specs)
}

func TestActRetry_Errors(t *testing.T) {
	f := newActFixture(t)
	f.actEndedTask("t0", "power", "sind power reboot worker-0.dev", "realm", "sind", "cluster", "dev", "op", "reboot", "nodes", "worker-0")
	f.st.AddTask(store.Task{ID: "run", Action: "power", Form: url.Values{"cluster": {"dev"}}, Status: store.TaskRunning})
	f.st.AddTask(store.Task{ID: "bare", Action: "power", Status: store.TaskFailed})

	assert.Equal(t, http.StatusNotFound, f.post("/tasks/nope/retry", "").Code)
	assert.Equal(t, http.StatusBadRequest, f.post("/tasks/bare/retry", "").Code)
	assert.Equal(t, http.StatusConflict, f.post("/tasks/run/retry", "").Code)
	assert.Equal(t, http.StatusBadRequest, f.post("/tasks/"+strings.Repeat("x", maxParamLen+1)+"/retry", "").Code)
	f.starter.startErr = errors.New("no fork")
	assert.Equal(t, http.StatusInternalServerError, f.post("/tasks/t0/retry", "").Code)
}

func TestActDialog_FillsInTheFormOfATask(t *testing.T) {
	f := newActFixture(t)
	f.actEndedTask("t0", "create-worker", "sind create worker dev --count 3", "realm", "sind", "cluster", "dev", "count", "3")
	f.actEndedTask("t1", "power", "sind power on worker-0.dev", "realm", "sind", "cluster", "dev", "op", "on", "nodes", "worker-0")

	body := f.get("/dialogs/create-worker?task=t0").Body.String()
	assert.Contains(t, body, `id="act-count" name="count" value="3"`)
	body = f.get("/dialogs/power?task=t1&op=reboot").Body.String()
	assert.Contains(t, body, `value="reboot" checked`, "the link changes the form")
	assert.Contains(t, f.get("/dialogs/power?task=t1").Body.String(), `value="on" checked`)
	assert.Equal(t, http.StatusNotFound, f.get("/dialogs/create-worker?task=nope").Code)
	assert.Equal(t, http.StatusNotFound, f.get("/dialogs/delete-worker?task=t0&cluster=dev").Code, "a task of another action")
}

func TestActRestartServiceDialog(t *testing.T) {
	f := newActFixture(t)
	// No status yet: the dialog says that sind ui has not probed the node.
	body := f.get("/dialogs/restart-service?realm=sind&cluster=dev&nodes=worker-1").Body.String()
	assert.Contains(t, body, "Restart a service of worker-1.dev")
	assert.Contains(t, body, "sind ui has not probed this node yet.")
	f.st.SetStatus("sind", "dev", &cluster.Status{Name: "dev", Nodes: []*cluster.NodeStatus{
		nil,
		{Name: "worker-1.dev", Health: &cluster.NodeHealth{State: cluster.StateRunning, Services: cluster.ServiceHealth{"slurmd": false, "munge": true, "sshd": true}}},
		{Name: "worker-0.dev", Health: &cluster.NodeHealth{State: cluster.StateRunning}},
		{Name: "controller.dev", Health: &cluster.NodeHealth{State: cluster.StateStopped}},
		{Name: "worker-9.dev"},
	}})
	f.st.SetFaults("sind", "dev", map[string]store.Fault{"worker-1": {Units: []store.Unit{{Name: "slurmd", State: "failed"}}}})
	// The services of the node, in the order of the health matrix, in the
	// words of the node page.
	body = f.get("/dialogs/restart-service?realm=sind&cluster=dev&nodes=worker-1&unit=slurmd").Body.String()
	assert.Contains(t, body, `<input type="hidden" name="nodes" value="worker-1">`)
	assert.Contains(t, body, `<label><input type="radio" name="unit" value="munge"> <span class="mono">munge</span> <small>active</small></label><label><input type="radio" name="unit" value="sshd"> <span class="mono">sshd</span> <small>active</small></label><label><input type="radio" name="unit" value="slurmd" checked> <span class="mono">slurmd</span> <small>failed</small></label>`)
	f.st.SetFaults("sind", "dev", nil)
	body = f.get("/dialogs/restart-service?realm=sind&cluster=dev&nodes=worker-1&unit=slurmd").Body.String()
	assert.Contains(t, body, `<span class="mono">slurmd</span> <small>down</small></label>`, "before sind ui read how it failed")
	// Without a node, as the jump list opens it: the nodes that run and
	// that sind ui probed, each with what is down on it, and the dialog
	// loads again with the one picked.
	body = f.get("/dialogs/restart-service?realm=sind&cluster=dev").Body.String()
	assert.Contains(t, body, "Restart a service of a node of dev")
	assert.Contains(t, body, `<select id="act-nodes" name="nodes" hx-get="/dialogs/restart-service" hx-include="closest form" hx-target="#layer" hx-swap="innerHTML" aria-describedby="act-hint-nodes"><option value="">Pick a node</option><option value="worker-0">worker-0</option><option value="worker-1">worker-1 · slurmd down</option></select>`)
	f.st.SetStatus("sind", "dev", nil)
	assert.Contains(t, f.get("/dialogs/restart-service?realm=sind&cluster=dev").Body.String(), `<option value="">Pick a node</option></select>`)
}
