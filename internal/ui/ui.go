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

	"github.com/GSI-HPC/sind/internal/ui/collect"
	"github.com/GSI-HPC/sind/internal/ui/demo"
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
	// Demo serves made-up clusters and plays the commands of the dialogs:
	// no Docker, no child process, nothing written. It does not record
	// itself in RuntimeDir, so that it never stands in for a real sind ui.
	Demo bool
	// Still makes the demo stand at demo.Epoch and play nothing, so that its
	// pages are the same on every run. It is for screenshots and needs Demo.
	Still bool
	// MonitoringInterval is how often the monitoring stacks are read
	// while a page is open, between collect.MinInterval and
	// collect.MaxInterval; 0 is collect.MonitoringEvery. The Monitoring
	// page changes it.
	MonitoringInterval time.Duration

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
	if o.Still && !o.Demo {
		return errors.New("--still needs --demo")
	}
	if o.MonitoringInterval == 0 {
		o.MonitoringInterval = collect.MonitoringEvery
	}
	if err := collect.CheckInterval(o.MonitoringInterval); err != nil {
		return fmt.Errorf("monitoring interval: %w", err)
	}
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
	if o.RuntimeDir != "" && !o.Demo {
		var err error
		if inst, err = newInstance(o.RuntimeDir); err != nil {
			return fmt.Errorf("preparing the runtime directory: %w", err)
		}
		inst.procRoot = o.procRoot
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
				case <-openBrowser(ctx, o.Exec, inst.dir, r.URL, log):
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
	cfg := serverConfig{Env: env, Token: token, Port: port, Registry: o.registry}
	if o.Demo {
		d := demo.New(env.Store, o.Still)
		env.Exec = d.Exec()
		services = demoServices(d, services)
		if o.Still {
			cfg.Now = d.Now
		}
	}
	for _, svc := range services {
		if svc.Setup == nil {
			continue
		}
		if err := svc.Setup(env); err != nil {
			return fmt.Errorf("starting %s: %w", svc.Name, err)
		}
	}
	srv, err := newServer(ctx, cfg)
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
	if o.Demo {
		_, _ = fmt.Fprintln(o.Stderr, "Demo: made-up clusters; nothing touches Docker.")
	}
	if !o.NoOpen {
		dir := ""
		if inst != nil {
			dir = inst.dir
		} else if dir, err = os.MkdirTemp("", "sind-ui-"); err != nil {
			log.Warn("could not make a directory for the page that opens sind ui; open the URL above yourself", "err", err)
		} else {
			// The browser reads the page while sind ui runs.
			defer func() { _ = os.RemoveAll(dir) }()
		}
		if dir != "" {
			openBrowser(ctx, o.Exec, dir, url, log)
		}
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
