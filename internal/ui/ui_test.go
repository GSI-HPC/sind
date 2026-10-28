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
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb"
	"github.com/GSI-HPC/go-clikit/localweb/launch"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRegistry returns a registry that serves the real templates: the shell
// and the Overview page.
func runRegistry() *Registry {
	r := NewRegistry()
	r.AddFragment(railFragment())
	registerDock(r)
	r.AddRoute(pgRealmRoute(overviewPage, false, false))
	pgRegister(r)
	return r
}

// desktop stands in for a desktop with a display. Its opener records the
// arguments it is started with instead of starting a browser.
type desktop struct {
	opened chan []string
}

func newDesktop() *desktop { return &desktop{opened: make(chan []string, 4)} }

// options returns o with the desktop's environment and opener.
func (d *desktop) options(o Options) Options {
	o.GOOS = "linux"
	o.Getenv = func(k string) string {
		if k == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	o.start = func(name string, args ...string) error {
		d.opened <- append([]string{name}, args...)
		return nil
	}
	return o
}

// wait returns what the opener was started with.
func (d *desktop) wait(t *testing.T) []string {
	t.Helper()
	select {
	case args := <-d.opened:
		return args
	case <-time.After(2 * time.Second):
		t.Fatal("no browser was opened")
		return nil
	}
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
	d := newDesktop()
	r := startRun(t, d.options(Options{NoOpen: true, RuntimeDir: runtimeDir, registry: reg}))

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
	data, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf(`{"url":%q,"pid":%d}`, r.url, os.Getpid()), string(data))

	require.NoError(t, r.stop())
	assert.True(t, stopped.Load(), "services end with Run")
	assert.NoFileExists(t, record)
	assert.Empty(t, d.opened, "no browser with --no-open")
}

func TestRunOpensTheBrowserThroughAPrivateFile(t *testing.T) {
	runtimeDir := t.TempDir()
	d := newDesktop()
	r := startRun(t, d.options(Options{RuntimeDir: runtimeDir}))

	args := d.wait(t)
	port := r.url[strings.LastIndex(r.url, ":")+1 : strings.Index(r.url, "/?")]
	page := filepath.Join(runtimeDir, "sind", "open-"+port+".html")
	require.Equal(t, []string{"xdg-open", page}, args)
	data, err := os.ReadFile(page)
	require.NoError(t, err)
	assert.Contains(t, string(data), r.url)
	assert.NotContains(t, strings.Join(args, " "), "token")

	require.NoError(t, r.stop())
	assert.NoFileExists(t, page, "removed when sind ui stops")
}

func TestRunOpensTheBrowserWithoutARuntimeDir(t *testing.T) {
	d := newDesktop()
	r := startRun(t, d.options(Options{}))
	args := d.wait(t)
	// The page is in a private directory of its own, not in the command line.
	require.Len(t, args, 2)
	assert.NotContains(t, args[1], "token")
	page, err := os.ReadFile(args[1])
	require.NoError(t, err)
	assert.Contains(t, string(page), r.url)
	info, err := os.Stat(filepath.Dir(args[1]))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	require.NoError(t, r.stop())
	assert.NoDirExists(t, filepath.Dir(args[1]), "removed when sind ui stops")
}

func TestRunWithoutAPrivateDirectoryOpensNoBrowser(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
	d := newDesktop()
	r := startRun(t, d.options(Options{}))
	require.NoError(t, r.stop())
	assert.Empty(t, d.opened)
	assert.Contains(t, r.stderr.String(), "could not make a directory")
}

func TestRunOpensNoBrowserWithoutADesktop(t *testing.T) {
	for name, getenv := range map[string]func(string) string{
		"no display":  func(string) string { return "" },
		"ssh session": func(k string) string { return map[string]string{"DISPLAY": ":0", "SSH_CONNECTION": "a 1 b 2"}[k] },
	} {
		t.Run(name, func(t *testing.T) {
			d := newDesktop()
			o := d.options(Options{})
			o.Getenv = getenv
			r := startRun(t, o)
			require.NoError(t, r.stop())
			assert.Empty(t, d.opened)
			assert.Contains(t, r.stderr.String(), "not opening a browser; open the URL above yourself")
		})
	}
}

func TestRunLogsAnOpenerThatFails(t *testing.T) {
	d := newDesktop()
	o := d.options(Options{})
	o.start = func(string, ...string) error { return errors.New("no xdg-open") }
	r := startRun(t, o)
	require.NoError(t, r.stop())
	assert.Contains(t, r.stderr.String(), "could not open a browser; open the URL above yourself")
	assert.Contains(t, r.stderr.String(), "no xdg-open")
}

// TestRunLogsTheWarningsOfPkg checks that what pkg/ warns of while sind ui
// reads goes to the log, not to the stream as a bare "Warning:" line.
func TestRunLogsTheWarningsOfPkg(t *testing.T) {
	warned := make(chan struct{})
	reg := runRegistry()
	reg.AddService(Service{Name: "warner", Run: func(ctx context.Context, _ *Env) {
		sindlog.Warn(ctx, "listing the nodes without their IPs: gone")
		close(warned)
	}})
	r := startRun(t, Options{NoOpen: true, registry: reg})
	<-warned
	require.NoError(t, r.stop())
	assert.Contains(t, r.stderr.String(), `level=WARN msg="listing the nodes without their IPs: gone"`)
	assert.NotContains(t, r.stderr.String(), "Warning:")
}

// recordAnotherProcess writes the record of a sind ui at url that runs as
// another process: a process that finds its own record finds a start of
// its own that has gone, so a test of a second start records the parent.
func recordAnotherProcess(t *testing.T, runtimeDir, url string) {
	t.Helper()
	dir := filepath.Join(runtimeDir, "sind")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	record := fmt.Sprintf(`{"url":%q,"pid":%d}`, url, os.Getppid())
	require.NoError(t, os.WriteFile(filepath.Join(dir, launch.RecordFile), []byte(record), 0o600))
}

func TestSecondRunOpensTheRunningOne(t *testing.T) {
	runtimeDir := t.TempDir()
	first := startRun(t, Options{NoOpen: true})
	defer func() { require.NoError(t, first.stop()) }()
	recordAnotherProcess(t, runtimeDir, first.url)

	d := newDesktop()
	var stderr syncBuffer
	err := Run(context.Background(), d.options(Options{RuntimeDir: runtimeDir, Stderr: &stderr, registry: runRegistry()}))
	require.NoError(t, err)
	assert.Equal(t, "sind ui is already running: "+first.url+"\n", stderr.String())
	args := d.wait(t)
	require.Len(t, args, 2)
	page, err := os.ReadFile(args[1])
	require.NoError(t, err)
	assert.Contains(t, string(page), first.url)

	// With --no-open it only says where.
	d2 := newDesktop()
	stderr = syncBuffer{}
	require.NoError(t, Run(context.Background(), d2.options(Options{NoOpen: true, RuntimeDir: runtimeDir, Stderr: &stderr, registry: runRegistry()})))
	assert.Contains(t, stderr.String(), "already running")
	assert.Empty(t, d2.opened)

	// The first one still runs and still has its record.
	resp, _ := browse(t, first.url)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRunReplacesAStaleRecord(t *testing.T) {
	runtimeDir := t.TempDir()
	dir := filepath.Join(runtimeDir, "sind")
	require.NoError(t, os.Mkdir(dir, 0o700))
	record := filepath.Join(dir, launch.RecordFile)
	require.NoError(t, os.WriteFile(record, []byte(`{"url":"http://127.0.0.1:1/?token=old","pid":`+strconv.Itoa(1<<30)+`}`), 0o600))

	r := startRun(t, Options{NoOpen: true, RuntimeDir: runtimeDir})
	data, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Contains(t, string(data), r.url)
	require.NoError(t, r.stop())
	assert.NoFileExists(t, record)
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
	// A listener for another user than ours closes the connection before a
	// request is read.
	r := startRun(t, Options{NoOpen: true, listen: func(ctx context.Context, port int, log *slog.Logger) (*localweb.Bound, error) {
		return openListener(ctx, port, func(l net.Listener, refused func(error)) (net.Listener, error) {
			return localweb.PeerListener(l, os.Getuid()+1, refused)
		}, log)
	}})
	defer func() { require.NoError(t, r.stop()) }()
	c := &http.Client{Timeout: 3 * time.Second}
	_, err := c.Get(r.url)
	require.Error(t, err)
	assert.Eventually(t, func() bool { return strings.Contains(r.stderr.String(), "closed the connection") },
		2*time.Second, time.Millisecond, "the refusal is logged")
}

func TestRunDefaults(t *testing.T) {
	// A context that has ended already: Run starts, then stops, with every
	// default in place: the real executor, no log, the embedded files, the
	// default registry.
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
	t.Run("runtime directory is open to others", func(t *testing.T) {
		runtimeDir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(runtimeDir, "sind"), 0o700))
		require.NoError(t, os.Chmod(filepath.Join(runtimeDir, "sind"), 0o755))
		err := Run(context.Background(), Options{RuntimeDir: runtimeDir, NoOpen: true, registry: runRegistry()})
		assert.ErrorContains(t, err, "preparing the runtime directory")
	})
	t.Run("cannot listen", func(t *testing.T) {
		err := Run(context.Background(), Options{
			NoOpen: true, registry: runRegistry(),
			listen: func(context.Context, int, *slog.Logger) (*localweb.Bound, error) {
				return nil, errors.New("no sockets")
			},
		})
		assert.EqualError(t, err, "no sockets")
	})
	t.Run("the peer check is not supported", func(t *testing.T) {
		err := Run(context.Background(), Options{
			NoOpen: true, registry: runRegistry(),
			listen: func(ctx context.Context, port int, log *slog.Logger) (*localweb.Bound, error) {
				return openListener(ctx, port, func(net.Listener, func(error)) (net.Listener, error) {
					return nil, errors.ErrUnsupported
				}, log)
			},
		})
		assert.ErrorIs(t, err, errors.ErrUnsupported)
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
			listen: func(context.Context, int, *slog.Logger) (*localweb.Bound, error) {
				return &localweb.Bound{Listeners: []net.Listener{brokenListener{}}, Addrs: []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1")}}, nil
			},
		})
		assert.EqualError(t, err, "serve: accept: broken")
	})
}

// brokenListener fails to accept.
type brokenListener struct{}

func (brokenListener) Accept() (net.Conn, error) { return nil, errors.New("accept: broken") }
func (brokenListener) Close() error              { return nil }
func (brokenListener) Addr() net.Addr            { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
