// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const logsDemoTemplate = `{{define "page-logsdemo"}}{{template "logs-tab" .}}{{end}}`

func logsNodes() []cluster.NodeSummary {
	return []cluster.NodeSummary{
		{Container: "sind-dev-controller", Cluster: "dev", Role: config.RoleController, State: cluster.StateRunning},
		{Container: "sind-dev-db", Cluster: "dev", Role: config.RoleDB, State: cluster.StateRunning},
		{Container: "sind-dev-submitter", Cluster: "dev", Role: config.RoleSubmitter, State: cluster.StateRunning},
		{Container: "sind-dev-worker-0", Cluster: "dev", Role: config.RoleWorker, State: cluster.StateRunning},
		{Container: "sind-dev-worker-1", Cluster: "dev", Role: config.RoleWorker, State: "exited"},
		{Container: "sind-other-worker-0", Cluster: "other", Role: config.RoleWorker, State: cluster.StateRunning},
	}
}

// logsServer is a test server with the logs part, the nodes of realm sind
// and a page that holds the logs tab.
func logsServer(t *testing.T, ex *mock.Executor) *testServer {
	t.Helper()
	cfg, st := testConfig(t)
	cfg.Env.Exec = ex
	cfg.Registry.AddRoute(logsRoute)
	cfg.Registry.AddPage(Page{
		Name: "logsdemo", Pattern: "GET /clusters/{realm}/{cluster}", Title: "Cluster",
		Nav: "clusters", Params: []string{"node"},
	})
	cfg.FS = overlay(t, fstest.MapFS{
		"templates/test.tmpl":     {Data: []byte(testTemplates)},
		"templates/logsdemo.tmpl": {Data: []byte(logsDemoTemplate)},
	})
	st.SetNodes("sind", logsNodes())
	return newTestServerFrom(t, cfg, st).login()
}

// pipeStarter returns an executor whose Start hands out pipes, and the pipes.
func pipeStarter() (*mock.Executor, *mock.Pipes) {
	p := &mock.Pipes{}
	return &mock.Executor{OnStart: p.OnStart}, p
}

// lockedWriter is a ResponseWriter that the handler and the test share.
type lockedWriter struct {
	mu        sync.Mutex
	hdr       http.Header
	code      int
	body      strings.Builder
	failAfter int // writes that succeed; negative: all
}

func newLockedWriter(failAfter int) *lockedWriter {
	return &lockedWriter{hdr: http.Header{}, failAfter: failAfter}
}

func (w *lockedWriter) Header() http.Header { return w.hdr }
func (w *lockedWriter) WriteHeader(c int)   { w.mu.Lock(); w.code = c; w.mu.Unlock() }
func (w *lockedWriter) Flush()              {}
func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failAfter == 0 {
		return 0, errors.New("client gone")
	}
	if w.failAfter > 0 {
		w.failAfter--
	}
	return w.body.Write(p)
}
func (w *lockedWriter) text() string { w.mu.Lock(); defer w.mu.Unlock(); return w.body.String() }

// noFlushWriter has no Flush method.
type noFlushWriter struct{ http.ResponseWriter }

// logsRun serves the stream in a goroutine and returns what is needed to
// stop it and to see that it has ended.
func logsRun(ts *testServer, w http.ResponseWriter, target string) (cancel context.CancelFunc, done <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	r := request("GET", target).WithContext(ctx)
	r.AddCookie(ts.cookie)
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		ts.h.ServeHTTP(w, r)
	}()
	return cancel, ch
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, 5*time.Millisecond, what)
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return")
	}
}

func TestLogsArgs(t *testing.T) {
	assert.Equal(t, []string{"logs", "--follow", "--tail", "200", "sind-dev-worker-0"},
		logsArgs(logsDocker, "sind", "dev", "worker-0", ""))
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "journalctl", "-u", "slurmd", "--follow", "-n", "200"},
		logsArgs(logsJournal, "sind", "dev", "worker-0", "slurmd"))
	assert.Equal(t, []string{"exec", "sind-dev-worker-0", "journalctl", "--follow", "-n", "200"},
		logsArgs(logsJournal, "sind", "dev", "worker-0", ""))
}

func TestLogsStreamArgvPerSource(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"node=worker-0", []string{"logs", "--follow", "--tail", "200", "sind-dev-worker-0"}},
		{"node=worker-0&source=docker&unit=slurmd", []string{"logs", "--follow", "--tail", "200", "sind-dev-worker-0"}},
		{"node=worker-0&source=journal&unit=slurmd", []string{"exec", "sind-dev-worker-0", "journalctl", "-u", "slurmd", "--follow", "-n", "200"}},
		{"node=controller&source=journal", []string{"exec", "sind-dev-controller", "journalctl", "--follow", "-n", "200"}},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			ex, pipes := pipeStarter()
			ts := logsServer(t, ex)
			w := newLockedWriter(-1)
			cancel, done := logsRun(ts, w, "/logs/sind/dev?"+c.query)
			waitFor(t, "start", func() bool { return pipes.Len() == 1 })
			cancel()
			waitDone(t, done)
			require.Len(t, ex.Calls, 1)
			assert.Equal(t, "docker", ex.Calls[0].Name)
			assert.Equal(t, c.want, ex.Calls[0].Args)
		})
	}
}

func TestLogsStreamLinesAndEnd(t *testing.T) {
	ex, pipes := pipeStarter()
	ts := logsServer(t, ex)
	w := newLockedWriter(-1)
	_, done := logsRun(ts, w, "/logs/sind/dev?node=worker-0")
	waitFor(t, "start", func() bool { return pipes.Len() == 1 })
	pipes.Write(0, "<b>hello</b>\x1b[31m red\x1b[0m\r\nsecond\n")
	waitFor(t, "lines", func() bool { return strings.Contains(w.text(), "second") })
	pipes.CloseAll()
	waitDone(t, done)
	body := w.text()
	assert.Equal(t, http.StatusOK, w.code)
	assert.Equal(t, "text/event-stream", w.hdr.Get("Content-Type"))
	assert.Contains(t, body, "event: line\ndata: <b>hello</b> red\n\n")
	assert.Contains(t, body, "event: line\ndata: second\n\n")
	assert.Contains(t, body, "event: notice\ndata: the log ended\n\n")
	assert.True(t, strings.HasSuffix(body, "event: end\ndata: \n\n"), body)
}

// closeTracker closes with a channel, as the child's stdout does.
type closeTracker struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (c *closeTracker) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.ReadCloser.Close()
}

func trackedExecutor() (*mock.Executor, *closeTracker) {
	pr, _ := io.Pipe()
	ct := &closeTracker{ReadCloser: pr, closed: make(chan struct{})}
	return &mock.Executor{OnStart: func([]string) mock.StreamResult { return mock.StreamResult{Reader: ct} }}, ct
}

func TestLogsStreamStopsChildOnDisconnect(t *testing.T) {
	ex, ct := trackedExecutor()
	ts := logsServer(t, ex)
	cancel, done := logsRun(ts, newLockedWriter(-1), "/logs/sind/dev?node=worker-0")
	waitFor(t, "start", func() bool { return logsStreams.Load() == 1 })
	select {
	case <-ct.closed:
		t.Fatal("closed before the client left")
	default:
	}
	cancel()
	waitDone(t, done)
	select {
	case <-ct.closed:
	default:
		t.Fatal("the child was not stopped")
	}
	assert.Zero(t, logsStreams.Load())
}

func TestLogsStreamStopsChildOnShutdown(t *testing.T) {
	ex, ct := trackedExecutor()
	ts := logsServer(t, ex)
	_, done := logsRun(ts, newLockedWriter(-1), "/logs/sind/dev?node=worker-0&source=journal&unit=slurmd")
	waitFor(t, "start", func() bool { return logsStreams.Load() == 1 })
	ts.cancel()
	waitDone(t, done)
	select {
	case <-ct.closed:
	default:
		t.Fatal("the child was not stopped")
	}
}

func TestLogsStreamStopsWhenClientWriteFails(t *testing.T) {
	ex, pipes := pipeStarter()
	ts := logsServer(t, ex)
	w := newLockedWriter(1) // the opening comment only
	_, done := logsRun(ts, w, "/logs/sind/dev?node=worker-0")
	waitFor(t, "start", func() bool { return pipes.Len() == 1 })
	pipes.Write(0, "line\n")
	waitDone(t, done)
}

func TestLogsStreamKeepAlive(t *testing.T) {
	ex, pipes := pipeStarter()
	ts := logsServer(t, ex)
	ts.srv.keepAlive = 5 * time.Millisecond
	w := newLockedWriter(-1)
	cancel, done := logsRun(ts, w, "/logs/sind/dev?node=worker-0")
	waitFor(t, "start", func() bool { return pipes.Len() == 1 })
	waitFor(t, "keep-alive", func() bool { return strings.Contains(w.text(), ": keep-alive") })
	cancel()
	waitDone(t, done)
}

func TestLogsStreamKeepAliveFailure(t *testing.T) {
	ex, pipes := pipeStarter()
	ts := logsServer(t, ex)
	ts.srv.keepAlive = 5 * time.Millisecond
	_, done := logsRun(ts, newLockedWriter(1), "/logs/sind/dev?node=worker-0")
	waitFor(t, "start", func() bool { return pipes.Len() == 1 })
	waitDone(t, done)
}

func TestLogsStreamSkipsTooFastLines(t *testing.T) {
	flood := strings.Repeat("x\n", 3*logsMaxRate)
	ex := &mock.Executor{OnStart: func([]string) mock.StreamResult {
		return mock.StreamResult{Reader: io.NopCloser(strings.NewReader(flood))}
	}}
	ts := logsServer(t, ex)
	w := newLockedWriter(-1)
	_, done := logsRun(ts, w, "/logs/sind/dev?node=worker-0")
	waitDone(t, done)
	body := w.text()
	assert.Contains(t, body, " lines skipped: the stream was too fast")
	assert.LessOrEqual(t, strings.Count(body, "event: line"), logsMaxRate)
	assert.True(t, strings.HasSuffix(body, "event: end\ndata: \n\n"))
}

func TestLogsStreamRefusals(t *testing.T) {
	ex, _ := pipeStarter()
	ts := logsServer(t, ex)
	for target, want := range map[string]int{
		"/logs/sind/dev":                                         http.StatusNotFound,
		"/logs/sind/dev?node=nope":                               http.StatusNotFound,
		"/logs/sind/dev?node=sind-dev-worker-0":                  http.StatusNotFound,
		"/logs/sind/dev?node=worker-0.dev":                       http.StatusNotFound,
		"/logs/sind/other?node=worker-1":                         http.StatusNotFound,
		"/logs/sind/nope?node=worker-0":                          http.StatusNotFound,
		"/logs/nope/dev?node=worker-0":                           http.StatusNotFound,
		"/logs/sind/dev?node=worker-0&source=shell":              http.StatusBadRequest,
		"/logs/sind/dev?node=worker-0&source=journal&unit=-f":    http.StatusBadRequest,
		"/logs/sind/dev?node=worker-0&source=journal&unit=a%20b": http.StatusBadRequest,
		"/logs/sind/dev?node=" + strings.Repeat("a", 300):        http.StatusBadRequest,
	} {
		assert.Equal(t, want, ts.get(target).Code, target)
	}
	assert.Empty(t, ex.Calls, "no child for a refused request")
}

func TestLogsStreamStartFailure(t *testing.T) {
	ts := logsServer(t, &mock.Executor{OnStart: func([]string) mock.StreamResult {
		return mock.StreamResult{Err: errors.New("no docker")}
	}})
	w := ts.get("/logs/sind/dev?node=worker-0")
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Zero(t, logsStreams.Load())
}

func TestLogsStreamNoFlusher(t *testing.T) {
	ex, _ := pipeStarter()
	ts := logsServer(t, ex)
	r := request("GET", "/logs/sind/dev?node=worker-0")
	r.AddCookie(ts.cookie)
	rec := httptest.NewRecorder()
	ts.h.ServeHTTP(noFlushWriter{rec}, r)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Empty(t, ex.Calls)
}

func TestLogsStreamLimit(t *testing.T) {
	ex, pipes := pipeStarter()
	ts := logsServer(t, ex)
	var cancels []context.CancelFunc
	var dones []<-chan struct{}
	for i := 0; i < logsMaxStreams; i++ {
		c, d := logsRun(ts, newLockedWriter(-1), "/logs/sind/dev?node=worker-0")
		cancels, dones = append(cancels, c), append(dones, d)
	}
	waitFor(t, "streams", func() bool { return pipes.Len() == logsMaxStreams })
	assert.Equal(t, http.StatusTooManyRequests, ts.get("/logs/sind/dev?node=worker-0").Code)
	for i, c := range cancels {
		c()
		waitDone(t, dones[i])
	}
	assert.Zero(t, logsStreams.Load())
}

func TestLogsRead(t *testing.T) {
	t.Run("cut line", func(t *testing.T) {
		long := strings.Repeat("a", 3*logsMaxLine)
		out := make(chan string, 4)
		var dropped atomic.Int64
		logsRead(strings.NewReader(long+"\nnext\nlast"), out, &dropped, time.Now)
		close(out)
		var got []string
		for l := range out {
			got = append(got, l)
		}
		require.Len(t, got, 3)
		assert.Len(t, got[0], logsMaxLine+len(" [line cut]"))
		assert.True(t, strings.HasSuffix(got[0], " [line cut]"))
		assert.Equal(t, []string{"next", "last"}, got[1:])
		assert.Zero(t, dropped.Load())
	})
	t.Run("blank line and no final newline", func(t *testing.T) {
		out := make(chan string, 4)
		var dropped atomic.Int64
		logsRead(strings.NewReader("a\n\nb"), out, &dropped, time.Now)
		close(out)
		var got []string
		for l := range out {
			got = append(got, l)
		}
		assert.Equal(t, []string{"a", "", "b"}, got)
	})
	t.Run("slow reader", func(t *testing.T) {
		out := make(chan string, 1)
		var dropped atomic.Int64
		logsRead(strings.NewReader("1\n2\n3\n4\n5\n"), out, &dropped, time.Now)
		assert.Equal(t, int64(4), dropped.Load())
		assert.Equal(t, "1", <-out)
	})
	t.Run("rate", func(t *testing.T) {
		now := time.Unix(1000, 0)
		out := make(chan string, 4*logsMaxRate)
		var dropped atomic.Int64
		in := strings.Repeat("x\n", logsMaxRate+5)
		logsRead(strings.NewReader(in), out, &dropped, func() time.Time { return now })
		assert.Equal(t, int64(5), dropped.Load())
		assert.Len(t, out, logsMaxRate)
		// The next second starts again.
		now = now.Add(time.Second)
		logsRead(strings.NewReader("y\n"), out, &dropped, func() time.Time { return now })
		assert.Equal(t, int64(5), dropped.Load())
	})
	t.Run("read error", func(t *testing.T) {
		out := make(chan string, 4)
		var dropped atomic.Int64
		logsRead(io.MultiReader(strings.NewReader("part"), &errReader{}), out, &dropped, time.Now)
		assert.Equal(t, "part", <-out)
	})
}

type errReader struct{}

func (*errReader) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestLogsReadLine(t *testing.T) {
	br := bufio.NewReaderSize(strings.NewReader("one\r\ntwo"), logsMaxLine)
	l, err := logsReadLine(br)
	assert.NoError(t, err)
	assert.Equal(t, "one", l)
	l, err = logsReadLine(br)
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, "two", l)
}

func TestLogsClean(t *testing.T) {
	for in, want := range map[string]string{
		"plain\n":                   "plain",
		"a\tb":                      "a\tb",
		"\x1b[1;31mred\x1b[0m":      "red",
		"\x1b]0;title\x07text":      "text",
		"\x1b]0;title\x1b\\text":    "text",
		"\x1bMmove":                 "move",
		"bell\x07 nul\x00 del\x7f":  "bell nul del",
		"c1\u0085\u009b end":        "c1 end",
		"bad \xff utf8":             "bad � utf8",
		"<script>alert(1)</script>": "<script>alert(1)</script>",
		"line\rbreak":               "linebreak",
		"ünïcode ✓":                 "ünïcode ✓",
	} {
		assert.Equal(t, want, logsClean(in), fmt.Sprintf("%q", in))
	}
}

func TestLogsTab(t *testing.T) {
	ex, _ := pipeStarter()
	ts := logsServer(t, ex)

	w := ts.get("/clusters/sind/dev")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `data-realm="sind" data-cluster="dev" data-cmd-prefix="sind logs "`)
	assert.Contains(t, body, `<option value="controller" data-units="slurmctld munge sshd" selected>controller</option>`)
	assert.Contains(t, body, `data-units="mariadb slurmdbd munge sshd"`)
	assert.Contains(t, body, `<option value="submitter" data-units="munge sshd">`)
	assert.Contains(t, body, `data-units="slurmd munge sshd">worker-0</option>`)
	assert.Contains(t, body, `>worker-1 (exited)</option>`)
	assert.NotContains(t, body, "other-worker")
	assert.Contains(t, body, `<code data-logs-cmd>sind logs controller.dev -f -n 200</code>`)
	assert.Contains(t, body, "journal, all units")

	body = ts.get("/clusters/sind/dev?node=worker-0").Body.String()
	assert.Contains(t, body, `value="worker-0" data-units="slurmd munge sshd" selected`)
	assert.Contains(t, body, "sind logs worker-0.dev -f -n 200")

	body = ts.get("/clusters/sind/dev?node=gone").Body.String()
	assert.Contains(t, body, `value="controller" data-units="slurmctld munge sshd" selected`)

	// An empty snapshot says so.
	body = ts.get("/clusters/sind/nope").Body.String()
	assert.Contains(t, body, "No nodes of nope are known yet")
	assert.NotContains(t, body, "data-logs-box")
}

func TestLogsRegistered(t *testing.T) {
	r := defaultRegistry.snapshot()
	var patterns []string
	for _, rt := range r.routes {
		patterns = append(patterns, rt.Pattern)
	}
	assert.Contains(t, patterns, "GET /logs/{realm}/{cluster}")
}

func TestLogsRefuseNavigationFromAnotherPage(t *testing.T) {
	ts := logsServer(t, &mock.Executor{})
	for site, want := range map[string]int{"same-site": http.StatusForbidden, "cross-site": http.StatusForbidden} {
		r := request("GET", "/logs/sind/dev?node=controller")
		r.AddCookie(ts.cookie)
		r.Header.Set("Sec-Fetch-Site", site)
		r.Header.Set("Sec-Fetch-Mode", "navigate")
		r.Header.Set("Sec-Fetch-Dest", "document")
		assert.Equal(t, want, ts.do(r).Code, site)
	}
}
