// SPDX-License-Identifier: LGPL-3.0-or-later

package run

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// call is what the fake executor was asked to run.
type call struct {
	name string
	args []string
	env  []string
}

// fakeExec is an executor whose commands end when the test says: a command
// reports its process ID as the real executor does, then waits for a result.
type fakeExec struct {
	mu      sync.Mutex
	calls   []call
	pid     int
	noStart error
	// gate, when set, holds the start of a command until it is closed.
	gate    chan struct{}
	results chan result
}

type result struct {
	stderr string
	err    error
}

func newFakeExec() *fakeExec { return &fakeExec{pid: 4242, results: make(chan result, 8)} }

func (f *fakeExec) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{name, args, cmdexec.Env(ctx)})
	f.mu.Unlock()
	if f.noStart != nil {
		return "", "", f.noStart
	}
	if f.gate != nil {
		<-f.gate
	}
	if started := cmdexec.Started(ctx); started != nil {
		started(f.pid)
		started(f.pid) // reported again: harmless
	}
	select {
	case r := <-f.results:
		return "", r.stderr, r.err
	case <-ctx.Done():
		return "", "killed", errors.New("signal: killed")
	}
}

func (f *fakeExec) RunWithStdin(context.Context, io.Reader, string, ...string) (string, string, error) {
	panic("not used")
}

func (f *fakeExec) Start(context.Context, string, ...string) (*cmdexec.Process, error) {
	panic("not used")
}

func (f *fakeExec) lastCall(t *testing.T) call {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls)
	return f.calls[len(f.calls)-1]
}

// exitErr returns the error of a command that exited with code, by running
// a shell that does.
func exitErr(t *testing.T, code string, stderr string) error {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit "+code)
	err := cmd.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	return cmdexec.WrapExitError(ee, stderr)
}

type kill struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (k *kill) fn(_ int, sig syscall.Signal) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, sig.String())
	return k.err
}

func (k *kill) got() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.calls...)
}

type fixture struct {
	r       *Runner
	ex      *fakeExec
	st      *store.Store
	kill    *kill
	refresh chan string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{ex: newFakeExec(), st: store.New(), kill: &kill{}, refresh: make(chan string, 4)}
	n := byte(0)
	f.r = New(Config{
		Exec: f.ex, Store: f.st, Binary: "/usr/bin/sind", ActivityLog: "/state/activity.jsonl",
		Environ: func() []string {
			return []string{"PATH=/bin", "SIND_REALM=ci", "SIND_PROGRESS=plain", "SIND_PROGRESS_LOG=/x", "TRACEPARENT=old", "TRACESTATE=s"}
		},
		Refresh: func(realm string) { f.refresh <- realm },
		Kill:    f.kill.fn,
		Random: func(b []byte) {
			for i := range b {
				n++
				b[i] = n
			}
		},
	})
	t.Cleanup(func() { f.r.Shutdown(time.Second) })
	return f
}

func (f *fixture) waitDone(t *testing.T, id string) store.Task {
	t.Helper()
	require.Eventually(t, func() bool {
		task, ok := f.st.Snapshot().Task(id)
		return ok && task.Done()
	}, 5*time.Second, 5*time.Millisecond)
	task, _ := f.st.Snapshot().Task(id)
	return task
}

var spec = Spec{
	Action: "power", Argv: []string{"sind", "--realm", "ci", "power", "on", "worker-0.dev"},
	Display: "sind --realm ci power on worker-0.dev", Realm: "ci", Cluster: "dev",
}

const (
	// The fixture's random bytes count up, so the IDs are known.
	wantTrace = "0102030405060708090a0b0c0d0e0f10"
	wantSpan  = "1112131415161718"
	wantID    = "191a1b1c1d1e1f20"
)

func TestStart_RunsTheBinaryWithTheEnvironment(t *testing.T) {
	f := newFixture(t)

	id, err := f.r.Start(spec)

	require.NoError(t, err)
	assert.Equal(t, wantID, id)
	assert.Equal(t, 1, f.r.Running())
	task, ok := f.st.Snapshot().Task(id)
	require.True(t, ok)
	assert.Equal(t, store.TaskRunning, task.Status)
	assert.Equal(t, "power", task.Action)
	assert.Equal(t, spec.Display, task.Display)
	assert.Equal(t, "ci", task.Realm)
	assert.Equal(t, "dev", task.Cluster)
	assert.Equal(t, wantTrace, task.Trace)
	c := f.ex.lastCall(t)
	assert.Equal(t, "/usr/bin/sind", c.name)
	assert.Equal(t, spec.Argv[1:], c.args)
	assert.Equal(t, []string{
		"PATH=/bin", "SIND_PROGRESS=none", "TRACEPARENT=00-" + wantTrace + "-" + wantSpan + "-01",
		"SIND_PROGRESS_LOG=/state/activity.jsonl",
	}, c.env)
	f.ex.results <- result{}
	f.waitDone(t, id)
}

func TestStart_Refusals(t *testing.T) {
	f := newFixture(t)
	for _, argv := range [][]string{nil, {"docker", "ps"}} {
		_, err := f.r.Start(Spec{Argv: argv})
		require.EqualError(t, err, "not a sind command")
	}
	assert.Empty(t, f.st.Snapshot().Tasks)
}

func TestTask_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result func(t *testing.T) result
		status string
		exit   int
		err    string
	}{
		{"ok", func(*testing.T) result { return result{stderr: "warning\n"} }, store.TaskOK, 0, ""},
		{"failed", func(t *testing.T) result {
			out := "Warning: x\nError: no such cluster dev\n"
			return result{stderr: out, err: exitErr(t, "1", out)}
		}, store.TaskFailed, 1, "no such cluster dev"},
		{"failed without an error line", func(t *testing.T) result {
			return result{stderr: "boom\n", err: exitErr(t, "2", "boom\n")}
		}, store.TaskFailed, 2, "sind exit status 2"},
		{"empty error line", func(t *testing.T) result {
			return result{stderr: "Error:\n", err: exitErr(t, "3", "Error:\n")}
		}, store.TaskFailed, 3, "sind exit status 3"},
		{"canceled", func(t *testing.T) result {
			return result{stderr: "interrupted\n", err: exitErr(t, "130", "interrupted\n")}
		}, store.TaskCanceled, 130, ""},
		{"not started", func(*testing.T) result { return result{err: errors.New("fork/exec: no such file")} }, store.TaskFailed, -1, "fork/exec: no such file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			res := tc.result(t)
			id, err := f.r.Start(spec)
			require.NoError(t, err)

			f.ex.results <- res
			task := f.waitDone(t, id)

			assert.Equal(t, tc.status, task.Status)
			assert.Equal(t, tc.exit, task.Exit)
			assert.Equal(t, tc.err, task.Err)
			assert.Equal(t, strings.TrimSpace(res.stderr), strings.TrimSpace(task.Output))
			assert.NotNil(t, task.End)
			assert.Equal(t, "ci", <-f.refresh)
			assert.Equal(t, 0, f.r.Running())
		})
	}
}

func TestStart_ACommandThatDoesNotStart(t *testing.T) {
	f := newFixture(t)
	f.ex.noStart = errors.New("exec: not found")

	id, err := f.r.Start(spec)

	require.NoError(t, err)
	task := f.waitDone(t, id)
	assert.Equal(t, store.TaskFailed, task.Status)
	assert.Equal(t, "exec: not found", task.Err)
}

func TestTask_OutputIsEscaped(t *testing.T) {
	f := newFixture(t)
	id, err := f.r.Start(spec)
	require.NoError(t, err)

	f.ex.results <- result{stderr: "esc \x1b[31mred\n"}
	task := f.waitDone(t, id)

	assert.NotContains(t, task.Output, "\x1b")
	assert.Contains(t, task.Output, "red")
}

func TestTail(t *testing.T) {
	assert.Equal(t, "a\nb", tail("a\nb"))
	// Cut inside a line: the rest of that line goes too.
	assert.Equal(t, "tail\n", tail(strings.Repeat("x", outputKept)+"\ntail\n"))
	// Cut with no line break left keeps what there is.
	assert.Equal(t, strings.Repeat("y", outputKept), tail(strings.Repeat("y", outputKept+10)))
}

func TestCancel_InterruptsTheGroup(t *testing.T) {
	f := newFixture(t)
	id, err := f.r.Start(spec)
	require.NoError(t, err)

	require.NoError(t, f.r.Cancel(id))

	assert.Equal(t, []string{"interrupt"}, f.kill.got())
	task, _ := f.st.Snapshot().Task(id)
	assert.True(t, task.Canceling)
	assert.Equal(t, store.TaskRunning, task.Status, "the task ends when sind has rolled back")
	f.ex.results <- result{err: exitErr(t, "130", "")}
	assert.Equal(t, store.TaskCanceled, f.waitDone(t, id).Status)
}

func TestCancel_Errors(t *testing.T) {
	f := newFixture(t)

	err := f.r.Cancel("nope")
	require.ErrorIs(t, err, ErrNotRunning)

	id, _ := f.r.Start(spec)
	f.kill.err = syscall.EPERM
	err = f.r.Cancel(id)
	require.ErrorIs(t, err, syscall.EPERM)
	f.ex.results <- result{}
	f.waitDone(t, id)

	id, _ = f.r.Start(spec)
	f.kill.err = syscall.ESRCH // gone between the check and the signal
	require.NoError(t, f.r.Cancel(id))
	f.ex.results <- result{}
	f.waitDone(t, id)
}

func TestCancel_InterruptsOnce(t *testing.T) {
	f := newFixture(t)
	id, err := f.r.Start(spec)
	require.NoError(t, err)

	// A second Stop, and then the shutdown of sind ui, send no second
	// SIGINT: it would kill sind in the middle of its rollback.
	require.NoError(t, f.r.Cancel(id))
	require.NoError(t, f.r.Cancel(id))
	go func() {
		time.Sleep(20 * time.Millisecond)
		f.ex.results <- result{err: exitErr(t, "130", "")}
	}()
	f.r.Shutdown(5 * time.Second)
	assert.Equal(t, []string{"interrupt"}, f.kill.got())
	task, _ := f.st.Snapshot().Task(id)
	assert.Equal(t, store.TaskCanceled, task.Status)
}

func TestShutdown_InterruptsAChildThatStartsLate(t *testing.T) {
	f := newFixture(t)
	f.ex.gate = make(chan struct{})
	go func() { _, _ = f.r.Start(spec) }()
	require.Eventually(t, func() bool { return len(f.st.Snapshot().Tasks) == 1 }, time.Second, time.Millisecond)
	shut := make(chan struct{})
	go func() { f.r.Shutdown(5 * time.Second); close(shut) }()
	require.Eventually(t, func() bool {
		f.r.mu.Lock()
		defer f.r.mu.Unlock()
		return f.r.closing
	}, time.Second, time.Millisecond)
	close(f.ex.gate)
	require.Eventually(t, func() bool { return len(f.kill.got()) == 1 }, time.Second, time.Millisecond)
	f.ex.results <- result{err: exitErr(t, "130", "")}
	<-shut
	assert.Equal(t, []string{"interrupt"}, f.kill.got())
}

func TestStart_RefusedOnceShutdownBegan(t *testing.T) {
	f := newFixture(t)
	f.r.Shutdown(time.Second)
	_, err := f.r.Start(spec)
	require.ErrorIs(t, err, ErrClosing)
	assert.Empty(t, f.st.Snapshot().Tasks)
}

func TestShutdown_InterruptsAndWaits(t *testing.T) {
	f := newFixture(t)
	id, _ := f.r.Start(spec)

	go func() {
		time.Sleep(20 * time.Millisecond)
		f.ex.results <- result{err: exitErr(t, "130", "")}
	}()
	f.r.Shutdown(5 * time.Second)

	assert.Equal(t, []string{"interrupt"}, f.kill.got())
	task, _ := f.st.Snapshot().Task(id)
	assert.Equal(t, store.TaskCanceled, task.Status)
}

func TestShutdown_KillsAfterTheGrace(t *testing.T) {
	f := newFixture(t)
	id, _ := f.r.Start(spec)

	f.r.Shutdown(10 * time.Millisecond)

	task, _ := f.st.Snapshot().Task(id)
	assert.Equal(t, store.TaskFailed, task.Status, "the fake reports the kill as its context ending")
	assert.Equal(t, "signal: killed", task.Err)
	assert.Equal(t, 0, f.r.Running())
}

func TestKillGroup(t *testing.T) {
	// No process group has this ID.
	err := KillGroup(1<<30, syscall.SIGINT)
	assert.ErrorIs(t, err, syscall.ESRCH)
}

func TestNew_Defaults(t *testing.T) {
	st := store.New()
	r := New(Config{Exec: newFakeExec(), Store: st})
	t.Cleanup(func() { r.Shutdown(time.Second) })
	assert.Equal(t, 16, len(r.hex(8)))
	assert.NotEqual(t, r.hex(8), r.hex(8))
	assert.WithinDuration(t, time.Now(), r.cfg.Now(), time.Minute)
	assert.NotNil(t, r.cfg.Kill)
}

func TestChildEnv(t *testing.T) {
	got := childEnv([]string{"A=1", "SIND_REALM=x", "TRACESTATE=y", "B=2=3"}, "", "00-t-s-01")
	assert.Equal(t, []string{"A=1", "B=2=3", "SIND_PROGRESS=none", "TRACEPARENT=00-t-s-01"}, got)
}
