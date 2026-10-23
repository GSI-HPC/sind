// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

func (f *actFakeStarter) Running() int { return 0 }

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
	return f
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
			`id="act-db" name="db" checked`, `id="act-submitter" name="submitter" checked`, `id="act-backup" name="backup" aria-`, `id="act-pull" name="pull" checked`,
			`<aside class="act-planpane" aria-label="Plan">`, `<span class="act-status" id="act-status"></span>`, `id="act-say" role="status"`, `value="~/sind/hpc"`, `data-act-follow`,
			"ghcr.io/gsi-hpc/sind-node", `value="5m" checked`, `id="act-pull-auto" name="pull_auto" value="1"`,
			`id="act-realm" name="realm" value="sind" list="act-dl-realms"`, `<datalist id="act-dl-realms"><option value="sind"></option></datalist>`, "A new name makes a new realm.",
			// A size chip is named by its node and its value; the controls of a
			// worker group by the workers it makes.
			`aria-labelledby="act-czl-controller act-chip-controller"><span class="vh" id="act-czl-controller">Size of controller</span>`,
			`aria-labelledby="act-czl-w0 act-range-w0 act-chip-w0"><span class="vh" id="act-czl-w0">Size of workers</span>`,
			`aria-label="Fewer in workers" aria-labelledby="act-w0-less act-range-w0"`, `aria-label="More in workers" aria-labelledby="act-w0-more act-range-w0"`,
			`aria-label="Remove workers" aria-labelledby="act-w0-del act-range-w0"`}},
		{"create-cluster", "wait=7m&pull=&image=ghcr.io/gsi-hpc/sind-node:26.05.4", []string{`value="other" checked`, `name="wait_other" value="7m"`, `id="act-pull" name="pull" aria-`}},
		{"create-cluster", "wait=0&image=ghcr.io/gsi-hpc/sind-node:26.05.4", []string{`value="0" checked`, `id="act-pull" name="pull" aria-`, `name="pull_auto"`}},
		{"create-cluster", "realm=ci&name=big", []string{`value="big"`, `value="~/sind/big"`, `name="realm" value="ci"`, `<option value="ci"></option><option value="sind"></option>`}},
		{"create-cluster", "name=dev", []string{`value="dev"`}},
		{"create-cluster", "dir=/scratch", []string{`name="name" value="hpc"`, `value="/scratch"`}},
		{"create-worker", "cluster=dev", []string{"Add workers to dev", `name="count" value="1"`, `name="cluster" value="dev"`}},
		{"delete-cluster", "cluster=dev", []string{"Delete cluster dev", "Deletes cluster dev of realm sind. This cannot be undone.", `Type <b class="mono" id="act-confirm-word">dev</b> to confirm`,
			`autocapitalize="none" autocorrect="off" spellcheck="false" aria-describedby="act-why"`, `btn danger solid`, `disabled aria-describedby="act-why">Delete cluster<kbd`}},
		{"delete-worker", "cluster=dev&nodes=worker-1", []string{"Delete workers of dev", `value="worker-1" checked`, `value="worker-0"> `, `id="act-confirm-word">worker-1</b>`}},
		{"delete-worker", "cluster=dev&nodes=worker-0,worker-1", []string{`id="act-confirm-word">dev</b>`}},
		{"power", "cluster=dev&nodes=worker-0,worker-1&op=cut", []string{"Power and drain: nodes of dev", `value="worker-0" checked`, `value="worker-1" checked`, `value="controller">`, `value="cut" checked`, "Cut power",
			`<legend class="lbl">Slurm (scontrol)</legend>`, `<legend class="lbl">Containers</legend>`, `<fieldset class="fld act-ops act-danger"><legend class="lbl">Can lose data</legend>`,
			`id="act-reason" name="reason" value=""`, "Drain and Resume change what Slurm does"}},
		{"power", "cluster=dev&op=drain&reason=disk", []string{`value="drain" checked`, `name="reason" value="disk"`}},
		{"power", "cluster=dev&op=resume", []string{`value="resume" checked`}},
		{"power", "cluster=dev&op=stop", []string{`value="shutdown" checked`}},
		{"power", "cluster=dev&op=melt", []string{`value="shutdown" checked`}},
		{"connect", "container=kind-cp", []string{"Connect a container", `value="kind-cp"`, `id="act-realm" name="realm" value="sind"`, "The realm whose networks the container joins."}},
		{"disconnect", "", []string{"Disconnect a container", "connected to realm sind"}},
		{"delete-realm", "", []string{"Delete realm sind", "Deletes every cluster of realm sind", `Type <b class="mono" id="act-confirm-word">sind</b> to confirm`}},
	} {
		t.Run(tc.id+"?"+tc.query, func(t *testing.T) {
			w := f.get("/dialogs/" + tc.id + "?" + tc.query)
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
	w := f.get("/dialogs/create-cluster?name=x&memory=3g&dtype=volume&monitoring=on")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `name="memory" value="3g"`)
	assert.Contains(t, body, `value="volume" checked`)
	assert.Contains(t, body, `name="monitoring" checked`)
	assert.Contains(t, body, ` readonly aria-describedby="act-err-dir`, "a volume needs no directory")
}

func TestActDialog_NewClusterTakesAFreeName(t *testing.T) {
	f := newActFixture(t)
	f.st.SetClusters("sind", []cluster.Summary{{Name: "hpc"}, {Name: "hpc-2"}})

	body := f.get("/dialogs/create-cluster").Body.String()

	assert.Contains(t, body, `id="act-name" name="name" value="hpc-3" data-act-select`, "the script selects a default name")
	assert.Contains(t, body, `value="~/sind/hpc-3" autocapitalize="off" spellcheck="false" data-act-follow`)
	assert.Contains(t, body, `<b>workers</b>`)
	assert.NotContains(t, f.get("/dialogs/create-cluster?dir=/x").Body.String(), "data-act-follow", "a typed directory stays")
	assert.Contains(t, f.get("/dialogs/create-cluster?name=lab").Body.String(), `id="act-name" name="name" value="lab" autocomplete="off"`, "a name of the link stays as it is")

	body = f.get("/dialogs/create-cluster?wg=w0&wg=wabc&w0-count=1&wabc-count=2").Body.String()
	assert.Contains(t, body, `<b>workers 1</b>`)
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
	assert.Contains(t, f.post("/actions/power/preview", power+"&op=on").Body.String(), "holds the lock")
	assert.NotContains(t, f.post("/actions/delete-realm/preview", actForm("realm", "ci")).Body.String(), "holds the lock")
}

func TestActPreview_ShowsTheCommand(t *testing.T) {
	f := newActFixture(t)

	w := f.post("/actions/power/preview", actForm("cluster", "dev", "op", "on", "nodes", "worker-0", "nodes", "worker-1"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `<code>sind power on &#39;worker-[0-1].dev&#39;</code>`)
	assert.Contains(t, body, `data-copy="sind power on &#39;worker-[0-1].dev&#39;"`)
	assert.Contains(t, body, `id="act-ok" aria-keyshortcuts="Control+Enter Meta+Enter" hx-swap-oob="true">Start 2 nodes<kbd`)
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
	assert.Contains(t, body, `disabled>Run<kbd`)
	assert.NotContains(t, body, `name="shown"`)

	// A value that the form lacks is a hint, not an error.
	for _, c := range []struct{ id, form, hint string }{
		{"power", actForm("cluster", "dev", "op", "on"), "Pick at least one node of dev."},
		{"connect", actForm("container", " "), "Write the name of a container, such as kind-control-plane."},
	} {
		body = f.post("/actions/"+c.id+"/preview", c.form).Body.String()
		assert.Contains(t, body, `<p class="hint act-missing">`+c.hint+`</p>`, c.id)
		assert.NotContains(t, body, `callout bad`, c.id)
		assert.Regexp(t, `id="act-ok"[^>]* disabled>`, body, c.id)
		assert.NotContains(t, body, `name="shown"`, c.id)
	}

	// The typed confirmation unlocks the button.
	del := actForm("cluster", "dev")
	body = f.post("/actions/delete-cluster/preview", del).Body.String()
	assert.Contains(t, body, `disabled aria-describedby="act-why">Delete cluster dev<kbd`)
	assert.Contains(t, body, `<span id="act-why" hx-swap-oob="innerHTML">The button stays off until the name matches.</span>`)
	assert.Contains(t, body, `<b class="mono" id="act-confirm-word" hx-swap-oob="true">dev</b>`)
	body = f.post("/actions/delete-cluster/preview", del+"&confirm=dev").Body.String()
	assert.NotContains(t, body, `disabled`)
	assert.Contains(t, body, `<span id="act-why" hx-swap-oob="innerHTML"></span>`)
	assert.Contains(t, f.post("/actions/delete-cluster/preview", del+"&confirm=Dev").Body.String(), "Case matters: type dev, not Dev.")
	assert.Contains(t, f.post("/actions/delete-cluster/preview", del+"&confirm=de").Body.String(), "de is not dev. Type the name as it is shown.")

	// One worker is confirmed with its own name, more with the cluster's.
	body = f.post("/actions/delete-worker/preview", actForm("cluster", "dev", "nodes", "worker-0")).Body.String()
	assert.Contains(t, body, `id="act-confirm-word" hx-swap-oob="true">worker-0</b>`)
	assert.Contains(t, body, `>Delete worker-0<kbd`)
	assert.NotContains(t, f.post("/actions/delete-worker/preview", actForm("cluster", "dev", "nodes", "worker-0", "confirm", "worker-0 ")).Body.String(), "disabled")
	body = f.post("/actions/delete-worker/preview", actForm("cluster", "dev", "nodes", "worker-0,worker-1")).Body.String()
	assert.Contains(t, body, `id="act-confirm-word" hx-swap-oob="true">dev</b>`)
	assert.Contains(t, body, `>Delete 2 workers<kbd`)
}

func TestActPreview_ListsWhatGoesAndStays(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/delete-cluster/preview", actForm("cluster", "dev")).Body.String()
	assert.Contains(t, body, `<div class="callout bad act-list"><b>Goes</b><ul><li>3 containers: controller, worker-0, worker-1</li>`)
	assert.Contains(t, body, `<div class="callout act-list"><b>Stays</b><ul><li>the host directories it mounts, if any</li><li>the other cluster of realm sind, and its mesh</li></ul></div>`)

	body = f.post("/actions/delete-realm/preview", actForm("realm", "sind")).Body.String()
	assert.Contains(t, body, "<li>2 clusters: default, dev</li>")
	assert.Contains(t, body, "<code>sind --realm sind delete realm</code>")
}

func TestActConnect_ProblemsAtTheirFields(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/dialogs/connect").Body.String()
	assert.Less(t, strings.Index(body, `id="act-container"`), strings.Index(body, `id="act-realm"`), "the field to fill comes first and gets the focus")
	assert.Contains(t, body, `aria-describedby="act-err-container act-hint-container"><span class="err" id="act-err-container"></span>`)
	assert.Contains(t, body, "It needs a cluster, whose mesh comes with it.")

	body = f.post("/actions/connect/preview", actForm("realm", "No", "container", "kind-cp")).Body.String()
	assert.Contains(t, body, `<span class="err" id="act-err-realm" hx-swap-oob="innerHTML">use `)
	assert.Contains(t, body, `<span class="err" id="act-err-container" hx-swap-oob="innerHTML"></span>`)
	assert.Contains(t, body, `<p class="hint act-missing">Fix the field above to see the command.</p>`)
	assert.NotContains(t, body, "callout bad", "the field says it")
	assert.Regexp(t, `id="act-ok"[^>]* disabled>`, body)

	body = f.post("/actions/connect/preview", actForm("container", "a;b")).Body.String()
	assert.Contains(t, body, `<span class="err" id="act-err-container" hx-swap-oob="innerHTML">write the name of a container, such as kind-control-plane</span>`)

	body = f.post("/actions/connect/preview", actForm("realm", "new", "container", "kind-cp")).Body.String()
	assert.Contains(t, body, "<code>sind --realm new connect container kind-cp</code>")
	assert.Contains(t, body, `<div class="callout warn">sind ui sees no realm new. sind connects a container only to a realm with a cluster, whose mesh it joins; create a cluster in new first.</div>`)

	// A dialog without fields of its own keeps the error in the preview.
	body = f.post("/actions/disconnect/preview", actForm("realm", "No", "container", "kind-cp")).Body.String()
	assert.Contains(t, body, `<div class="callout bad">realm: use `)
	assert.NotContains(t, body, "act-err-realm")
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
	body = f.post("/actions/power/preview", actForm("cluster", "dev", "op", "reboot")).Body.String()
	assert.NotContains(t, body, "lock")
}

func TestActPreview_NewClusterFillsThePanels(t *testing.T) {
	f := newActFixture(t)

	w := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "w0-count", "2", "dir", "/scratch/hpc", "ur", "u0", "u0-name", "ada"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `<code>sind create cluster --config /state/sind/ui/configs/hpc.yaml</code>`)
	assert.Contains(t, body, `<div class="act-tiles"><span class="act-tile controller">controller</span><span class="act-tile worker">worker-0</span><span class="act-tile worker">worker-1</span></div>`)
	assert.Contains(t, body, `<span class="act-path">/state/sind/ui/configs/hpc.yaml</span>`)
	assert.Contains(t, body, `<span class="pill ok">Ready</span>`)
	assert.Contains(t, body, `<span class="err" id="act-err-name" hx-swap-oob="innerHTML"></span>`)
	assert.Contains(t, body, `id="act-ok" aria-keyshortcuts="Control+Enter Meta+Enter" hx-swap-oob="true">Create<kbd`)
	assert.Contains(t, body, "kind: Cluster\nname: hpc\n")
	assert.Contains(t, body, "- name: ada")
	assert.Contains(t, body, "saves the config to /state/sind/ui/configs/hpc.yaml")
}

func TestActRoutes_WithoutTheRunner(t *testing.T) {
	f := newActFixture(t)
	f.srv.env.values.Delete(actKey{})

	assert.Equal(t, http.StatusInternalServerError, f.post("/actions/power/preview", "").Code)
	assert.Equal(t, http.StatusInternalServerError, f.postHTMX("/actions/power/run", "").Code)
	assert.Equal(t, http.StatusInternalServerError, f.post("/tasks/t/cancel", "").Code)
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

	// The dialog closes, and the page behind it gets a notice of the run.
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, w.Header().Get("HX-Redirect"))
	assert.Equal(t, "#act-notes", w.Header().Get("HX-Retarget"))
	assert.Equal(t, "beforeend", w.Header().Get("HX-Reswap"))
	assert.JSONEq(t, `{"act-started": {"url": "/tasks/task1", "follow": false}}`, w.Header().Get("HX-Trigger-After-Settle"))
	assert.Equal(t, `<div class="act-note" data-act-note><p>Started <code>sind power reboot worker-0.dev</code>. <a href="/tasks/task1">Show progress</a></p>`+
		`<button type="button" class="iconbtn" data-act-note-close aria-label="Close this notice" title="Close">×</button></div>`, w.Body.String())
	require.Len(t, f.starter.specs, 1)
	assert.Equal(t, run.Spec{
		Action: "power", Argv: []string{"sind", "power", "reboot", "worker-0.dev"},
		Display: "sind power reboot worker-0.dev", Realm: "sind", Cluster: "dev",
	}, f.starter.specs[0])
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
	assert.Equal(t, []string{"/state/sind/ui/configs/hpc.yaml"}, mapKeys(f.saved))
	assert.Contains(t, f.saved["/state/sind/ui/configs/hpc.yaml"], "name: hpc\nrealm: ci\n")
	require.Len(t, f.starter.specs, 1)
	assert.Equal(t, "sind --realm ci create cluster --config /state/sind/ui/configs/hpc.yaml --wait 2m", f.starter.specs[0].Display)
	assert.Equal(t, "ci", f.starter.specs[0].Realm)
	assert.Equal(t, "hpc", f.starter.specs[0].Cluster)
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
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">Pull, wait 5m</span>`)

	body = f.post("/actions/create-cluster/preview", form+"&pull=on&image=ghcr.io/gsi-hpc/sind-node:26.05.4&wait=0").Body.String()
	assert.Contains(t, body, `data-act-pull="off" hx-swap-oob="true">Off, as the tag 26.05.4 is fixed.</span>`)
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">No pull, no wait limit</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "pull", "on", "wait", "other", "wait_other", "20m")).Body.String()
	assert.Contains(t, body, `data-act-pull="on" hx-swap-oob="true">Gets the newest image first, even if Docker has one.</span>`)
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">Pull, wait 20m</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "wait", "2m")).Body.String()
	assert.Contains(t, body, `id="act-sum-run" hx-swap-oob="innerHTML">No pull, wait 2m</span>`)
	assert.Contains(t, body, `data-copy="cfg=`, "Copy gives the script")
}

func TestActDialog_NewClusterNodes(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/dialogs/create-cluster").Body.String()
	assert.Contains(t, body, `<input type="hidden" name="wg" value="w0">`)
	assert.Contains(t, body, `<span class="hint" id="act-range-w0">worker-0 … worker-1</span>`)
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

	body = f.get("/dialogs/create-cluster?workers=abc&cpus=3").Body.String()
	assert.Contains(t, body, `<span class="hint" id="act-range-w0">worker-0</span>`, "a wrong count is one worker")
	assert.Contains(t, body, `name="size" value="x" checked`, "a link with a size gives custom values")
	assert.Contains(t, body, `placeholder="3" data-act-ph="cpus"`)

	body = f.get("/dialogs/create-cluster?workers=5").Body.String()
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
	assert.Contains(t, body, `<span class="hint" id="act-range-w0" hx-swap-oob="true">worker-0 … worker-1</span>`)
	assert.Contains(t, body, `<span class="hint" id="act-range-w1" hx-swap-oob="true">worker-2 … worker-4</span>`)
	assert.NotContains(t, body, "act-chip-bad", "a key that the dialog did not make gets no swap")
	assert.Contains(t, body, `<span id="act-eff" hidden hx-swap-oob="true" data-image="ghcr.io/x/y:1" data-cpus="4" data-memory="4g" data-tmp="1g"></span>`)
	assert.Contains(t, body, `id="act-err-w1-cpus" hx-swap-oob="innerHTML"></span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "size", "x", "cpus", "3", "w0-count", "1")).Body.String()
	assert.Contains(t, body, `data-image="ghcr.io/gsi-hpc/sind-node:latest" data-cpus="3" data-memory="512m" data-tmp="256m"`)
	assert.Contains(t, body, `id="act-range-w0" hx-swap-oob="true">worker-0</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("realm", "Bad Realm")).Body.String()
	assert.Contains(t, body, `id="act-range-w0" hx-swap-oob="true">worker-0</span>`, "a wrong realm is a problem; the plan shows")

	f.state.configDir = ""
	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `id="act-range-w0" hx-swap-oob="true"></span>`, "no plan, no range")
}

func TestActPreview_NewClusterMemoryMeter(t *testing.T) {
	f := newActFixture(t)
	form := actForm("name", "hpc", "w0-count", "2")

	assert.NotContains(t, f.post("/actions/create-cluster/preview", form).Body.String(), "<meter", "a host of unknown size")

	f.st.SetDocker(store.DockerState{Checked: true, MemTotal: 2 << 30})
	body := f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<meter min="0" max="2048" value="1536" high="1638" optimum="0" aria-label="Memory of the Docker host"></meter><span class="hint">1.5 GiB of the 2 GiB of the Docker host</span>`)
	assert.NotContains(t, body, "act-overcommit")

	form = actForm("name", "hpc", "w0-count", "4", "dir", "/scratch/hpc")
	body = f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<a href="#act-overcommit" data-act-jump="overcommit">Memory</a>: the nodes may use 2.5 GiB, more than the 2 GiB of the Docker host; give them less memory, or tick Create anyway`)
	assert.Contains(t, body, `<input type="checkbox" id="act-overcommit" name="overcommit" data-act-hold aria-describedby="act-hint-overcommit"><span class="act-lab"><b>Create anyway</b>`)
	assert.Contains(t, body, `<span class="pill bad">Not ready · 1 problem</span>`)

	form = actForm("name", "hpc", "w0-count", "4", "overcommit", "on", "dir", "/scratch/hpc")
	body = f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<input type="checkbox" id="act-overcommit" name="overcommit" checked data-act-hold aria-describedby="act-hint-overcommit">`)
	assert.Contains(t, body, `<span class="act-status" id="act-status" hx-swap-oob="innerHTML"><span class="pill warn" data-act-say="Ready, with 1 warning. Create runs the command." data-act-key="ready 1">Ready · 1 warning</span>`)

	form = actForm("name", "hpc", "w0-count", "4", "overcommit", "on", "slurm-main", "ClusterName=prod", "dir", "/scratch/hpc")
	body = f.post("/actions/create-cluster/preview", form).Body.String()
	assert.Contains(t, body, `<span class="pill warn">Ready · 2 warnings</span>`)
}

func TestActPreview_NewClusterSumsUpTheSections(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "dir", "~/sind/missing", "mkdir", "on", "mount", "/work")).Body.String()
	assert.Contains(t, body, `<span class="act-ss" id="act-sum-data" hx-swap-oob="innerHTML">~/sind/missing, created, at /work</span>`)
	assert.Contains(t, body, `<span class="act-sb" id="act-bad-data" hx-swap-oob="innerHTML"></span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "dtype", "volume", "mount", "work,x")).Body.String()
	assert.Contains(t, body, `id="act-sum-data" hx-swap-oob="innerHTML">Docker volume, at work,x</span>`)
	assert.Contains(t, body, `id="act-bad-data" hx-swap-oob="innerHTML">1 problem</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "dir", "rel", "dtype", "nfs")).Body.String()
	assert.Contains(t, body, `id="act-sum-data" hx-swap-oob="innerHTML">rel</span>`)
	assert.Contains(t, body, `id="act-bad-data" hx-swap-oob="innerHTML">2 problems</span>`)
}

func mapKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestActRun_RefusesTheFormThatDoesNotBuild(t *testing.T) {
	f := newActFixture(t)

	w := f.postHTMX("/actions/power/run", actForm("cluster", "dev", "op", "on")+"&shown=x")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "#act-preview", w.Header().Get("HX-Retarget"))
	assert.Equal(t, "innerHTML", w.Header().Get("HX-Reswap"))
	assert.Contains(t, w.Body.String(), `<div class="callout bad">Pick at least one node of dev.</div>`)
	assert.NotContains(t, w.Body.String(), "act-missing")
	assert.Contains(t, w.Body.String(), "disabled>Run<kbd")
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
	assert.Contains(t, w.Header().Get("HX-Trigger-After-Settle"), `"/tasks/task1"`)
	assert.Equal(t, "sind delete cluster dev", f.starter.specs[0].Display)
}

func TestActRun_RefusesACommandThatChanged(t *testing.T) {
	f := newActFixture(t)
	form := actForm("cluster", "dev", "op", "on", "nodes", "worker-0")
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
	form := actForm("cluster", "dev", "op", "on", "nodes", "worker-0")
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
	restore := actSeams(t)
	defer restore()
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
	restore := actSeams(t)
	defer restore()

	actExecutable = func() (string, error) { return "", errors.New("no /proc") }
	assert.EqualError(t, actService().Setup(cfg.Env), "finding the sind executable: no /proc")

	actExecutable = func() (string, error) { return "/usr/bin/sind", nil }
	actStateDir = func() (string, error) { return "", errors.New("no home") }
	assert.EqualError(t, actService().Setup(cfg.Env), "finding the state directory: no home")
	assert.Nil(t, cfg.Env.Get(actKey{}))
}

func TestActService_SetupSurvivesWhatSindDefaults(t *testing.T) {
	cfg, _ := testConfig(t)
	restore := actSeams(t)
	defer restore()
	actExecutable = func() (string, error) { return "/usr/bin/sind", nil }
	actStateDir = func() (string, error) { return "/state/sind", nil }
	actActivityLog = func() (string, error) { return "", errors.New("read-only home") }
	actHome = func() (string, error) { return "", errors.New("no home") }

	require.NoError(t, actService().Setup(cfg.Env))

	got := cfg.Env.Get(actKey{}).(*actState)
	assert.Empty(t, got.home)
}

// actSeams returns the function that puts the seams of Setup back.
func actSeams(t *testing.T) func() {
	t.Helper()
	e, d, l, h, v := actExecutable, actStateDir, actActivityLog, actHome, actEnviron
	return func() { actExecutable, actStateDir, actActivityLog, actHome, actEnviron = e, d, l, h, v }
}

func TestActService_RefreshesTheCollectorAfterATask(t *testing.T) {
	cfg, st := testConfig(t)
	restore := actSeams(t)
	defer restore()
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
	assert.Contains(t, body, "<title>Connect a container · sind ui</title>")
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
	assert.Contains(t, body, `<span class="act-tile more">6 more</span>`, "21 nodes show as 15 tiles and the rest")
	assert.Contains(t, body, "kind: Cluster\nname: dev\n", "the plan still shows the config")
	assert.Contains(t, body, `disabled>Create<kbd`)
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

	body := f.get("/dialogs/create-cluster").Body.String()
	assert.Contains(t, body, `<details class="act-sec" id="act-sec-users"><summary>`, "no rows: the section is closed")
	assert.Contains(t, body, `<template id="act-tpl-user"><div class="act-row" id="act-row-__ROW__" data-act-row><input type="hidden" name="ur" value="__ROW__">`)
	assert.Contains(t, body, `<template id="act-tpl-limit"><div class="act-rowf act-lim" data-act-row>
  <input type="text" name="__ROW__-lk" value=""`)
	assert.Contains(t, body, `<option value="MaxJobs"></option>`)

	body = f.get("/dialogs/create-cluster?users=ada,bob").Body.String()
	assert.Contains(t, body, `<details class="act-sec" id="act-sec-users" open>`)
	assert.Contains(t, body, `id="act-u0-name" name="u0-name" value="ada"`)
	assert.Contains(t, body, `id="act-u1-name" name="u1-name" value="bob"`)
	// Each remove button is named after the name in its row.
	assert.Contains(t, body, `id="act-u1-del" data-act-rowdel aria-label="Remove the user" aria-labelledby="act-u1-del act-u1-name"`)

	body = f.get("/dialogs/create-cluster?" + actForm("ar", "a0", "a0-name", "physics", "a0-lk", "MaxJobs", "a0-lv", "4", "a0-lk", "GrpTRES",
		"ur", "u0", "u0-admin", "operator", "gr", "g0", "gr", "api", "g0-name", "hpc", "evil", "x")).Body.String()
	assert.Contains(t, body, `name="a0-lk" value="MaxJobs"`)
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
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">2 users, 1 group, 1 account</span>`)
	assert.Contains(t, body, `<datalist id="act-dl-groups" hx-swap-oob="true"><option value="hpc"></option></datalist>`)
	assert.Contains(t, body, `<datalist id="act-dl-accounts" hx-swap-oob="true"><option value="physics"></option></datalist>`)
	assert.Contains(t, body, `id="act-err-u1-name" hx-swap-oob="innerHTML"></span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "ur", "u0", "u0-name", "Ada")).Body.String()
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">1 user</span>`)
	assert.Contains(t, body, `id="act-bad-users" hx-swap-oob="innerHTML">1 problem</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `id="act-sum-users" hx-swap-oob="innerHTML">No users</span>`)
}

func TestActDialog_NewClusterSlurmAndIdentity(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/dialogs/create-cluster").Body.String()
	assert.Contains(t, body, `<details class="act-sec" id="act-sec-identity"><summary>`)
	assert.Contains(t, body, `<input type="radio" name="identity" value="local" checked id="act-identity">`)
	assert.Contains(t, body, `<details class="act-sec act-slurm" id="act-sec-slurm"><summary>`)
	assert.Contains(t, body, `<input type="radio" name="stab" value="main" checked>`)
	assert.Contains(t, body, `<textarea id="act-slurm-gres" name="slurm-gres" rows="6" placeholder="such as Name=gpu File=/dev/null Count=1"`)
	assert.Contains(t, body, `id="act-cvmfs" name="cvmfs"`)
}

func TestActPreview_NewClusterSlurmAndIdentity(t *testing.T) {
	f := newActFixture(t)

	body := f.post("/actions/create-cluster/preview", actForm("name", "hpc", "slurm-main", "MaxJobCount=100", "slurm-gres", " ",
		"identity", "clientIds", "ctlusers", "on", "cvmfs", "on", "dtype", "volume")).Body.String()
	assert.Contains(t, body, `id="act-sum-slurm" hx-swap-oob="innerHTML">Adds to slurm.conf</span>`)
	assert.Contains(t, body, `<span class="act-dot" id="act-dot-main" hx-swap-oob="true"> •</span>`)
	assert.Contains(t, body, `<span class="act-dot" id="act-dot-gres" hx-swap-oob="true"></span>`)
	assert.Contains(t, body, `id="act-sum-identity" hx-swap-oob="innerHTML">clientIds, users on the controllers too</span>`)
	assert.Contains(t, body, `id="act-sum-data" hx-swap-oob="innerHTML">Docker volume, CVMFS</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc", "identity", "nssSlurm", "ctlusers", "on")).Body.String()
	assert.Contains(t, body, `id="act-sum-slurm" hx-swap-oob="innerHTML">No additions</span>`)
	assert.Contains(t, body, `id="act-sum-identity" hx-swap-oob="innerHTML">nssSlurm</span>`)

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `id="act-sum-identity" hx-swap-oob="innerHTML">local</span>`)
}

func TestActPreview_NewClusterYAML(t *testing.T) {
	f := newActFixture(t)

	body := f.get("/dialogs/create-cluster").Body.String()
	assert.Contains(t, body, `<textarea id="act-yaml" name="yaml" rows="26" placeholder="kind: Cluster&#10;name: hpc`)
	assert.Contains(t, body, `aria-describedby="act-err-yaml act-hint-yaml" disabled></textarea>`)
	assert.Contains(t, body, `<input type="hidden" id="act-yaml-on" name="yaml_on" value="1" disabled>`)
	assert.Contains(t, body, `<button type="button" class="btn sm" data-act-yaml-start>Start from YAML…</button>`)
	assert.Contains(t, body, `<button type="button" class="btn sm" data-act-yaml-open aria-describedby="act-hint-yaml">Open a file…</button>`)
	assert.Contains(t, body, `<input type="file" id="act-yaml-file" accept=".yaml,.yml,application/yaml,text/yaml,text/plain" hidden>`)
	assert.Less(t, strings.Index(body, `id="act-name"`), strings.Index(body, `id="act-yamled"`), "Name comes first, and stays beside the YAML")

	body = f.post("/actions/create-cluster/preview", actForm("name", "hpc")).Body.String()
	assert.Contains(t, body, `<div class="act-tools"><button type="button" class="btn sm" data-act-yaml-edit>Edit YAML</button><button type="button" class="btn sm" data-copy="kind: Cluster`+"\nname: hpc\n")
	assert.Less(t, strings.Index(body, "data-act-yaml-edit"), strings.Index(body, "act-tiles"), "Edit YAML is at the top of the plan")

	body = f.post("/actions/create-cluster/preview", actForm("yaml_on", "1", "yaml", "kind: Cluster\nname: lab # mine\n", "name", "hpc")).Body.String()
	assert.NotContains(t, body, "data-act-yaml-edit")
	assert.Contains(t, body, `<div class="act-tools"><button type="button" class="btn sm" data-copy="kind: Cluster`+"\nname: lab # mine\n"+`">Copy YAML</button></div>`)
	assert.Contains(t, body, "The YAML that you edit, as it is.")
	assert.Contains(t, body, `<code>sind create cluster --config /state/sind/ui/configs/lab.yaml</code>`)
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
	assert.Contains(t, body, `<details class="act-conf" id="act-conf" data-act-hold><summary>Preview slurm.conf</summary>`)
	assert.Contains(t, body, "ClusterName=hpc\n")

	body = f.post("/actions/create-cluster/preview", actForm("yaml_on", "1", "yaml", "kind: Cluster\nname: lab\n")).Body.String()
	assert.Contains(t, body, `<details class="act-conf" id="act-conf" data-act-hold>`, "the YAML has the preview too")
	assert.NotContains(t, body, "Then sind runs")

	body = f.post("/actions/create-cluster/preview", actForm("name", "Hpc")).Body.String()
	assert.NotContains(t, body, "act-conf", "no preview while the name is wrong")
}
