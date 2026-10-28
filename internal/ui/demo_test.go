// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/ui/demo"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// demoPages are the pages of the demo, with the dialogs, the help and the
// run of a still demo that goes on.
var demoPages = []string{
	"/", "/clusters", "/clusters/sind/dev", "/clusters/sind/dev?tab=nodes", "/clusters/sind/dev?tab=logs",
	"/clusters/sind/dev?tab=network", "/clusters/sind/dev?tab=connect", "/clusters/sind/legacy", "/clusters/ci/pr-145",
	"/nodes", "/nodes/sind/dev/worker-4", "/mesh", "/monitoring", "/topology", "/doctor",
	"/activity", "/runs/d3e0000000000002", "/runs/d3e0000000000005", "/runs/d3e0000000000006",
	"/clusters/new", "/dialogs/delete-cluster?realm=sind&cluster=legacy", "/dialogs/power?realm=sind&cluster=dev&op=reboot",
	"/help?topic=/clusters/sind/dev", "/help?topic=word/mesh",
}

// stillPages are the pages that a still demo repeats: those of the demo and
// the run that goes on.
var stillPages = append([]string{"/runs/d3e0000000000063", "/tasks/7a5c0000000000a1"}, demoPages...)

// demoSession is a browser that has opened the URL of a demo.
type demoSession struct {
	t    *testing.T
	c    *http.Client
	base string
	csrf string
}

var csrfMeta = regexp.MustCompile(`"X-Sind-CSRF":"([^"]*)"`)

func openDemo(t *testing.T, r *running) *demoSession {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	s := &demoSession{t: t, c: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
	s.base, _, _ = strings.Cut(r.url, "/?")
	_, body := s.get(r.url)
	m := csrfMeta.FindStringSubmatch(body)
	require.NotNil(t, m, body)
	s.csrf = m[1]
	return s
}

func (s *demoSession) get(target string) (int, string) {
	s.t.Helper()
	resp, err := s.c.Get(target)
	require.NoError(s.t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err)
	return resp.StatusCode, string(body)
}

func (s *demoSession) post(path string, form url.Values) (*http.Response, string) {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.base+path, strings.NewReader(form.Encode()))
	require.NoError(s.t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", s.base)
	req.Header.Set(csrfHeader, s.csrf)
	req.Header.Set("HX-Request", "true")
	resp, err := s.c.Do(req)
	require.NoError(s.t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err)
	return resp, string(body)
}

// capture is a service that hands out the Env of a run.
func capture(got chan<- *Env) Service {
	return Service{Name: "zz-capture", Setup: func(env *Env) error { got <- env; return nil }}
}

func TestDemo_ServesEveryPage(t *testing.T) {
	runtimeDir := t.TempDir()
	reg := defaultRegistry.snapshot()
	envs := make(chan *Env, 1)
	reg.AddService(capture(envs))
	r := startRun(t, Options{Demo: true, NoOpen: true, RuntimeDir: runtimeDir, registry: reg, Exec: &mock.Executor{}})
	s := openDemo(t, r)
	assert.Contains(t, r.stderr.String(), "Demo: made-up clusters")

	for _, p := range demoPages {
		code, body := s.get(s.base + p)
		assert.Equal(t, http.StatusOK, code, p)
		assert.NotContains(t, body, "Docker is not reachable", p)
	}
	code, body := s.get(s.base + "/clusters")
	require.Equal(t, http.StatusOK, code)
	for _, name := range []string{"default", "dev", "legacy", "pr-145"} {
		assert.Contains(t, body, name)
	}

	env := <-envs
	_, isDemo := env.Exec.(*demo.Exec)
	assert.True(t, isDemo, "commands go to the demo's executor, not the real one")
	assert.NotSame(t, env.Opts.Exec, env.Exec)
	assert.NotNil(t, env.Get(ColRefreshKey))
	assert.NotNil(t, env.Get(ColDoctorKey))
	iv, ok := env.Get(ColMonitoringIntervalKey).(moreInterval)
	require.True(t, ok)
	assert.Equal(t, 30*time.Second, iv.Get(), "Run fills in the default")

	require.NoError(t, r.stop())
	entries, err := os.ReadDir(runtimeDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a demo records nothing in the runtime directory")
}

func TestDemo_RunsACommandEndToEnd(t *testing.T) {
	was := demo.Speed
	demo.Speed = 200
	t.Cleanup(func() { demo.Speed = was })
	r := startRun(t, Options{Demo: true, NoOpen: true, registry: defaultRegistry.snapshot()})
	s := openDemo(t, r)

	form := url.Values{"realm": {"sind"}, "name": {"e2e"}, "w0-count": {"1"}, "cpus": {"1"}, "dtype": {"volume"}, "wait": {"1m"}}
	_, preview := s.post("/actions/create-cluster/preview", form)
	m := regexp.MustCompile(`name="shown" value="([^"]*)"`).FindStringSubmatch(preview)
	require.NotNil(t, m, preview)
	form.Set("shown", m[1])
	resp, body := s.post("/actions/create-cluster/run", form)
	require.Equal(t, http.StatusNoContent, resp.StatusCode, body)
	redirect := resp.Header.Get("HX-Redirect")
	require.True(t, strings.HasPrefix(redirect, "/tasks/"), redirect)

	// The task page leads to the run, whose steps are the real ones.
	require.Eventually(t, func() bool {
		code, page := s.get(s.base + redirect)
		return code == http.StatusOK && strings.Contains(page, "mesh registration") && strings.Contains(page, "ok")
	}, 20*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		_, page := s.get(s.base + "/clusters/sind/e2e")
		return strings.Contains(page, "e2e") && !strings.Contains(page, "Not found")
	}, 20*time.Second, 100*time.Millisecond)
	require.NoError(t, r.stop())
}

func TestDemo_StreamsLogs(t *testing.T) {
	r := startRun(t, Options{Demo: true, NoOpen: true, registry: defaultRegistry.snapshot()})
	s := openDemo(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/logs/sind/dev?node=worker-1&source=journal", nil)
	require.NoError(t, err)
	resp, err := s.c.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	sc := bufio.NewScanner(resp.Body)
	lines := 0
	for lines < 5 && sc.Scan() {
		if strings.HasPrefix(sc.Text(), "data: ") && strings.Contains(sc.Text(), "worker-1 slurmd[") {
			lines++
		}
	}
	assert.Equal(t, 5, lines, "made-up journal lines of the node")
	cancel()
	require.NoError(t, r.stop())
}

func TestDemo_StillNeedsDemo(t *testing.T) {
	err := Run(context.Background(), Options{Still: true, NoOpen: true})
	assert.EqualError(t, err, "--still needs --demo")
}

func TestDemoServices(t *testing.T) {
	fail := func(*Env) error { t.Fatal("the real Setup ran"); return nil }
	registered := []Service{
		{Name: "act", Setup: fail, Run: func(context.Context, *Env) {}},
		{Name: "collect", Setup: fail},
		{Name: "collect-activity"},
		{Name: "more"},
	}
	d := demo.New(store.New(), true)
	got := demoServices(d, registered)

	var names []string
	for _, svc := range got {
		names = append(names, svc.Name)
	}
	assert.Equal(t, []string{"act", "more", "demo"}, names, "no collectors, the runner and the demo's own service")
	assert.NotNil(t, got[0].Run, "the runner keeps its Run, which stops the commands")

	env := &Env{Store: store.New()}
	require.NoError(t, got[0].Setup(env))
	st, ok := env.Get(actKey{}).(*actState)
	require.True(t, ok)
	assert.Same(t, d.Starter(), st.starter)
	assert.Equal(t, demo.Home, st.home)
	assert.Equal(t, demo.ConfigDir, st.configDir)
	require.NoError(t, st.writeFile("/x", []byte("y")))
	assert.Len(t, registered, 4, "the registry's services are left as they were")
}

func TestDemoServices_DemoSetupAndRun(t *testing.T) {
	st := store.New()
	d := demo.New(st, true)
	svcs := demoServices(d, nil)
	require.Len(t, svcs, 1)
	env := &Env{Store: st}
	require.NoError(t, svcs[0].Setup(env))
	assert.Len(t, st.Snapshot().Realms, 2, "the store is filled before the server is built")
	refresh, ok := env.Get(ColRefreshKey).(func(string))
	require.True(t, ok)
	refresh("sind")
	doctor, ok := env.Get(ColDoctorKey).(func())
	require.True(t, ok)
	doctor()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svcs[0].Run(ctx, env); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the demo's Run did not end with its context")
	}
}

// --- still ---

// token masks what differs from run to run in a page: the CSRF token and
// the port.
var stillMask = regexp.MustCompile(`"X-Sind-CSRF":"[^"]*"|name="csrf" value="[^"]*"|127\.0\.0\.1:\d+`)

// servedStill serves a still demo, fetches the pages and stops it.
func servedStill(t *testing.T) map[string]string {
	t.Helper()
	r := startRun(t, Options{Demo: true, Still: true, NoOpen: true, registry: defaultRegistry.snapshot()})
	s := openDemo(t, r)
	pages := map[string]string{}
	for _, p := range stillPages {
		resp, err := s.c.Get(s.base + p)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, p)
		pages[p] = stillMask.ReplaceAllString(string(body), "")
	}
	require.NoError(t, r.stop())
	return pages
}

func TestStillDemoRepeats(t *testing.T) {
	first, second := servedStill(t), servedStill(t)
	require.Len(t, first, len(stillPages))
	for _, p := range stillPages {
		assert.True(t, first[p] == second[p], "GET %s differs between two runs of the still demo:\n%s", p, firstDiff(first[p], second[p]))
	}
}

func TestStillDemoStandsAtTheEpoch(t *testing.T) {
	pages := servedStill(t)
	run := pages["/runs/d3e0000000000063"]
	assert.Contains(t, run, "running", "the run that goes on")
	assert.Contains(t, run, "09:14:29", "it started 31 s before the epoch, 09:15:00 UTC")
}

// firstDiff shows the first line where a and b differ.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return "first run:  " + al[i] + "\nsecond run: " + bl[i]
		}
	}
	return "the lengths differ"
}
