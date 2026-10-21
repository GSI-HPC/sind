// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRegistry returns a registry that serves the real templates: the shell
// and the Overview page.
func runRegistry() *Registry {
	r := NewRegistry()
	r.AddFragment(railFragment())
	r.AddFragment(tasksFragment())
	r.AddPage(overviewPage)
	pgRegister(r)
	return r
}

// running is a Run in a goroutine.
type running struct {
	t      *testing.T
	cancel context.CancelFunc
	done   chan error
	stderr *syncBuffer
	url    string
}

var urlRE = regexp.MustCompile(`http://\S+`)

// startRun starts Run with o and waits until it has printed its URL.
func startRun(t *testing.T, o Options) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &running{t: t, cancel: cancel, done: make(chan error, 1), stderr: &syncBuffer{}}
	o.Stderr = r.stderr
	if o.registry == nil {
		o.registry = runRegistry()
	}
	go func() { r.done <- Run(ctx, o) }()
	require.Eventually(t, func() bool {
		select {
		case err := <-r.done:
			r.done <- err
			return true
		default:
			return strings.Contains(r.stderr.String(), "Press Ctrl-C")
		}
	}, 5*time.Second, 5*time.Millisecond, r.stderr.String())
	r.url = urlRE.FindString(r.stderr.String())
	require.NotEmpty(t, r.url, r.stderr.String())
	return r
}

// stop ends Run and returns its error.
func (r *running) stop() error {
	r.t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		return err
	case <-time.After(10 * time.Second):
		require.FailNow(r.t, "Run did not return")
		return nil
	}
}

// browse fetches rawURL like a browser: with cookies, following redirects.
func browse(t *testing.T, rawURL string) (*http.Response, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, err := c.Get(rawURL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(body)
}

func TestRunServesTheOverviewAndStops(t *testing.T) {
	runtimeDir := t.TempDir()
	var started, stopped atomic.Bool
	reg := runRegistry()
	reg.AddService(Service{
		Name: "svc",
		Setup: func(env *Env) error {
			env.Put("answer", 42)
			return nil
		},
		Run: func(ctx context.Context, env *Env) {
			assert.Equal(t, 42, env.Get("answer"))
			assert.NotNil(t, env.Store)
			assert.Contains(t, env.Addr, "127.0.0.1:")
			started.Store(true)
			<-ctx.Done()
			stopped.Store(true)
		},
	})
	reg.AddService(Service{Name: "inert"}) // neither Setup nor Run
	m := &mock.Executor{}
	r := startRun(t, Options{NoOpen: true, RuntimeDir: runtimeDir, Exec: m, registry: reg})

	assert.Regexp(t, `^http://127\.0\.0\.1:\d+/\?token=[A-Za-z0-9_-]{43}$`, r.url)
	assert.Contains(t, r.stderr.String(), "sind ui: "+r.url)

	resp, body := browse(t, r.url)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "<h1>Overview</h1>")
	assert.NotContains(t, resp.Request.URL.String(), "token", "the token left the address bar")
	require.Eventually(t, started.Load, 2*time.Second, time.Millisecond)

	// Without the token, the page is not served.
	plain := strings.SplitN(r.url, "?", 2)[0]
	noToken, err := http.Get(plain)
	require.NoError(t, err)
	_ = noToken.Body.Close()
	assert.Equal(t, http.StatusForbidden, noToken.StatusCode)

	// The record names this process, readable by the user alone.
	record := filepath.Join(runtimeDir, "sind", "ui.json")
	info, err := os.Stat(record)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	rec, ok := readRecord(record)
	require.True(t, ok)
	assert.Equal(t, runtimeRecord{URL: r.url, PID: os.Getpid()}, rec)

	require.NoError(t, r.stop())
	assert.True(t, stopped.Load(), "services end with Run")
	assert.NoFileExists(t, record)
	assert.Empty(t, m.Calls, "no browser with --no-open")
}

func TestRunOpensTheBrowserThroughAPrivateFile(t *testing.T) {
	runtimeDir := t.TempDir()
	opened := make(chan []string, 1)
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		opened <- args
		return mock.Result{}
	}}
	r := startRun(t, Options{RuntimeDir: runtimeDir, Exec: m})
	defer func() { require.NoError(t, r.stop()) }()

	var args []string
	select {
	case args = <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("no browser was opened")
	}
	require.Equal(t, []string{filepath.Join(runtimeDir, "sind", "ui-open.html")}, args)
	page, err := os.ReadFile(args[0])
	require.NoError(t, err)
	assert.Contains(t, string(page), r.url)
	assert.NotContains(t, strings.Join(args, " "), "token")
	assert.Equal(t, "xdg-open", m.Calls[0].Name)
}

func TestRunOpensTheBrowserWithoutARuntimeDir(t *testing.T) {
	opened := make(chan []string, 1)
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		opened <- args
		return mock.Result{}
	}}
	r := startRun(t, Options{Exec: m})
	defer func() { require.NoError(t, r.stop()) }()
	select {
	case args := <-opened:
		assert.Equal(t, []string{r.url}, args)
	case <-time.After(2 * time.Second):
		t.Fatal("no browser was opened")
	}
}

func TestSecondRunOpensTheRunningOne(t *testing.T) {
	runtimeDir := t.TempDir()
	first := startRun(t, Options{NoOpen: true, RuntimeDir: runtimeDir})
	defer func() { require.NoError(t, first.stop()) }()

	opened := make(chan []string, 1)
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		opened <- args
		return mock.Result{}
	}}
	var stderr syncBuffer
	err := Run(context.Background(), Options{RuntimeDir: runtimeDir, Exec: m, Stderr: &stderr, registry: runRegistry()})
	require.NoError(t, err)
	assert.Equal(t, "sind ui is already running: "+first.url+"\n", stderr.String())
	require.Len(t, opened, 1, "the second start waited for xdg-open")
	page, err := os.ReadFile((<-opened)[0])
	require.NoError(t, err)
	assert.Contains(t, string(page), first.url)

	// With --no-open it only says where.
	m2 := &mock.Executor{}
	stderr = syncBuffer{}
	require.NoError(t, Run(context.Background(), Options{NoOpen: true, RuntimeDir: runtimeDir, Exec: m2, Stderr: &stderr, registry: runRegistry()}))
	assert.Contains(t, stderr.String(), "already running")
	assert.Empty(t, m2.Calls)

	// The first one still runs and still has its record.
	resp, _ := browse(t, first.url)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSecondRunDoesNotWaitForeverForTheBrowser(t *testing.T) {
	runtimeDir := t.TempDir()
	first := startRun(t, Options{NoOpen: true, RuntimeDir: runtimeDir})
	defer func() { require.NoError(t, first.stop()) }()

	saved := openWait
	openWait = 50 * time.Millisecond
	t.Cleanup(func() { openWait = saved })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	m := &mock.Executor{OnCall: func([]string, string) mock.Result { <-release; return mock.Result{} }}
	start := time.Now()
	require.NoError(t, Run(context.Background(), Options{RuntimeDir: runtimeDir, Exec: m, Stderr: io.Discard, registry: runRegistry()}))
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestRunReplacesAStaleRecord(t *testing.T) {
	runtimeDir := t.TempDir()
	i, err := newInstance(runtimeDir)
	require.NoError(t, err)
	writeRecordFile(t, i, `{"url":"http://127.0.0.1:1/?token=old","pid":`+strconv.Itoa(1<<30)+`}`)

	r := startRun(t, Options{NoOpen: true, RuntimeDir: runtimeDir})
	rec, ok := readRecord(i.recordPath())
	require.True(t, ok)
	assert.Equal(t, r.url, rec.URL)
	require.NoError(t, r.stop())
	assert.NoFileExists(t, i.recordPath())
}

func TestRunTwiceOnAFixedPort(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	r := startRun(t, Options{NoOpen: true, Port: port})
	assert.Contains(t, r.url, fmt.Sprintf("127.0.0.1:%d/", port))
	// Without a runtime directory nothing records the first, so the second
	// finds the port taken.
	err = Run(context.Background(), Options{NoOpen: true, Port: port, registry: runRegistry()})
	assert.EqualError(t, err, fmt.Sprintf("port %d is in use; pick another with --port, or leave it out", port))
	require.NoError(t, r.stop())
}

func TestRunRefusesConnectionsOfOtherUsers(t *testing.T) {
	// A /proc without the table cannot name the owner of a connection, and
	// the connection is closed before a request is read.
	r := startRun(t, Options{NoOpen: true, procRoot: t.TempDir()})
	defer func() { require.NoError(t, r.stop()) }()
	c := &http.Client{Timeout: 3 * time.Second}
	_, err := c.Get(r.url)
	require.Error(t, err)
	assert.Eventually(t, func() bool { return strings.Contains(r.stderr.String(), "closed a connection") },
		2*time.Second, time.Millisecond, "the refusal is logged")
}

func TestRunDefaults(t *testing.T) {
	// A context that has ended already: Run starts, then stops, with every
	// default in place: the real executor, no log, the embedded files, the
	// default registry and /proc.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, Run(ctx, Options{NoOpen: true}))
}

func TestRunErrors(t *testing.T) {
	t.Run("runtime directory is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, nil, 0o600))
		err := Run(context.Background(), Options{RuntimeDir: file, NoOpen: true, registry: runRegistry()})
		assert.ErrorContains(t, err, "preparing the runtime directory")
	})
	t.Run("lock fails", func(t *testing.T) {
		err := Run(context.Background(), Options{
			RuntimeDir: t.TempDir(), NoOpen: true, registry: runRegistry(),
			flock: func(int, int) error { return errors.New("no locks here") },
		})
		assert.ErrorContains(t, err, "locking the runtime directory: no locks here")
	})
	t.Run("cannot listen", func(t *testing.T) {
		err := Run(context.Background(), Options{
			NoOpen: true, registry: runRegistry(),
			listen: func(int, string, *slog.Logger) (net.Listener, error) { return nil, errors.New("no sockets") },
		})
		assert.EqualError(t, err, "no sockets")
	})
	t.Run("setup fails", func(t *testing.T) {
		reg := runRegistry()
		var ran atomic.Bool
		reg.AddService(Service{Name: "a", Run: func(context.Context, *Env) { ran.Store(true) }})
		reg.AddService(Service{Name: "b", Setup: func(*Env) error { return errors.New("no docker") }})
		err := Run(context.Background(), Options{NoOpen: true, registry: reg})
		assert.EqualError(t, err, "starting b: no docker")
		assert.False(t, ran.Load(), "nothing runs before every Setup has passed")
	})
	t.Run("registry does not fit the templates", func(t *testing.T) {
		reg := runRegistry()
		reg.AddPage(Page{Name: "lonely", Pattern: "GET /lonely"})
		err := Run(context.Background(), Options{NoOpen: true, registry: reg})
		assert.ErrorContains(t, err, `page lonely has no template block "page-lonely"`)
	})
	t.Run("record cannot be written", func(t *testing.T) {
		runtimeDir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(runtimeDir, "sind", "ui.json", "inside"), 0o700))
		err := Run(context.Background(), Options{RuntimeDir: runtimeDir, NoOpen: true, registry: runRegistry()})
		assert.ErrorContains(t, err, "recording the running sind ui")
	})
	t.Run("the server fails", func(t *testing.T) {
		err := Run(context.Background(), Options{
			NoOpen: true, registry: runRegistry(),
			listen: func(int, string, *slog.Logger) (net.Listener, error) { return brokenListener{}, nil },
		})
		assert.EqualError(t, err, "accept: broken")
	})
}

// brokenListener fails to accept.
type brokenListener struct{}

func (brokenListener) Accept() (net.Conn, error) { return nil, errors.New("accept: broken") }
func (brokenListener) Close() error              { return nil }
func (brokenListener) Addr() net.Addr            { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
