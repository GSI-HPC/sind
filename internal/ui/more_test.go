// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/collect"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoreMeshPage(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	body := pgBody(t, ts, "/mesh", http.StatusOK)
	for _, want := range []string{
		"Mesh and DNS", "Realm <b class=\"mono\">sind</b>", "Realm <b class=\"mono\">ci</b>",
		"sind-mesh", "172.18.0.0/16", "sind-dns", "172.18.255.254", "sind.sind", "sind-ssh-config", "2 records",
		"worker-0.default.sind.sind", "kind-control-plane",
		"/dialogs/connect?realm=sind", "/dialogs/delete-realm?realm=ci",
		"/dialogs/disconnect?container=kind-control-plane&amp;realm=sind",
		"Realm ci has no mesh", "No other containers", `sse-swap="more-mesh"`, `href="/static/`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "not reachable")
	// The records can be filtered, copied one by one or as a list, and
	// lead to their nodes.
	for _, want := range []string{
		`data-more-filter="dns-sind" placeholder="Filter by name or address" aria-label="Filter the DNS records of realm sind"`,
		`<span class="faint" role="status" data-more-count="dns-sind">2 records</span>`,
		`<table id="dns-sind" class="more-cards more-dnscards" role="table">`,
		`<tr role="row" data-more-row="172.19.0.2 controller.default.sind.sind"><td role="cell" class="mono"><a href="/nodes/sind/default/controller">controller.default.sind.sind</a></td>`,
		`data-more-copy-text="worker-0.default.sind.sind" aria-label="Copy worker-0.default.sind.sind">Copy</button>`,
		`data-more-copy-rows="dns-sind" aria-label="Copy list of realm sind, as lines of /etc/hosts">Copy list</button>`,
		`data-more-none="dns-sind" hidden>No record matches the filter.`,
		`<code>dig +short @172.18.255.254 controller.default.sind.sind</code>`,
	} {
		assert.Contains(t, body, want)
	}

	one := pgBody(t, ts, "/mesh?realm=sind", http.StatusOK)
	assert.NotContains(t, one, "<h2>Realm <b", "one realm needs no head of its own")
	assert.NotContains(t, one, "has no mesh")

	body = pgBody(t, ts, "/mesh?realm=nope", http.StatusNotFound)
	assert.Contains(t, body, "There is no realm nope.")
	assert.Contains(t, body, "with its last cluster")
	assert.Contains(t, body, `<a class="btn primary" href="/mesh?realm=">All realms</a>`)
	body = pgBody(t, ts, "/mesh?realm=sindd", http.StatusNotFound)
	assert.Contains(t, body, `<a class="btn primary" href="/mesh?realm=sind">Realm sind</a>`)
}

func TestMoreMeshOffersContainersToConnect(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	ts.st.SetMesh("ci", store.Mesh{
		Info: &mesh.Info{Network: "ci-mesh"},
		Connectable: []store.Outside{
			{Name: "db", Image: "postgres:16", State: "exited"},
			{Name: "kind-control-plane", Image: "kindest/node:v1.31.0", State: "running", Status: "Up 5 hours"},
			{Name: "my-app", Image: "python:3.12-slim", State: "running"},
		},
	})
	body := pgBody(t, ts, "/mesh?realm=ci", http.StatusOK)
	// While no container is connected, the card lists those that run and
	// can connect, each with Connect, and the command for any name.
	assert.Contains(t, body, `<span class="faint">none connected yet</span>`)
	assert.Contains(t, body, `<b class="mono">kind-control-plane</b><small>kindest/node:v1.31.0 · up 5 hours · also in realm sind</small>`)
	assert.Contains(t, body, `<a class="btn sm" href="/dialogs/connect?container=my-app&amp;realm=ci"`)
	assert.Contains(t, body, `aria-label="Connect my-app to realm ci…">Connect…</a>`)
	assert.NotContains(t, body, `container=db`, "one that does not run cannot connect")
	assert.Contains(t, body, `<span class="pg-cli"><code class="mono">sind --realm ci connect container NAME</code><button type="button" class="btn sm ghost pg-copy" data-pg-copy-text="sind --realm ci connect container NAME"`)
	assert.NotContains(t, body, "No other containers")

	ts.st.SetMesh("sind", store.Mesh{Info: &mesh.Info{Network: "sind-mesh"}, Connectable: []store.Outside{{Name: "x", State: "running"}}})
	body = pgBody(t, ts, "/mesh?realm=sind", http.StatusOK)
	assert.Contains(t, body, `<code class="mono">sind connect container NAME</code>`)
	assert.Contains(t, body, `<b class="mono">x</b><small></small>`)
}

func TestMoreMeshShowsTheFirstRecords(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	m := ts.st.Snapshot().Meshes["sind"]
	m.Records = nil
	for i := range 12 {
		m.Records = append(m.Records, mesh.DNSRecord{Hostname: fmt.Sprintf("worker-%d.dev.sind.sind", i), IP: fmt.Sprintf("172.18.0.%d", i+2)})
	}
	ts.st.SetMesh("sind", m)
	body := pgBody(t, ts, "/mesh?realm=sind", http.StatusOK)
	assert.Contains(t, body, `<tr role="row" data-more-row="172.18.0.11 worker-9.dev.sind.sind"><td`)
	assert.Contains(t, body, `<tr role="row" data-more-row="172.18.0.12 worker-10.dev.sind.sind" data-more-cut hidden><td`)
	assert.Contains(t, body, `<button type="button" class="btn sm" data-more-all="dns-sind" aria-controls="dns-sind">Show all 12 records</button>`)
	assert.NotContains(t, body, "more-scroll", "no box that scrolls in the page")

	m.Records = m.Records[:10]
	ts.st.SetMesh("sind", m)
	assert.NotContains(t, pgBody(t, ts, "/mesh?realm=sind", http.StatusOK), "data-more-all")
}

func TestMoreMeshStates(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	ts.st.SetMesh("ci", store.Mesh{Err: "docker inspect failed"})
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "cannot connect"})
	body := pgBody(t, ts, "/mesh?realm=ci", http.StatusOK)
	assert.Contains(t, body, "could not be read: docker inspect failed")
	assert.NotContains(t, body, "dig +short", "no DNS server to ask")
	assert.Contains(t, body, "Docker is not reachable: cannot connect")
	assert.NotContains(t, body, "has no mesh")
	assert.Contains(t, body, "No DNS records yet")

	// A mesh that was read in part keeps its pill honest.
	m := ts.st.Snapshot().Meshes["sind"]
	m.Err = "dns: timeout"
	ts.st.SetMesh("sind", m)
	body = pgBody(t, ts, "/mesh?realm=sind", http.StatusOK)
	assert.Contains(t, body, `<span class="pill bad">error</span>`)
	assert.Contains(t, body, "sind-dns")

	// One record is one, not "1 records".
	m.Records = m.Records[:1]
	ts.st.SetMesh("sind", m)
	body = pgBody(t, ts, "/mesh?realm=sind", http.StatusOK)
	assert.Contains(t, body, `<span class="chip">1 record</span>`)
	assert.Contains(t, body, `data-more-count="dns-sind">1 record</span>`)
}

func TestMoreNoRealms(t *testing.T) {
	for _, tc := range []struct {
		name   string
		docker store.DockerState
		want   string
	}{
		{"unchecked", store.DockerState{}, "has not reached Docker yet"},
		{"down", store.DockerState{Checked: true, Err: "permission denied"}, "Docker is not reachable: permission denied."},
		{"empty", store.DockerState{Checked: true}, "No realm yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, _ := moreTestServer(t, false, false)
			ts.st.SetDocker(tc.docker)
			for _, page := range []string{"/mesh", "/monitoring", "/topology"} {
				body := pgBody(t, ts, page, http.StatusOK)
				assert.Contains(t, body, tc.want, page)
				assert.NotContains(t, body, "callout warn", page)
			}
		})
	}
	n := moreNoRealms(store.DockerState{Checked: true, Err: "x"})
	assert.Equal(t, "/doctor", n.Href)
	assert.Equal(t, "/clusters/new", moreNoRealms(store.DockerState{Checked: true}).Href)
	assert.Empty(t, moreNoRealms(store.DockerState{}).Href)
}

func TestMoreMonitoringPage(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	body := pgBody(t, ts, "/monitoring", http.StatusOK)
	for _, want := range []string{
		"Monitoring", "sind-grafana", "grafana/grafana:13.2.3", "http://grafana.sind.sind:3000",
		`<span class="pill ok">ready</span>`, `<span class="pill ok">running</span>`, `<span class="pill warn">starting</span>`,
		`href="http://grafana.sind.sind:3000" target="_blank" rel="noopener noreferrer"`,
		"4 services", "/clusters/sind/dev", "26.05.1", "No stack in <span class=\"mono\">ci</span>",
		"Realm <b class=\"mono\">sind</b>",
	} {
		assert.Contains(t, body, want)
	}
	assert.Equal(t, 3, strings.Count(body, "Open ↗"), "alloy has no page to open")
	assert.NotContains(t, body, "no monitoring stack")
	for _, want := range []string{
		`aria-label="Open grafana in a new tab"`,
		`172.18.255.253:3000</span> <button type="button" class="btn sm" data-more-copy-text="172.18.255.253:3000" aria-label="Copy the address of grafana">Copy</button>`,
		// The host check says host DNS is off, so the Open links fail.
		`<div class="callout warn">This host does not resolve the names under <span class="mono">.sind</span> (<a href="/doctor">Doctor: DNS policy</a>), so the Open links fail.`,
		`ssh -L 3000:172.18.255.253:3000 HOST`,
		// dev has a stopped node, and Prometheus has no numbers of it yet.
		`data-label="Nodes running">3 of 4</td>`, "No numbers yet: Prometheus has them once it has read slurmctld.",
	} {
		assert.Contains(t, body, want)
	}
	assert.Equal(t, 1, strings.Count(body, "data-more-copy-text="), "only grafana's URL has a name instead of its address")

	one := pgBody(t, ts, "/monitoring?realm=sind", http.StatusOK)
	assert.Contains(t, one, "<h2>Stack</h2>")
	assert.NotContains(t, one, "No stack in")

	empty := pgBody(t, ts, "/monitoring?realm=ci", http.StatusOK)
	assert.Contains(t, empty, "Realm <b class=\"mono\">ci</b> has no monitoring stack")
	assert.Contains(t, empty, "sind --realm ci create cluster --monitoring", "the command works in the realm of the page")
	assert.Contains(t, empty, "/clusters/new?realm=ci")
	assert.Contains(t, empty, "Slurm 26.05")

	pgBody(t, ts, "/monitoring?realm=nope", http.StatusNotFound)
}

func TestMoreMonitoringEmptyAndEdges(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	ts.st.SetMonitoring("sind", nil)
	body := pgBody(t, ts, "/monitoring", http.StatusOK)
	assert.Contains(t, body, "No realm has no monitoring stack")

	ts.st.SetMonitoring("ci", &cluster.Monitoring{Realm: "ci", Services: []cluster.MonitoringService{
		{Name: "grafana", State: cluster.ServiceMissing},
		{Name: "loki", State: "running", URL: "ftp://x"},
	}})
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "gone"})
	body = pgBody(t, ts, "/monitoring?realm=ci", http.StatusOK)
	assert.Contains(t, body, `<span class="pill bad">missing</span>`)
	assert.Contains(t, body, "No cluster of this realm is monitored")
	assert.NotContains(t, body, "Open ↗")
	assert.Contains(t, body, "Docker is not reachable: gone")
}

func TestMoreMonitoringNumbers(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	m := *ts.st.Snapshot().Monitorings["sind"]
	m.Clusters = []cluster.MonitoredCluster{
		{Name: "dev", Nodes: 4, SlurmVersion: "26.05.1", Slurm: &cluster.SlurmCounts{Nodes: 3, NodesDown: 1, NodesDrain: 1, JobsRunning: 7, JobsPending: 12}},
		{Name: "default", Nodes: 2, SlurmVersion: "26.05.1", Slurm: &cluster.SlurmCounts{Nodes: 1, NodesDown: 2, JobsRunning: 1}},
	}
	ts.st.SetMonitoring("sind", &m)
	body := pgBody(t, ts, "/monitoring?realm=sind", http.StatusOK)
	for _, want := range []string{
		"Nodes running", "Take jobs", "Jobs running", "Jobs pending",
		// A node in drain takes no jobs, as Ready for jobs counts it.
		`<td role="cell" class="num" data-label="Take jobs">1 of 3<br><span class="faint more-nowrap" title="Slurm sends no new jobs to a node that is down or in drain, which is drained or draining. A node can be both at once.">1 down, 1 in drain</span></td>`,
		`<td role="cell" class="num" data-label="Jobs running">7</td><td role="cell" class="num" data-label="Jobs pending">12</td>`,
		`<td role="cell" class="num" data-label="Nodes running">2 of 2</td>`, `data-label="Take jobs">0 of 1<br>`,
		// On a phone, each row is a card with the labels in it.
		`<table class="more-cards more-moncards" role="table">`, `<td role="cell" class="mono more-aside">26.05.1</td>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "No numbers yet")

	// Slurm's nodes all up: no line of down ones.
	m.Clusters = []cluster.MonitoredCluster{{Name: "dev", Slurm: &cluster.SlurmCounts{Nodes: 3}}}
	ts.st.SetMonitoring("sind", &m)
	body = pgBody(t, ts, "/monitoring?realm=sind", http.StatusOK)
	assert.Contains(t, body, `data-label="Take jobs">3 of 3</td>`)
}

func TestMoreMonitoringHostDNS(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	report := func(checks ...store.DoctorCheck) { ts.st.SetDoctor(&store.DoctorReport{Time: pgNow, Checks: checks}) }

	report(store.DoctorCheck{Name: "DNS policy", Status: "ok"})
	body := pgBody(t, ts, "/monitoring", http.StatusOK)
	assert.Contains(t, body, "which this host resolves. To open Grafana from another machine")
	assert.NotContains(t, body, "callout warn")

	report(store.DoctorCheck{Name: "Docker Engine", Status: "ok"})
	body = pgBody(t, ts, "/monitoring", http.StatusOK)
	assert.Contains(t, body, "which resolve only on the Docker host, with host DNS. If Open fails, use the address with its port.")

	// Without an address of Grafana there is no ssh example.
	m := *ts.st.Snapshot().Monitorings["sind"]
	m.Services = []cluster.MonitoringService{{Name: "grafana", URL: "http://grafana.sind.sind:3000", State: "running"}}
	ts.st.SetMonitoring("sind", &m)
	ts.st.SetDoctor(nil)
	body = pgBody(t, ts, "/monitoring", http.StatusOK)
	assert.Contains(t, body, "with host DNS. If Open fails")
	assert.NotContains(t, body, "ssh -L")

	assert.Empty(t, moreHostDNS(store.Snapshot{}))
}

func TestMoreServiceState(t *testing.T) {
	for _, tc := range []struct {
		state          string
		ready, checked bool
		class, text    string
	}{
		{"running", false, false, "ok", "running"},
		{"running", true, true, "ok", "ready"},
		{"running", false, true, "warn", "starting"},
		{"missing", false, false, "bad", "missing"},
		{"exited", false, false, "bad", "exited"},
		{"dead", false, false, "bad", "dead"},
		{"paused", false, false, "warn", "paused"},
		{"created", false, false, "run", "created"},
		{"removing", false, false, "", "removing"},
	} {
		class, text := moreServiceState(cluster.MonitoringService{State: dockerState(tc.state), Ready: tc.ready, Checked: tc.checked})
		assert.Equal(t, tc.class, class, tc.state)
		assert.Equal(t, tc.text, text, tc.state)
	}
}

func TestMoreMonitoringCount(t *testing.T) {
	assert.Equal(t, NavCount{}, moreMonitoringCount(store.Snapshot{}, "", time.Time{}), "no stack, no badge")
	ts, _ := moreTestServer(t, true, false)
	assert.Equal(t, NavCount{Text: "1 starting", Class: "warn", Label: "1 monitoring service is not ready yet"}, moreMonitoringCount(ts.st.Snapshot(), "", time.Time{}))
	stack := func(states ...string) store.Snapshot {
		m := &cluster.Monitoring{}
		for _, s := range states {
			m.Services = append(m.Services, cluster.MonitoringService{State: dockerState(s), Ready: true, Checked: true})
		}
		return store.Snapshot{Monitorings: map[string]*cluster.Monitoring{"sind": m}}
	}
	assert.Equal(t, NavCount{Class: "ok", Label: "2 monitoring services are ready"}, moreMonitoringCount(stack("running", "running"), "", time.Time{}))
	assert.Equal(t, NavCount{Text: "2 down", Class: "bad", Label: "2 monitoring services are down"}, moreMonitoringCount(stack("running", "exited", "missing", "paused"), "", time.Time{}))
	assert.Equal(t, "2 monitoring services are not ready yet", moreMonitoringCount(stack("paused", "created"), "", time.Time{}).Label)
	assert.Equal(t, "1 monitoring service is down", moreMonitoringCount(stack("exited"), "", time.Time{}).Label)
}

func TestMoreMonitoringInterval(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	assert.NotContains(t, pgBody(t, ts, "/monitoring", http.StatusOK), "Read every", "nothing reads the stacks")

	iv := collect.NewInterval(45 * time.Second)
	ts.srv.env.Put(ColMonitoringIntervalKey, iv)
	body := pgBody(t, ts, "/monitoring", http.StatusOK)
	for _, want := range []string{
		`action="/monitoring/interval"`, `name="csrf" value="` + ts.csrf + `"`, "Read every", `aria-labelledby="mon-interval-label"`,
		// The ids let htmx give the focus back to the button pressed.
		`<button type="submit" id="mon-interval-10s" name="interval" value="10s" aria-pressed="false">10s</button>` +
			`<button type="submit" id="mon-interval-30s" name="interval" value="30s" aria-pressed="false">30s</button>` +
			`<button type="submit" id="mon-interval-45s" name="interval" value="45s" aria-pressed="true">45s</button>` +
			`<button type="submit" id="mon-interval-1m" name="interval" value="1m" aria-pressed="false">1m</button>` +
			`<button type="submit" id="mon-interval-5m" name="interval" value="5m" aria-pressed="false">5m</button>`,
		`<span class="more-saved" role="status"><span id="mon-saved"></span></span>`,
		"How often sind ui reads the stacks", "The choice lasts until sind ui stops.", "Prometheus keeps its own scrape interval",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "Saved:", "a page that set nothing saved nothing")
	assert.NotContains(t, body, `name="interval"><option`, "buttons save one choice each, not every arrow key")

	post := func(value string, hx bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7878/monitoring/interval", strings.NewReader("interval="+value))
		r.Host = "127.0.0.1:7878"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://127.0.0.1:7878")
		r.Header.Set(csrfHeader, ts.csrf)
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		r.AddCookie(ts.cookie)
		return ts.do(r)
	}
	w := post("5m", true)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 5*time.Minute, iv.Get())
	assert.Contains(t, w.Body.String(), `id="mon-interval-5m" name="interval" value="5m" aria-pressed="true"`)
	assert.Contains(t, w.Body.String(), `<span id="mon-saved" hx-swap-oob="true">Saved: every 5m</span>`)
	assert.NotContains(t, w.Body.String(), "more-ivnote", "the note stays on the page")
	assert.NotContains(t, w.Body.String(), "45s", "a choice that is no longer current goes")
	assert.NotContains(t, w.Body.String(), "<html")

	w = post("10s", false)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/monitoring", w.Header().Get("Location"))
	assert.Equal(t, 10*time.Second, iv.Get())

	for _, bad := range []string{"45s", "1s", "x", ""} {
		assert.Equal(t, http.StatusBadRequest, post(bad, true).Code, bad)
	}
	assert.Equal(t, 10*time.Second, iv.Get())
	assert.Equal(t, http.StatusBadRequest, post(strings.Repeat("1", 5000), true).Code, "a value that is too long")

	ts.srv.env.Put(ColMonitoringIntervalKey, nil)
	assert.Equal(t, http.StatusNotFound, post("30s", true).Code)
	moreEnv.Store(nil)
	assert.Nil(t, moreEnvGet[moreInterval](ColMonitoringIntervalKey))
}

func TestMoreDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "10s", time.Minute: "1m", 90 * time.Second: "1m30s", time.Hour: "1h",
		90 * time.Minute: "1h30m", time.Hour + time.Second: "1h0m1s",
	} {
		assert.Equal(t, want, moreDuration(d), d)
	}
}

func TestMoreTopologyPage(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	body := pgBody(t, ts, "/topology", http.StatusOK)
	for _, want := range []string{
		"Topology", `<svg viewBox="0 0 900 `, `role="group"`, "Diagram of realm sind", "MESH SIND-MESH", "CLUSTER NETWORKS", "HOST", "NODES",
		"this host", "Docker Engine 28.5.1", "sind-dns", "DNS · 172.18.255.254", "SSH relay", "sind-grafana", "grafana · 172.18.255.253",
		"kind-control-plane", "connected container", `href="/mesh?realm=sind"`, `href="/monitoring?realm=sind"`,
		"sind-default-net", "172.19.0.0/16 · running", "sind-dev-net", "172.20.0.0/16 · mixed", "sind-empty-net", "empty",
		`href="/nodes/sind/dev/worker-1"`, "worker-1.dev", "172.19.0.4 · worker · stopped", "172.19.0.2 · controller · running",
		`aria-label="worker-1.dev · 172.19.0.4 · worker · stopped"`,
		`href="/clusters/sind/dev"`, `class="n bad off"`, `class="n bad"`,
		`class="e mesh"`, `class="e net"`, "Realm <b class=\"mono\">ci</b>", "MESH</text>",
		"the mesh network with 7 containers, and 3 cluster networks with the nodes", "1 cluster network with the nodes",
		`<div class="pb more-topo" data-more-scroll="Diagram that scrolls sideways">`, "Scroll sideways to see the nodes",
		`<a href="/topology" class="on" aria-current="true">Diagram</a><a href="/topology?view=table">Table</a>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, `role="img"`, "an image hides its links")
	assert.NotContains(t, body, `tabindex="0"`, "only a diagram that scrolls is a stop of the Tab key, which the script knows")
	assert.NotContains(t, body, "<script>")
	assert.NotContains(t, body, "style=")

	// The table lists the same things with the same links.
	table := pgBody(t, ts, "/topology?view=table", http.StatusOK)
	for _, want := range []string{
		`<table class="more-topotable more-cards" role="table">`, `<th scope="colgroup" colspan="4" role="rowheader">Host and mesh</th>`,
		`<a href="/doctor">this host</a>`, "Docker Engine 28.5.1",
		`<a href="/mesh?realm=sind">sind-dns</a></td><td role="cell">DNS</td><td role="cell" class="mono">172.18.255.254</td>`,
		`<a href="/monitoring?realm=sind">sind-grafana</a></td><td role="cell">grafana</td><td role="cell" class="mono">172.18.255.253</td><td role="cell" class="more-aside"><span class="pill ok">ready</span>`,
		`<a href="/clusters/sind/dev" class="mono">sind-dev-net</a>`, `<span class="mono">172.20.0.0/16</span>`, `<span class="pill warn">mixed</span>`,
		`<a href="/nodes/sind/dev/worker-1">worker-1.dev</a></td><td role="cell">worker</td><td role="cell" class="mono">172.19.0.4</td><td role="cell" class="more-aside"><span class="pill bad">stopped</span>`,
		"No nodes.", `<a href="/topology">Diagram</a><a href="/topology?view=table" class="on" aria-current="true">Table</a>`,
	} {
		assert.Contains(t, table, want)
	}
	assert.NotContains(t, table, "<svg viewBox")
	assert.Contains(t, pgBody(t, ts, "/topology?view=table&realm=ci", http.StatusOK), `href="/topology?realm=ci"`)

	pgBody(t, ts, "/topology?realm=nope", http.StatusNotFound)
	pgBody(t, ts, "/topology?view=pie", http.StatusBadRequest)
}

func TestMoreTopologyEdges(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	// A realm with nothing to draw says so.
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "sind"}, {Name: "bare"}, {Name: "ci"}})
	body := pgBody(t, ts, "/topology", http.StatusOK)
	assert.Contains(t, body, `Nothing to draw for <span class="mono">bare</span>`)

	// No Docker Engine check: the host line says only Docker. A long name is clipped.
	ts.st.SetDoctor(nil)
	ts.st.SetMesh("ci", store.Mesh{Err: "unread"})
	ts.st.SetNodes("ci", moreNodes("ci", "pr-145", 0, -1))
	ts.st.SetClusters("ci", []cluster.Summary{{Name: "pr-145", State: cluster.StateStopped}})
	body = pgBody(t, ts, "/topology?realm=ci", http.StatusOK)
	assert.Contains(t, body, ">Docker</text>")
	assert.Contains(t, body, "ci-pr-145-net")
	assert.Contains(t, body, ">stopped</text>")

	ts.st.SetDocker(store.DockerState{Checked: true, Err: "gone"})
	assert.Contains(t, pgBody(t, ts, "/topology", http.StatusOK), "Docker is not reachable: gone")
}

func TestMoreTopologyLayout(t *testing.T) {
	st := store.New()
	moreFixture(t, st)
	topo, ok := moreTopologyOf(st.Snapshot(), "sind")
	require.True(t, ok)
	assert.Equal(t, 900, topo.Width)
	assert.GreaterOrEqual(t, topo.Height, topoMinH)
	assert.Len(t, topo.Heads, 4)
	for _, b := range topo.Boxes {
		assert.GreaterOrEqual(t, b.X, 0)
		assert.LessOrEqual(t, b.X+b.W, topo.Width, b.Label)
		assert.LessOrEqual(t, b.Y+b.H, topo.Height, b.Label)
	}
	// Every edge ends at a box: one per mesh item from the host, one per
	// cluster from DNS, one per node from its cluster network.
	assert.Len(t, topo.Edges, 7+3+6, "host edges, cluster edges and node edges")

	_, ok = moreTopologyOf(st.Snapshot(), "unknown")
	assert.False(t, ok)

	assert.Equal(t, "abc", moreClip("abc", 5))
	assert.Equal(t, "ab…", moreClip("abcdef", 3))
	assert.Equal(t, 84, moreBox(2, 100, "x", "y", "", "").Y, "box starts 16 above its line")
	assert.Equal(t, 10+topoX[3], moreBox(3, 0, "x", "", "", "").TextX())
}

func TestMoreDoctorPage(t *testing.T) {
	ts, calls := moreTestServer(t, true, true)
	body := pgBody(t, ts, "/doctor", http.StatusOK)
	for _, want := range []string{
		"Doctor", "Docker Engine 28.5.1", "cgroup v2", "1 failed", "1 warning", "checked 3 min ago",
		`<span class="more-mark ok"`, `<span class="more-mark warn"`, `<span class="more-mark bad"`, `<span class="more-mark "`, ">?</span>",
		`<span class="pill warn nodot">warning</span>`, `<span class="pill bad nodot">failed</span>`, `<span class="pill ok nodot">ok</span>`,
		`<p class="more-fixtext">Raise the limit now:</p>`, `<code>sudo sysctl fs.inotify.max_user_instances=1024</code><button type="button" data-more-copy aria-label="Copy sudo sysctl fs.inotify.max_user_instances=1024">Copy</button>`,
		"Check again", `action="/doctor/check"`, `name="csrf" value="` + ts.csrf + `"`,
		`<div class="callout more-verdict bad" role="status"><b>Not ready: sind cannot create clusters on this host.</b> Fix the failed check first.`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "<code>Raise", "Copy takes the command, not the sentence before it")
	assert.NotContains(t, body, "not reachable")
	assert.NotContains(t, body, "disabled>Check again")
	assert.Zero(t, *calls)

	ts.st.SetDoctor(&store.DoctorReport{Time: pgNow, Running: true, Checks: []store.DoctorCheck{{Name: "Docker Engine", Status: "ok"}, {Name: "x", Status: "warning"}, {Name: "y", Status: "warning"}}})
	body = pgBody(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, `pill run">checking`)
	assert.Contains(t, body, "disabled>Check again")
	assert.Contains(t, body, "just now")

	ts.st.SetDoctor(&store.DoctorReport{Time: pgNow, Checks: []store.DoctorCheck{{Name: "Docker Engine", Status: "ok"}, {Name: "x", Status: "warning"}, {Name: "y", Status: "warning"}}})
	body = pgBody(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "2 warnings")
	assert.Contains(t, body, "<b>Ready: sind can create clusters on this host.</b> The 2 warnings do not stop sind")

	ts.st.SetDoctor(&store.DoctorReport{Time: pgNow, Checks: []store.DoctorCheck{{Name: "Docker Engine", Status: "ok"}}})
	body = pgBody(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "all passed")
	assert.Contains(t, body, `more-verdict ok" role="status"><b>Ready: sind can create clusters on this host.</b> Every check passed.`)

	// A multi-line command has no prompt.
	ts.st.SetDoctor(&store.DoctorReport{Time: pgNow, Checks: []store.DoctorCheck{{Name: "Docker host", Status: "warning",
		Remediation: "Point the docker CLI here:\n\nunset DOCKER_HOST\ndocker context use default"}}})
	assert.Contains(t, pgBody(t, ts, "/doctor", http.StatusOK), "<code class=\"nop\">unset DOCKER_HOST\ndocker context use default</code>")
}

func TestMoreFixSteps(t *testing.T) {
	assert.Nil(t, moreFixSteps(""))
	assert.Equal(t, []moreFixStep{
		{Text: []string{"Raise the limit now:"}, Cmd: "sudo sysctl fs.inotify.max_user_instances=1024"},
		{Text: []string{"Keep it across reboots:"}, Cmd: "echo fs.inotify.max_user_instances=1024 | sudo tee /etc/sysctl.d/99-sind.conf"},
	}, moreFixSteps("Raise the limit now:\r\n\r\n  sudo sysctl fs.inotify.max_user_instances=1024  \n\n\nKeep it across reboots:\n\n"+
		"echo fs.inotify.max_user_instances=1024 | sudo tee /etc/sysctl.d/99-sind.conf\n"))
	// Sentences that start in lower case end like sentences; a heredoc
	// keeps its indent; words with no command after them are a step too.
	assert.Equal(t, []moreFixStep{
		{Text: []string{"sind starts its nodes with options that Docker refuses here.\nRemove them and restart the daemon:"},
			Cmd: "sudo tee /x <<'R'\n  rule\nR"},
		{Text: []string{"Install Docker Engine 28 or later: https://docs.docker.com/engine/install/", "Then log in again."}},
	}, moreFixSteps("sind starts its nodes with options that Docker refuses here.\nRemove them and restart the daemon:\n\n"+
		"sudo tee /x <<'R'\n  rule\nR\n\nInstall Docker Engine 28 or later: https://docs.docker.com/engine/install/\n\nThen log in again."))
	assert.Equal(t, "a\n\n\tb", moreDedent("\t\ta\n\n\t\t\tb\n"))
	assert.Equal(t, "", moreDedent(" \n\t"))
	assert.False(t, moreFixStep{Cmd: "a"}.Multi())
}

func TestMoreDoctorVerdict(t *testing.T) {
	for _, tc := range []struct {
		d           moreDoctorData
		class, want string
	}{
		{moreDoctorData{Running: true}, "run", "Checking this host"},
		{moreDoctorData{CanRecheck: true}, "", "Press Check again"},
		{moreDoctorData{}, "", "Run sind doctor in a terminal"},
		{moreDoctorData{HasReport: true, Failures: 1, Warnings: 3}, "bad", "Fix the failed check first. Create and power on stop until it passes."},
		{moreDoctorData{HasReport: true, Failures: 2}, "bad", "Fix the 2 failed checks first. Create and power on stop until they pass."},
		{moreDoctorData{HasReport: true, Warnings: 1}, "ok", "The warning does not stop sind, but read its fix."},
		{moreDoctorData{HasReport: true, Warnings: 2}, "ok", "The 2 warnings do not stop sind, but read their fixes."},
		{moreDoctorData{HasReport: true, Running: true}, "ok", "Every check passed."},
	} {
		v := moreDoctorVerdict(tc.d)
		assert.Equal(t, tc.class, v.Class, tc.want)
		assert.Contains(t, v.Title+" "+v.Text, tc.want)
	}
}

func TestMoreDoctorEmptyStates(t *testing.T) {
	ts, _ := moreTestServer(t, false, true)
	body := pgBody(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "not run yet")
	assert.Contains(t, body, "No check has run yet. Press Check again to run them.")

	ts.st.SetDoctor(&store.DoctorReport{Running: true})
	body = pgBody(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "The first check is running.")
	assert.NotContains(t, body, "checked ")

	ts.st.SetDoctor(nil)
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "Cannot connect to the Docker daemon"})
	body = pgBody(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "Docker is not reachable:")
	assert.Contains(t, body, "Cannot connect to the Docker daemon")

	// Without a service that runs the checks the button is not there.
	ts2, _ := moreTestServer(t, false, false)
	body = pgBody(t, ts2, "/doctor", http.StatusOK)
	assert.NotContains(t, body, "Check again")
	assert.NotContains(t, body, "/doctor/check")
	assert.Contains(t, body, "No check has run yet.")
	assert.NotContains(t, body, "Press Check again")
	moreEnv.Store(nil)
	assert.Nil(t, moreEnvGet[func()](ColDoctorKey))
}

func TestMoreDoctorCheck(t *testing.T) {
	ts, calls := moreTestServer(t, true, true)
	post := func(hx bool) *httptest.ResponseRecorder {
		r := request("POST", "/doctor/check")
		r.Header.Set("Origin", "http://127.0.0.1:7878")
		r.Header.Set(csrfHeader, ts.csrf)
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		r.AddCookie(ts.cookie)
		return ts.do(r)
	}
	w := post(true)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, *calls)
	assert.Contains(t, w.Body.String(), "inotify")
	assert.NotContains(t, w.Body.String(), "<html")

	w = post(false)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/doctor", w.Header().Get("Location"))
	assert.Equal(t, 2, *calls)

	// Without the key nothing runs.
	ts2, calls2 := moreTestServer(t, true, false)
	r := request("POST", "/doctor/check")
	r.Header.Set("Origin", "http://127.0.0.1:7878")
	r.Header.Set(csrfHeader, ts2.csrf)
	r.AddCookie(ts2.cookie)
	assert.Equal(t, http.StatusNotFound, ts2.do(r).Code)
	assert.Zero(t, *calls2)

	// A request without the CSRF token is refused before the route runs.
	r = request("POST", "/doctor/check")
	r.Header.Set("Origin", "http://127.0.0.1:7878")
	r.AddCookie(ts.cookie)
	assert.Equal(t, http.StatusForbidden, ts.do(r).Code)
	assert.Equal(t, 2, *calls)
}

func TestMoreDoctorCount(t *testing.T) {
	assert.Equal(t, NavCount{}, moreDoctorCount(store.Snapshot{}, "", time.Time{}))
	ok := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "ok"}}}
	assert.Equal(t, NavCount{Class: "ok", Label: "every host check passed"}, moreDoctorCount(store.Snapshot{Doctor: ok}, "", time.Time{}))
	warn := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "ok"}, {Status: "warning"}}}
	assert.Equal(t, NavCount{Text: "1 warning", Class: "warn", Label: "1 warning"}, moreDoctorCount(store.Snapshot{Doctor: warn}, "", time.Time{}))
	bad := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "warning"}, {Status: "failed"}, {Status: "warning"}}}
	assert.Equal(t, NavCount{Text: "1 failed", Class: "bad", Label: "1 host check failed, 2 warnings"}, moreDoctorCount(store.Snapshot{Doctor: bad}, "", time.Time{}))
	worse := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "failed"}, {Status: "failed"}}}
	assert.Equal(t, NavCount{Text: "2 failed", Class: "bad", Label: "2 host checks failed"}, moreDoctorCount(store.Snapshot{Doctor: worse}, "", time.Time{}))

	// The rail of any page shows both badges, with their meaning in words.
	ts, _ := moreTestServer(t, true, false)
	body := pgBody(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, `<span class="n bad" title="1 host check failed, 2 warnings"><span aria-hidden="true">1 failed</span><span class="vh">, 1 host check failed, 2 warnings</span></span>`)
	assert.Contains(t, body, `<span class="n warn" title="1 monitoring service is not ready yet"><span aria-hidden="true">1 starting</span>`)
}

func TestMoreFragmentsAreLive(t *testing.T) {
	reg := defaultRegistry
	for name, topic := range map[string]store.Topic{
		"more-mesh":       store.MeshTopic("sind"),
		"more-monitoring": store.MonitoringTopic("sind"),
		"more-topology":   store.NodesTopic("sind"),
		"more-doctor":     store.TopicDoctor,
	} {
		f, ok := reg.fragments[name]
		require.True(t, ok, name)
		assert.True(t, f.depends([]store.Topic{topic}, nil), name)
		assert.True(t, f.depends([]store.Topic{store.TopicDocker}, nil), name)
		assert.False(t, f.depends([]store.Topic{store.TopicTasks}, nil), name)
	}
	f := reg.fragments["more-topology"]
	for _, topic := range []store.Topic{store.ClustersTopic("a"), store.NetworksTopic("a"), store.MeshTopic("a"), store.MonitoringTopic("a")} {
		assert.True(t, f.depends([]store.Topic{topic}, nil), string(topic))
	}
	// A page names its fragment and the realm it reads.
	for _, p := range reg.pages {
		if strings.HasPrefix(p.Name, "more-") {
			assert.Equal(t, []string{p.Name}, p.Fragments)
		}
	}
}

func TestMoreServiceSetup(t *testing.T) {
	t.Cleanup(func() { moreEnv.Store(nil) })
	var setup func(*Env) error
	for _, s := range defaultRegistry.services {
		if s.Name == "more" {
			setup = s.Setup
		}
	}
	require.NotNil(t, setup)
	env := &Env{}
	env.Put(ColDoctorKey, func() {})
	require.NoError(t, setup(env))
	assert.NotNil(t, moreEnvGet[func()](ColDoctorKey))
}

func TestMoreHelpers(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		time.Second: "just now", 30 * time.Second: "30 s ago", 5 * time.Minute: "5 min ago",
		3 * time.Hour: "3 h ago", 50 * time.Hour: "2 d ago",
	} {
		assert.Equal(t, want, moreAgo(now, now.Add(-d)))
	}
	assert.Equal(t, "", moreDockerDown(store.DockerState{Err: "unchecked"}))
	assert.Equal(t, "boom", moreDockerDown(store.DockerState{Checked: true, Err: "boom"}))
	cls, mark := moreCheckMark("failed")
	assert.Equal(t, "bad", cls)
	assert.Equal(t, "✗", mark)
}

func TestMoreListPagesReadTheRealmCookie(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	for _, path := range []string{"/mesh", "/monitoring", "/topology"} {
		r := request("GET", path)
		r.AddCookie(ts.cookie)
		r.AddCookie(&http.Cookie{Name: "sind_realm", Value: "ci"})
		w := ts.do(r)
		require.Equal(t, http.StatusFound, w.Code, path)
		assert.Equal(t, path+"?realm=ci", w.Header().Get("Location"), path)
	}
}

func TestPgOutsides(t *testing.T) {
	sn := store.Snapshot{
		Realms: []cluster.RealmSummary{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		Meshes: map[string]store.Mesh{
			"a": {Connectable: []store.Outside{{Name: "kind", Image: "kindest/node", State: "running", Status: "Up About an hour", Networks: []string{"kind"}}}},
			"b": {External: []string{"kind"}},
			"c": {External: []string{"kind"}},
		},
	}
	assert.Equal(t, []pgOutside{{
		Name: "kind", Image: "kindest/node", Networks: "network kind", Up: "up about an hour", State: "running",
		Also: "also in realms b and c", ConnectURL: "/dialogs/connect?container=kind&realm=a",
	}}, pgOutsides(sn, "a"))
	assert.Nil(t, pgOutsides(sn, "b"))

	// The monitoring stack of another realm is sind's, though the docker ps
	// of this realm does not tell it apart.
	sn.Meshes["b"] = store.Mesh{Connectable: []store.Outside{{Name: "a-mesh-grafana-1", State: "running"}}}
	sn.Monitorings = map[string]*cluster.Monitoring{"a": {Services: []cluster.MonitoringService{{Container: "a-mesh-grafana-1"}}}}
	assert.Nil(t, pgOutsides(sn, "b"))
}
