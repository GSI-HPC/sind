// SPDX-License-Identifier: LGPL-3.0-or-later

// Package run starts the sind commands that the dialogs of sind ui make, and
// keeps their tasks in the store while they run.
//
// A task's command is a child process of its own: the running sind binary,
// in a process group of its own, with no input and no progress display. Its
// progress comes from the activity log, where the child writes the same
// events that make a terminal's progress display: the runner hands the child
// a trace ID in TRACEPARENT, and the pages find the run with that trace.
// Stopping a task sends SIGINT to the group, as Ctrl-C does, so that sind
// rolls back and exits with 130.
package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/GSI-HPC/go-clikit/termtext"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

const (
	// outputKept is how much of the end of a command's standard error a
	// task keeps, in bytes.
	outputKept = 32 << 10
	// ExitInterrupted is the exit code of a sind that was interrupted and
	// rolled back.
	ExitInterrupted = 130
)

// ErrNotRunning is what Cancel returns for a task that is not running.
var ErrNotRunning = errors.New("the command is not running")

// ErrClosing is the error of Start once Shutdown has begun.
var ErrClosing = errors.New("sind ui is shutting down")

// Spec describes a command to run as a task.
type Spec struct {
	// Action is the id of the action that built the command.
	Action string
	// Argv is the command line; Argv[0] is "sind", which the runner
	// replaces by the running binary.
	Argv []string
	// Display is Argv quoted for a shell, as the dialog showed it.
	Display string
	// Realm and Cluster name what the command works on, empty for none.
	Realm   string
	Cluster string
}

// Starter starts tasks and stops them. The runner executes them.
type Starter interface {
	// Start starts the command in the background and returns the ID of its
	// task, which the store holds from now on.
	Start(s Spec) (string, error)
	// Cancel asks the running task to stop, as Ctrl-C does.
	Cancel(id string) error
	// Running returns how many tasks run.
	Running() int
	// Shutdown asks running tasks to stop and waits for them, up to grace,
	// then kills those left.
	Shutdown(grace time.Duration)
}

// Config configures a Runner.
type Config struct {
	// Exec runs the child.
	Exec  cmdexec.Executor
	Store *store.Store
	// Binary is the sind executable, which runs in place of "sind".
	Binary string
	// ActivityLog is the event log that the child appends to; empty for the
	// child's own default.
	ActivityLog string
	// Environ returns the environment the child's is made from; nil is
	// none, which the caller sets to os.Environ.
	Environ func() []string
	// Refresh is called with the realm of a task that has ended, for the
	// collectors to look again at what the command changed. It may be nil.
	Refresh func(realm string)
	// Kill sends sig to the process group of the child with the process ID
	// pid; nil is KillGroup.
	Kill func(pid int, sig syscall.Signal) error
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Random fills b with random bytes, for the IDs; nil is crypto/rand.
	Random func(b []byte)
}

// Runner executes tasks as child processes.
type Runner struct {
	cfg Config
	// ctx ends when Shutdown has run out of patience, which kills the
	// children that are left.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	running map[string]*child
	// closing is set once Shutdown has begun: no task starts after it.
	closing bool
	wg      sync.WaitGroup
}

// child is a running task's process.
type child struct {
	pid int
	// interrupted is set once the child got its SIGINT. sind takes the
	// first SIGINT as Ctrl-C and rolls back; a second one would kill it in
	// the middle of the rollback, so it never gets one.
	interrupted bool
}

// New returns a Runner for cfg.
func New(cfg Config) *Runner {
	if cfg.Kill == nil {
		cfg.Kill = KillGroup
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Random == nil {
		cfg.Random = func(b []byte) {
			// crypto/rand never fails; it ends the process when it cannot.
			_, _ = rand.Read(b)
		}
	}
	ctx, cancel := context.WithCancel(cmdexec.WithProcessGroup(context.Background()))
	return &Runner{cfg: cfg, ctx: ctx, cancel: cancel, running: map[string]*child{}}
}

// KillGroup sends sig to the process group whose leader has the ID pid.
func KillGroup(pid int, sig syscall.Signal) error { return syscall.Kill(-pid, sig) }

// Start implements Starter.
func (r *Runner) Start(s Spec) (string, error) {
	if len(s.Argv) == 0 || s.Argv[0] != "sind" {
		return "", errors.New("not a sind command")
	}
	trace, span := r.hex(16), r.hex(8)
	t := store.Task{
		ID: r.hex(8), Action: s.Action, Display: s.Display, Argv: s.Argv, Realm: s.Realm, Cluster: s.Cluster,
		Trace: trace, Start: r.cfg.Now(), Status: store.TaskRunning,
	}
	var environ []string
	if r.cfg.Environ != nil {
		environ = r.cfg.Environ()
	}
	env := childEnv(environ, r.cfg.ActivityLog, "00-"+trace+"-"+span+"-01")
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return "", ErrClosing
	}
	r.wg.Add(1)
	r.mu.Unlock()
	r.cfg.Store.AddTask(t)

	started := make(chan struct{})
	var once sync.Once
	onStart := func(pid int) {
		once.Do(func() {
			r.mu.Lock()
			late := r.closing
			r.running[t.ID] = &child{pid: pid, interrupted: late}
			r.mu.Unlock()
			if late {
				// Shutdown began while the child started.
				_ = r.cfg.Kill(pid, syscall.SIGINT)
			}
			close(started)
		})
	}
	ctx := cmdexec.WithStarted(cmdexec.WithEnv(r.ctx, env), onStart)

	done := make(chan struct{})
	go func() {
		defer r.wg.Done()
		defer close(done)
		_, stderr, err := r.cfg.Exec.Run(ctx, r.cfg.Binary, s.Argv[1:]...)
		r.end(t, stderr, err)
	}()
	// A task can be stopped from the moment Start returns.
	select {
	case <-started:
	case <-done:
	}
	return t.ID, nil
}

// end records how the child ended.
func (r *Runner) end(t store.Task, stderr string, err error) {
	r.mu.Lock()
	delete(r.running, t.ID)
	r.mu.Unlock()
	out := tail(stderr)
	exit, status := outcome(err)
	end := r.cfg.Now()
	r.cfg.Store.UpdateTask(t.ID, func(t *store.Task) {
		t.End, t.Output, t.Exit, t.Status = &end, out, exit, status
		if status == store.TaskFailed {
			t.Err = failure(out, err)
		}
	})
	if r.cfg.Refresh != nil {
		r.cfg.Refresh(t.Realm)
	}
}

// outcome returns the exit code and the task status of a command that ended
// with err: 0 is ok, 130 canceled and anything else failed; a command that
// could not be started or was killed has no exit code of its own, so it is
// -1.
func outcome(err error) (int, string) {
	if err == nil {
		return 0, store.TaskOK
	}
	exit := -1
	if ee, ok := errors.AsType[*cmdexec.ExitError](err); ok {
		exit = ee.ExitCode()
	}
	if exit == ExitInterrupted {
		return exit, store.TaskCanceled
	}
	return exit, store.TaskFailed
}

// failure says in one line why a command failed: the error line sind
// printed if it printed one, else how the process ended.
func failure(out string, err error) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if msg, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "Error:"); ok {
			if msg = strings.TrimSpace(msg); msg != "" {
				return msg
			}
		}
	}
	if ee, ok := errors.AsType[*cmdexec.ExitError](err); ok {
		return "sind " + ee.Err.String()
	}
	return err.Error()
}

// tail returns the end of stderr that a task keeps, escaped: from the start
// of its first whole line when the start was cut off.
func tail(stderr string) string {
	if over := len(stderr) - outputKept; over > 0 {
		stderr = stderr[over:]
		if _, rest, ok := strings.Cut(stderr, "\n"); ok {
			stderr = rest
		}
	}
	return termtext.EscapeLines(stderr)
}

// Cancel implements Starter: it sends SIGINT to the child's process group,
// so that sind stops, rolls back and exits with 130. It never kills.
func (r *Runner) Cancel(id string) error {
	r.mu.Lock()
	c, ok := r.running[id]
	again := ok && c.interrupted
	if ok {
		c.interrupted = true
	}
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("task %s: %w", id, ErrNotRunning)
	}
	if again {
		return nil // it is stopping already
	}
	r.cfg.Store.UpdateTask(id, func(t *store.Task) { t.Canceling = true })
	if err := r.cfg.Kill(c.pid, syscall.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stopping task %s: %w", id, err)
	}
	return nil
}

// Shutdown implements Starter: it interrupts the children, so that sind
// rolls back as it does for Ctrl-C, and kills those that have not ended
// after grace.
func (r *Runner) Shutdown(grace time.Duration) {
	r.interruptAll()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		// The context's end kills each child's group.
		r.cancel()
		<-done
	}
	r.cancel()
}

// interruptAll stops new tasks and sends SIGINT to the process group of
// every running child that has not had one yet.
func (r *Runner) interruptAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closing = true
	for _, c := range r.running {
		if !c.interrupted {
			c.interrupted = true
			_ = r.cfg.Kill(c.pid, syscall.SIGINT)
		}
	}
}

// Running returns how many children run.
func (r *Runner) Running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

// hex returns n random bytes as hexadecimal digits.
func (r *Runner) hex(n int) string {
	b := make([]byte, n)
	r.cfg.Random(b)
	return hex.EncodeToString(b)
}

// childEnv returns env for a sind child: no progress display, the event log
// sind ui reads, the trace that tells the child's run with the tracestate
// that marks it as a run of sind ui (store.UITraceState), and no SIND_REALM:
// the dialog's command names any realm but the default one on its command
// line, and without a name must run in the default realm, whatever the
// environment of sind ui says. The environment does not change the command
// line, so what the dialog showed still holds.
func childEnv(env []string, activityLog, traceparent string) []string {
	out := make([]string, 0, len(env)+4)
	for _, kv := range env {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "SIND_PROGRESS", "SIND_PROGRESS_LOG", "SIND_REALM", "TRACEPARENT", "TRACESTATE":
		default:
			out = append(out, kv)
		}
	}
	out = append(out, "SIND_PROGRESS=none", "TRACEPARENT="+traceparent, "TRACESTATE="+store.UITraceState)
	if activityLog != "" {
		out = append(out, "SIND_PROGRESS_LOG="+activityLog)
	}
	return out
}
