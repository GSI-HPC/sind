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
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// Options configure Run.
type Options struct {
	// Port to listen on; 0 picks a free one.
	Port int
	// NoOpen keeps the browser closed; the URL is printed either way.
	NoOpen bool
	// RuntimeDir is $XDG_RUNTIME_DIR, where the running UI is recorded so
	// that a second sind ui opens it instead of starting another. Empty
	// records nothing.
	RuntimeDir string
	// Exec runs the external commands, such as the one that opens the
	// browser; nil is the real one.
	Exec cmdexec.Executor
	// Stderr gets the URL and the log; nil discards both.
	Stderr io.Writer

	// registry is what is served; nil is the default registry.
	registry *Registry
	// procRoot is the /proc that names the owners of connections; empty
	// is /proc.
	procRoot string
	// listen opens the listener; nil is listen.
	listen func(port int, procRoot string, log *slog.Logger) (net.Listener, error)
	// flock takes the lock of the runtime directory; nil is flock(2).
	flock func(fd, how int) error
}

// shutdownGrace is how long open requests get to finish at shutdown.
const shutdownGrace = 5 * time.Second

// Run serves sind ui until ctx ends. When another sind ui of the user runs,
// it opens that one's URL and returns.
func Run(ctx context.Context, o Options) error {
	if o.Exec == nil {
		o.Exec = &cmdexec.OSExecutor{}
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.registry == nil {
		o.registry = defaultRegistry
	}
	if o.procRoot == "" {
		o.procRoot = "/proc"
	}
	if o.listen == nil {
		o.listen = listen
	}
	log := slog.New(slog.NewTextHandler(o.Stderr, nil))

	var inst *instance
	if o.RuntimeDir != "" {
		var err error
		if inst, err = newInstance(o.RuntimeDir); err != nil {
			return fmt.Errorf("preparing the runtime directory: %w", err)
		}
		if o.flock != nil {
			inst.flock = o.flock
		}
		if err := inst.lock(); err != nil {
			return fmt.Errorf("locking the runtime directory: %w", err)
		}
		// The lock is dropped once the record is written, or on an error.
		defer inst.unlock()
		if r, ok := inst.running(); ok {
			_, _ = fmt.Fprintf(o.Stderr, "sind ui is already running: %s\n", r.URL)
			if !o.NoOpen {
				select {
				case <-openBrowser(ctx, o.Exec, inst, r.URL, log):
				case <-time.After(openWait):
				}
			}
			return nil
		}
	}

	l, err := o.listen(o.Port, o.procRoot, log)
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("port %d is in use; pick another with --port, or leave it out", o.Port)
	}
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port
	addr := "127.0.0.1:" + strconv.Itoa(port)
	token := newSecret()
	url := "http://" + addr + "/?token=" + token

	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	env := &Env{Store: store.New(), Exec: o.Exec, Log: log, Opts: o, Addr: addr}
	services := o.registry.snapshot().services
	for _, svc := range services {
		if svc.Setup == nil {
			continue
		}
		if err := svc.Setup(env); err != nil {
			return fmt.Errorf("starting %s: %w", svc.Name, err)
		}
	}
	srv, err := newServer(ctx, serverConfig{Env: env, Token: token, Port: port, Registry: o.registry})
	if err != nil {
		return err
	}
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	for _, svc := range services {
		if svc.Run == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Run(ctx, env)
		}()
	}

	if inst != nil {
		if err := inst.publish(runtimeRecord{URL: url, PID: os.Getpid()}); err != nil {
			return fmt.Errorf("recording the running sind ui: %w", err)
		}
		inst.unlock()
		defer inst.retire()
	}
	_, _ = fmt.Fprintf(o.Stderr, "sind ui: %s\nPress Ctrl-C to stop.\n", url)
	if !o.NoOpen {
		openBrowser(ctx, o.Exec, inst, url, log)
	}

	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(l) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	// The live update streams end with ctx; then the rest drains.
	cancel()
	sctx, scancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer scancel()
	return hs.Shutdown(sctx)
}

// openWait is how long a second sind ui waits for xdg-open before it
// returns, as xdg-open may stay until the browser ends.
var openWait = 3 * time.Second
