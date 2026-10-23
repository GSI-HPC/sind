// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/collect"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moreGet fetches a page and returns its body, after checking the status.
func moreGet(t *testing.T, ts *testServer, target string, status int) string {
	t.Helper()
	w := ts.get(target)
	require.Equal(t, status, w.Code, target)
	return w.Body.String()
}

func TestMoreMeshPage(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	body := moreGet(t, ts, "/mesh", http.StatusOK)
	for _, want := range []string{
		"Mesh and DNS", "Realm <b class=\"mono\">sind</b>", "Realm <b class=\"mono\">ci</b>",
		"sind-mesh", "172.18.0.0/16", "sind-dns", "172.18.255.254", "sind.sind", "sind-ssh-config", "2 records",
		"worker-0.default.sind.sind", "kind-control-plane",
		"/dialogs/connect?realm=sind", "/dialogs/delete-realm?realm=ci",
		"/dialogs/disconnect?nodes=kind-control-plane&amp;realm=sind",
		"Realm ci has no mesh", "No other containers", `sse-swap="more-mesh"`, `href="/static/`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "not reachable")

	one := moreGet(t, ts, "/mesh?realm=sind", http.StatusOK)
	assert.NotContains(t, one, "Realm <b")
	assert.NotContains(t, one, "has no mesh")

	body = moreGet(t, ts, "/mesh?realm=nope", http.StatusNotFound)
	assert.Contains(t, body, "There is no realm nope.")
	assert.Contains(t, body, "with its last cluster")
	assert.Contains(t, body, `<a class="btn primary" href="/mesh?realm=">All realms</a>`)
	body = moreGet(t, ts, "/mesh?realm=sindd", http.StatusNotFound)
	assert.Contains(t, body, `<a class="btn primary" href="/mesh?realm=sind">Realm sind</a>`)
}

func TestMoreMeshStates(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	ts.st.SetMesh("ci", store.Mesh{Err: "docker inspect failed"})
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "cannot connect"})
	body := moreGet(t, ts, "/mesh?realm=ci", http.StatusOK)
	assert.Contains(t, body, "could not be read: docker inspect failed")
	assert.Contains(t, body, "Docker is not reachable: cannot connect")
	assert.NotContains(t, body, "has no mesh")
	assert.Contains(t, body, "No DNS records yet")

	// A mesh that was read in part keeps its pill honest.
	m, _ := ts.st.Snapshot().MeshOf("sind")
	m.Err = "dns: timeout"
	ts.st.SetMesh("sind", m)
	body = moreGet(t, ts, "/mesh?realm=sind", http.StatusOK)
	assert.Contains(t, body, `<span class="pill bad">error</span>`)
	assert.Contains(t, body, "sind-dns")
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
				body := moreGet(t, ts, page, http.StatusOK)
				assert.Contains(t, body, tc.want, page)
				assert.NotContains(t, body, "callout warn", page)
			}
		})
	}
	n := moreNoRealms(store.DockerState{Checked: true, Err: "x"})
	assert.Equal(t, "/doctor", n.Href)
	assert.Equal(t, "/dialogs/create-cluster", moreNoRealms(store.DockerState{Checked: true}).Href)
	assert.Empty(t, moreNoRealms(store.DockerState{}).Href)
}

func TestMoreMonitoringPage(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	body := moreGet(t, ts, "/monitoring", http.StatusOK)
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

	one := moreGet(t, ts, "/monitoring?realm=sind", http.StatusOK)
	assert.Contains(t, one, "<h2>Stack</h2>")
	assert.NotContains(t, one, "No stack in")

	empty := moreGet(t, ts, "/monitoring?realm=ci", http.StatusOK)
	assert.Contains(t, empty, "Realm <b class=\"mono\">ci</b> has no monitoring stack")
	assert.Contains(t, empty, "sind create cluster --monitoring")
	assert.Contains(t, empty, "/dialogs/create-cluster?realm=ci")
	assert.Contains(t, empty, "Slurm 26.05")

	moreGet(t, ts, "/monitoring?realm=nope", http.StatusNotFound)
}

func TestMoreMonitoringEmptyAndEdges(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	ts.st.SetMonitoring("sind", nil)
	body := moreGet(t, ts, "/monitoring", http.StatusOK)
	assert.Contains(t, body, "No realm has no monitoring stack")

	ts.st.SetMonitoring("ci", &cluster.Monitoring{Realm: "ci", Services: []cluster.MonitoringService{
		{Name: "grafana", State: cluster.ServiceMissing},
		{Name: "loki", State: "running", URL: "ftp://x"},
	}})
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "gone"})
	body = moreGet(t, ts, "/monitoring?realm=ci", http.StatusOK)
	assert.Contains(t, body, `<span class="pill bad">missing</span>`)
	assert.Contains(t, body, "No cluster of this realm is monitored")
	assert.NotContains(t, body, "Open ↗")
	assert.Contains(t, body, "Docker is not reachable: gone")
}

func TestMoreServiceState(t *testing.T) {
	for _, tc := range []struct {
		state            string
		ready, checked   bool
		class, text      string
		wantUnknownState bool
	}{
		{"running", false, false, "ok", "running", false},
		{"running", true, true, "ok", "ready", false},
		{"running", false, true, "warn", "starting", false},
		{"missing", false, false, "bad", "missing", false},
		{"exited", false, false, "bad", "exited", false},
		{"dead", false, false, "bad", "dead", false},
		{"paused", false, false, "warn", "paused", false},
		{"created", false, false, "run", "created", false},
		{"removing", false, false, "", "removing", true},
	} {
		class, text := moreServiceState(cluster.MonitoringService{State: dockerState(tc.state), Ready: tc.ready, Checked: tc.checked})
		assert.Equal(t, tc.class, class, tc.state)
		assert.Equal(t, tc.text, text, tc.state)
	}
}

func TestMoreMonitoringCount(t *testing.T) {
	assert.Equal(t, NavCount{}, moreMonitoringCount(store.Snapshot{}), "no stack, no badge")
	ts, _ := moreTestServer(t, true, false)
	assert.Equal(t, NavCount{Text: "1 starting", Class: "warn", Label: "1 monitoring service is not ready yet"}, moreMonitoringCount(ts.st.Snapshot()))
	stack := func(states ...string) store.Snapshot {
		m := &cluster.Monitoring{}
		for _, s := range states {
			m.Services = append(m.Services, cluster.MonitoringService{State: dockerState(s), Ready: true, Checked: true})
		}
		return store.Snapshot{Monitorings: map[string]*cluster.Monitoring{"sind": m}}
	}
	assert.Equal(t, NavCount{Class: "ok", Label: "2 monitoring services are ready"}, moreMonitoringCount(stack("running", "running")))
	assert.Equal(t, NavCount{Text: "2 down", Class: "bad", Label: "2 monitoring services are down"}, moreMonitoringCount(stack("running", "exited", "missing", "paused")))
	assert.Equal(t, "2 monitoring services are not ready yet", moreMonitoringCount(stack("paused", "created")).Label)
	assert.Equal(t, "1 monitoring service is down", moreMonitoringCount(stack("exited")).Label)
}

func TestMoreMonitoringInterval(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	assert.NotContains(t, moreGet(t, ts, "/monitoring", http.StatusOK), "read every", "nothing reads the stacks")

	iv := collect.NewInterval(45 * time.Second)
	ts.srv.Env().Put(moreIntervalKey, iv)
	body := moreGet(t, ts, "/monitoring", http.StatusOK)
	for _, want := range []string{
		`action="/monitoring/interval"`, `name="csrf" value="` + ts.csrf + `"`, "read every",
		`<option value="10s">10s</option><option value="30s">30s</option><option value="45s" selected>45s</option><option value="1m">1m</option><option value="5m">5m</option></select>`,
	} {
		assert.Contains(t, body, want)
	}

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
	assert.Contains(t, w.Body.String(), `<option value="5m" selected>`)
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

	ts.srv.Env().Put(moreIntervalKey, nil)
	assert.Equal(t, http.StatusNotFound, post("30s", true).Code)
	moreEnv.Store(nil)
	assert.Nil(t, moreIntervalOf())
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
	body := moreGet(t, ts, "/topology", http.StatusOK)
	for _, want := range []string{
		"Topology", `<svg viewBox="0 0 900 `, `role="img"`, "Topology of realm sind", "MESH SIND-MESH", "CLUSTER NETWORKS", "HOST", "NODES",
		"this host", "Docker Engine 28.5.1", "sind-dns", "DNS · 172.18.255.254", "SSH relay", "sind-grafana", "kind-control-plane", "connected container",
		"sind-default-net", "172.19.0.0/16 · running", "sind-dev-net", "172.20.0.0/16 · mixed", "sind-empty-net", "empty",
		`href="/nodes/sind/dev/worker-1"`, "worker-1.dev", `href="/clusters/sind/dev"`, `class="n bad off"`, `class="n bad"`,
		`class="e mesh"`, `class="e net"`, "Realm <b class=\"mono\">ci</b>", "MESH</text>",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "<script>")
	assert.NotContains(t, body, "style=")

	moreGet(t, ts, "/topology?realm=nope", http.StatusNotFound)
}

func TestMoreTopologyEdges(t *testing.T) {
	ts, _ := moreTestServer(t, true, false)
	// A realm with nothing to draw says so.
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "sind"}, {Name: "bare"}, {Name: "ci"}})
	body := moreGet(t, ts, "/topology", http.StatusOK)
	assert.Contains(t, body, `Nothing to draw for <span class="mono">bare</span>`)

	// No Docker Engine check: the host line says only Docker. A long name is clipped.
	ts.st.SetDoctor(nil)
	ts.st.SetMesh("ci", store.Mesh{Err: "unread"})
	ts.st.SetNodes("ci", moreNodes("ci", "pr-145", 0, -1))
	ts.st.SetClusters("ci", []cluster.Summary{{Name: "pr-145", State: cluster.StateStopped}})
	body = moreGet(t, ts, "/topology?realm=ci", http.StatusOK)
	assert.Contains(t, body, ">Docker</text>")
	assert.Contains(t, body, "ci-pr-145-net")
	assert.Contains(t, body, ">stopped</text>")

	ts.st.SetDocker(store.DockerState{Checked: true, Err: "gone"})
	assert.Contains(t, moreGet(t, ts, "/topology", http.StatusOK), "Docker is not reachable: gone")
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
	body := moreGet(t, ts, "/doctor", http.StatusOK)
	for _, want := range []string{
		"Doctor", "Docker Engine 28.5.1", "cgroup v2", "1 failed", "1 warning", "checked 3 min ago",
		`<span class="more-mark ok"`, `<span class="more-mark warn"`, `<span class="more-mark bad"`, `<span class="more-mark "`, ">?</span>",
		`<span class="pill warn nodot">warning</span>`, `<span class="pill bad nodot">failed</span>`, `<span class="pill ok nodot">ok</span>`,
		"sudo sysctl fs.inotify.max_user_instances=1024", "Raise the limit now:", "data-more-copy", `<code class="nop">`,
		"Check again", `action="/doctor/check"`, `name="csrf" value="` + ts.csrf + `"`, "A failed check stops create",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "not reachable")
	assert.NotContains(t, body, "disabled>Check again")
	assert.Zero(t, *calls)

	ts.st.SetDoctor(&store.DoctorReport{Time: moreNow, Running: true, Checks: []store.DoctorCheck{{Name: "Docker Engine", Status: "ok"}, {Name: "x", Status: "warning"}, {Name: "y", Status: "warning"}}})
	body = moreGet(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, `pill run">checking`)
	assert.Contains(t, body, "disabled>Check again")
	assert.Contains(t, body, "just now")

	ts.st.SetDoctor(&store.DoctorReport{Time: moreNow, Checks: []store.DoctorCheck{{Name: "Docker Engine", Status: "ok"}, {Name: "x", Status: "warning"}, {Name: "y", Status: "warning"}}})
	assert.Contains(t, moreGet(t, ts, "/doctor", http.StatusOK), "2 warnings")

	ts.st.SetDoctor(&store.DoctorReport{Time: moreNow, Checks: []store.DoctorCheck{{Name: "Docker Engine", Status: "ok"}}})
	assert.Contains(t, moreGet(t, ts, "/doctor", http.StatusOK), "all passed")
}

func TestMoreDoctorEmptyStates(t *testing.T) {
	ts, _ := moreTestServer(t, false, true)
	body := moreGet(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "not run yet")
	assert.Contains(t, body, "No check has run yet. Press Check again to run them.")

	ts.st.SetDoctor(&store.DoctorReport{Running: true})
	body = moreGet(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "The first check is running.")
	assert.NotContains(t, body, "checked ")

	ts.st.SetDoctor(nil)
	ts.st.SetDocker(store.DockerState{Checked: true, Err: "Cannot connect to the Docker daemon"})
	body = moreGet(t, ts, "/doctor", http.StatusOK)
	assert.Contains(t, body, "Docker is not reachable:")
	assert.Contains(t, body, "Cannot connect to the Docker daemon")

	// Without a service that runs the checks the button is not there.
	ts2, _ := moreTestServer(t, false, false)
	body = moreGet(t, ts2, "/doctor", http.StatusOK)
	assert.NotContains(t, body, "Check again")
	assert.NotContains(t, body, "/doctor/check")
	assert.Contains(t, body, "No check has run yet.")
	assert.NotContains(t, body, "Press Check again")
	moreEnv.Store(nil)
	assert.Nil(t, moreDoctorFunc())
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
	assert.Equal(t, NavCount{}, moreDoctorCount(store.Snapshot{}))
	ok := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "ok"}}}
	assert.Equal(t, NavCount{Class: "ok", Label: "every host check passed"}, moreDoctorCount(store.Snapshot{Doctor: ok}))
	warn := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "ok"}, {Status: "warning"}}}
	assert.Equal(t, NavCount{Text: "1 warning", Class: "warn", Label: "1 warning"}, moreDoctorCount(store.Snapshot{Doctor: warn}))
	bad := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "warning"}, {Status: "failed"}, {Status: "warning"}}}
	assert.Equal(t, NavCount{Text: "1 failed", Class: "bad", Label: "1 host check failed, 2 warnings"}, moreDoctorCount(store.Snapshot{Doctor: bad}))
	worse := &store.DoctorReport{Checks: []store.DoctorCheck{{Status: "failed"}, {Status: "failed"}}}
	assert.Equal(t, NavCount{Text: "2 failed", Class: "bad", Label: "2 host checks failed"}, moreDoctorCount(store.Snapshot{Doctor: worse}))

	// The rail of any page shows both badges, with their meaning in words.
	ts, _ := moreTestServer(t, true, false)
	body := moreGet(t, ts, "/doctor", http.StatusOK)
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
	env.Put(moreDoctorKey, func() {})
	require.NoError(t, setup(env))
	assert.NotNil(t, moreDoctorFunc())
}

func TestMoreHelpers(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		time.Second: "just now", 30 * time.Second: "30 s ago", 5 * time.Minute: "5 min ago",
		3 * time.Hour: "3 h ago", 50 * time.Hour: "2 d ago",
	} {
		assert.Equal(t, want, moreAgo(now, now.Add(-d)))
	}
	assert.Equal(t, "/dialogs/connect?nodes=a+b&realm=r", moreDialogURL("connect", "realm", "r", "nodes", "a b"))
	assert.Equal(t, "/dialogs/x?", moreDialogURL("x"))
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
