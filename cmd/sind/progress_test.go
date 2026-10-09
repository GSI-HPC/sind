// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/cliprogress"
	"github.com/GSI-HPC/go-clikit/progress/display"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// displays stands in for the displays of the commands a test runs: each is
// made without being started (cliprogress.Options.Manual), and drawn only
// when the test calls draw, on a clock that moves a second each time, which
// the displays' Bus reads too.
//
// Adapted from GSI-HPC/clusterctl internal/cli/display_test.go.
type displays struct {
	mu    sync.Mutex
	modes []string
	last  *cliprogress.Run
	clock time.Time
}

// fakeDisplays has the displays of the commands a test runs drawn by hand.
func fakeDisplays(t *testing.T) *displays {
	t.Helper()
	c := &displays{clock: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	t.Setenv("LC_ALL", "en_US.UTF-8") // the marks of the frames the tests expect
	saved, savedClock := startRun, displayClock
	startRun = func(ctx context.Context, o cliprogress.Options) (*cliprogress.Run, error) {
		o.Manual = true
		o.Now = c.now
		// The frames compare plain text; TestTheDisplaysDrawClassic
		// covers the theme.
		o.Theme = display.Theme{}
		r, err := cliprogress.Start(ctx, o)
		if err == nil && r.Mode() != cliprogress.ModeNone {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.modes = append(c.modes, r.Mode().String())
			c.last = r
		}
		return r, err
	}
	displayClock = c.now
	t.Cleanup(func() { startRun, displayClock = saved, savedClock })
	return c
}

func (c *displays) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock
}

// draw moves the clock on by a second and draws the display made last.
func (c *displays) draw() {
	c.mu.Lock()
	c.clock = c.clock.Add(time.Second)
	shown := c.last
	c.mu.Unlock()
	if shown != nil {
		shown.Draw()
	}
}

// shown returns the modes of the displays made, in the order they were.
func (c *displays) shown() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.modes, ",")
}

// onTerminals makes terminals of the streams given, and of nothing else:
// 160 columns wide and 40 rows high, with sind in their foreground. A
// stream that is a pipe is one only when pipe says so. Neither progress
// variable is set: the MCP server tests leave SIND_PROGRESS=none behind
// (withoutProgress).
func onTerminals(t *testing.T, pipe func(io.Writer) bool, terminals ...io.Writer) {
	t.Helper()
	t.Setenv(envProgress, "")
	t.Setenv(envProgressLog, "")
	savedTerminal, savedPipe, savedSize, savedForeground := onTerminal, intoPipe, terminalSize, inForeground
	onTerminal = func(w io.Writer) bool { return slices.Contains(terminals, w) }
	intoPipe = func(w io.Writer) bool { return pipe != nil && pipe(w) }
	terminalSize = func(io.Writer) (int, int, error) { return screenWidth, 40, nil }
	inForeground = func(io.Writer) bool { return true }
	t.Cleanup(func() {
		onTerminal, intoPipe, terminalSize, inForeground = savedTerminal, savedPipe, savedSize, savedForeground
	})
}

// screenWidth is how many columns the terminals of onTerminals have.
const screenWidth = 160

// executeOn runs a command line with ctx, writing to stdout and stderr, as
// cobra does without run's final error line.
func executeOn(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	cmd := NewRootCommand()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	cmd.SetContext(ctx)
	return cmd.Execute()
}

// timestamps matches the time the logger writes in front of a record.
var timestamps = regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d\.\d{3} `)

// withoutTimestamps returns s with the logger's timestamps written as
// HH:MM:SS.mmm.
func withoutTimestamps(s string) string {
	return timestamps.ReplaceAllString(s, "HH:MM:SS.mmm ")
}

// The live tree is drawn where it can be: auto draws it on a terminal that
// is not a dumb one while stdout goes into no pipe, and nowhere else; tty
// and counter from the flag insist on a terminal, plain lines need none,
// and none draws nothing. The variable fails no command: what it asks for
// where it cannot be drawn, or does not name, draws nothing. --progress
// wins over the variable, wherever it stands on the command line. A Bus the
// context brought, as a test's, draws nothing of its own.
func TestTheProgressDisplayIsDrawnWhereItCan(t *testing.T) {
	const ownErr = "--count must be at least 1, got 0"
	isPipe := func(io.Writer) bool { return true }
	for _, tc := range []struct {
		name     string
		terminal bool
		pipe     func(io.Writer) bool
		term     string
		env      string
		args     []string
		watched  bool
		shown    string
		msg      string
	}{
		{"auto on a terminal", true, nil, "xterm", "", nil, false, "tty", ownErr},
		{"auto without a terminal", false, nil, "xterm", "", nil, false, "", ownErr},
		{"auto on a dumb terminal", true, nil, "dumb", "", nil, false, "", ownErr},
		{"auto while stdout goes into a pipe", true, isPipe, "xterm", "", nil, false, "", ownErr},
		{"tty on a terminal", true, nil, "xterm", "", []string{"--progress", "tty"}, false, "tty", ownErr},
		{"tty while stdout goes into a pipe", true, isPipe, "xterm", "", []string{"--progress", "tty"}, false, "tty", ownErr},
		{"tty without a terminal", false, nil, "xterm", "", []string{"--progress", "tty"}, false, "",
			"--progress asks for a live tree, but standard error is not a terminal; use none, or auto to draw one only where it can be"},
		{"tty on a dumb terminal", true, nil, "dumb", "", []string{"--progress=tty"}, false, "",
			"--progress asks for a live tree, but the terminal cannot draw one (TERM is dumb); use none, or auto to draw one only where it can be"},
		{"counter on a terminal", true, nil, "xterm", "", []string{"--progress", "counter"}, false, "counter", ownErr},
		{"counter without a terminal", false, nil, "xterm", "", []string{"--progress", "counter"}, false, "",
			"--progress asks for a counter, but standard error is not a terminal; use none, or auto to draw one only where it can be"},
		{"plain without a terminal", false, nil, "xterm", "", []string{"--progress", "plain"}, false, "plain", ownErr},
		{"plain on a dumb terminal", true, nil, "dumb", "", []string{"--progress=plain"}, false, "plain", ownErr},
		{"none on a terminal", true, nil, "xterm", "", []string{"--progress", "none"}, false, "", ownErr},
		{"the variable", true, nil, "xterm", "none", nil, false, "", ownErr},
		{"the variable for a counter", true, nil, "xterm", "counter", nil, false, "counter", ownErr},
		{"the variable without a terminal", false, nil, "xterm", "tty", nil, false, "", ownErr},
		{"the variable for a counter on a dumb terminal", true, nil, "dumb", "counter", nil, false, "", ownErr},
		{"the variable for plain lines without a terminal", false, nil, "xterm", "plain", nil, false, "plain", ownErr},
		{"the flag over the variable", true, nil, "xterm", "counter", []string{"--progress", "none"}, false, "", ownErr},
		{"the flag after the command", true, nil, "xterm", "", []string{"create", "worker", "--progress", "counter"}, false, "counter", ownErr},
		{"a value it does not know", true, nil, "xterm", "", []string{"--progress", "tree"}, false, "",
			`--progress is "tree"; it takes one of auto, tty, counter, plain, none`},
		{"an empty value", true, nil, "xterm", "", []string{"--progress="}, false, "",
			`--progress is ""; it takes one of auto, tty, counter, plain, none`},
		{"a variable it does not know", true, nil, "xterm", "fancy", nil, false, "", ownErr},
		{"a Bus from the context", true, nil, "xterm", "tty", []string{"--progress", "tty"}, true, "", ownErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fakeDisplays(t)
			var stdout, stderr bytes.Buffer
			if tc.terminal {
				onTerminals(t, tc.pipe, &stderr)
			} else {
				onTerminals(t, tc.pipe)
			}
			t.Setenv("TERM", tc.term)
			t.Setenv(envProgress, tc.env)
			ctx := context.Background()
			var w *progresstest.Watcher
			if tc.watched {
				ctx, w = progresstest.Watch(ctx, t, progresstest.Classify(progressClass(ctx)))
			}
			args := tc.args
			if !slices.Contains(args, "worker") {
				args = append(slices.Clone(args), "create", "worker")
			}
			err := executeOn(ctx, &stdout, &stderr, append(args, "--count", "0")...)

			require.Error(t, err)
			assert.True(t, isUsageError(err), "a usage error: %v", err)
			assert.Equal(t, tc.msg, err.Error())
			assert.Equal(t, tc.shown, c.shown(), "displays made")
			assert.Empty(t, stdout.String())
			if w != nil {
				assert.Equal(t, "command create worker: failed (usage): "+ownErr+"\n", w.Finish())
			}
		})
	}
}

// Every command refuses a --progress it could not show, before it runs,
// as checkRealmFlag refuses a --realm: one that names no way to show
// progress, or a tree or a counter where neither can be drawn, even for a
// command that shows none. The variable fails none of them, and says
// nothing for a command that shows no progress.
func TestEveryCommandChecksTheProgressFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		args []string
		msg  string
	}{
		{"a value it does not know", "", []string{"--progress", "bogus", "version"},
			`--progress is "bogus"; it takes one of auto, tty, counter, plain, none`},
		{"a tree without a terminal", "", []string{"--progress", "tty", "get", "realms"},
			"--progress asks for a live tree, but standard error is not a terminal; use none, or auto to draw one only where it can be"},
		{"a counter without a terminal", "", []string{"get", "realms", "--progress=counter"},
			"--progress asks for a counter, but standard error is not a terminal; use none, or auto to draw one only where it can be"},
		// exec parses its flags itself, after PersistentPreRunE.
		{"a value it does not know after exec", "", []string{"exec", "--progress", "bogus", "--", "true"},
			`--progress is "bogus"; it takes one of auto, tty, counter, plain, none`},
		{"a tree without a terminal after exec", "", []string{"exec", "--progress=tty", "--", "true"},
			"--progress asks for a live tree, but standard error is not a terminal; use none, or auto to draw one only where it can be"},
		{"plain lines", "", []string{"--progress", "plain", "version"}, ""},
		{"the variable", "bogus", []string{"version"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onTerminals(t, nil)
			t.Setenv(envProgress, tc.env)
			calls := 0
			ctx := withClient(t.Context(), docker.NewClient(&mock.Executor{OnCall: func([]string, string) mock.Result {
				calls++
				return mock.Result{}
			}}))
			var stdout, stderr bytes.Buffer

			err := executeOn(ctx, &stdout, &stderr, tc.args...)

			if tc.msg == "" {
				require.NoError(t, err)
				assert.Empty(t, stderr.String())
				return
			}
			require.Error(t, err)
			assert.True(t, isUsageError(err), "a usage error: %v", err)
			assert.Equal(t, tc.msg, err.Error())
			assert.Empty(t, stdout.String())
			assert.Zero(t, calls, "nothing runs")
		})
	}
}

// A value of the variable that names no display is said once on stderr,
// escaped, and the command runs as it would with none; one that asks for a
// display where it cannot be drawn is not said at all, since the CI step
// that inherited it never asked.
func TestTheVariableFailsNoCommand(t *testing.T) {
	onTerminals(t, nil)
	for _, tc := range []struct {
		name, env, note string
	}{
		{"a value it does not know", "plian",
			`sind: SIND_PROGRESS is "plian"; it takes one of auto, tty, counter, plain, none; no progress is shown` + "\n"},
		{"a value with a control character", "\x1b[2Jtty",
			`sind: SIND_PROGRESS is "\x1b[2Jtty"; it takes one of auto, tty, counter, plain, none; no progress is shown` + "\n"},
		{"a tree without a terminal", "tty", ""},
		{"a counter without a terminal", "counter", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fakeDisplays(t)
			var quiet bytes.Buffer
			t.Setenv(envProgress, "")
			quietErr := executeOn(context.Background(), io.Discard, &quiet, "--progress=none", "create", "worker", "--count", "0")
			t.Setenv(envProgress, tc.env)
			var stderr bytes.Buffer
			err := executeOn(context.Background(), io.Discard, &stderr, "create", "worker", "--count", "0")

			assert.Equal(t, quietErr, err)
			assert.Empty(t, c.shown())
			assert.Equal(t, tc.note+quiet.String(), stderr.String())
		})
	}
}

// powerCutFails returns a context whose docker client fails every call,
// as a daemon that is not running does, after calling before.
func powerCutFails(ctx context.Context, t *testing.T, before func()) context.Context {
	t.Helper()
	exit1 := testutil.ExitCode1(t)
	return withClient(ctx, docker.NewClient(&mock.Executor{OnCall: func([]string, string) mock.Result {
		if before != nil {
			before()
		}
		return mock.Result{Stderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n", Err: exit1}
	}}))
}

// Without a display, stderr is what it was before sind showed progress,
// byte for byte, whatever asks for none: auto on a stderr that is no
// terminal, the variable asking for a tree there, or none itself; and an
// event log, which writes nothing on stderr, changes nothing either. -vvv
// has the logger write too.
func TestProgressLeavesStderrAloneWithoutADisplay(t *testing.T) {
	onTerminals(t, nil)
	c := fakeDisplays(t)
	args := []string{"-vvv", "power", "cut", "worker-0"}
	stderrOf := func(t *testing.T, extra ...string) (int, string) {
		t.Helper()
		var stderr bytes.Buffer
		code := run(powerCutFails(t.Context(), t, nil), append(slices.Clone(extra), args...), &stderr)
		return code, withoutTimestamps(stderr.String())
	}
	wantCode, want := stderrOf(t, "--progress=none")
	require.Equal(t, exitFailure, wantCode)
	require.Contains(t, want, "TRAC")
	require.Contains(t, want, "ERRO listing containers")

	t.Run("auto", func(t *testing.T) {
		code, got := stderrOf(t)
		assert.Equal(t, wantCode, code)
		assert.Equal(t, want, got)
	})
	t.Run("the variable asks for a tree", func(t *testing.T) {
		t.Setenv(envProgress, "tty")
		code, got := stderrOf(t)
		assert.Equal(t, wantCode, code)
		assert.Equal(t, want, got)
	})
	t.Run("an event log", func(t *testing.T) {
		code, got := stderrOf(t, "--progress-log", filepath.Join(t.TempDir(), "events.jsonl"))
		assert.Equal(t, wantCode, code)
		assert.Equal(t, want, got)
	})
	assert.Empty(t, c.shown(), "displays made")
}

// On a terminal the live tree is drawn: the command and how long it has
// run, and the docker command it waits for once that has run for a
// second. Once the command has failed the tree is gone, and what is left
// is the summary and then the error line.
func TestTheTreeIsDrawnOnATerminal(t *testing.T) {
	t.Setenv("TERM", "xterm")
	c := fakeDisplays(t)
	screen := &progresstest.Screen{Width: screenWidth}
	onTerminals(t, nil, screen)
	var frames []string
	ctx := powerCutFails(t.Context(), t, func() {
		c.draw()
		frames = append(frames, screen.String())
	})

	code := run(ctx, []string{"power", "cut", "worker-0"}, screen)

	assert.Equal(t, exitFailure, code)
	assert.Equal(t, []string{"power cut · 0:01.0\n  docker ps  1.0s\n"}, frames)
	assert.Equal(t, `sind: power cut: failed in 1.0s
HH:MM:SS.mmm ERRO listing containers: exit status 1: Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?
`, withoutTimestamps(screen.String()))
}

// A command that succeeds leaves no summary: the tree comes off the
// terminal, and exit status 0 says the rest.
func TestTheTreeLeavesNothingOnSuccess(t *testing.T) {
	t.Setenv("TERM", "xterm")
	c := fakeDisplays(t)
	screen := &progresstest.Screen{Width: screenWidth}
	onTerminals(t, nil, screen)
	var frames []string
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		c.draw()
		frames = append(frames, screen.String())
		if args[0] == "ps" {
			return mock.Result{Stdout: `{"ID":"c1","Names":"sind-default-worker-0","State":"running"}` + "\n"}
		}
		return mock.Result{Stdout: "sind-default-worker-0\n"}
	}}

	code := run(withClient(t.Context(), docker.NewClient(m)), []string{"power", "cut", "worker-0"}, screen)

	assert.Equal(t, exitOK, code)
	assert.Equal(t, []string{"power cut · 0:01.0\n  docker ps  1.0s\n", "power cut · 0:02.0\n  docker kill  1.0s\n"}, frames)
	assert.Empty(t, screen.String())
}

// An event log written to the terminal the tree is drawn on, as
// --progress-log /dev/stderr writes it, is written whole, each line above
// the tree's line that stays.
func TestAnEventLogOnTheTerminalOfTheTree(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("TRACEPARENT", "")
	t.Setenv("TRACESTATE", "")
	c := fakeDisplays(t)
	stderr, err := os.OpenFile(filepath.Join(t.TempDir(), "terminal"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	require.NoError(t, err)
	defer func() { _ = stderr.Close() }()
	onTerminals(t, nil, stderr)
	m := &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		c.draw()
		if args[0] == "ps" {
			return mock.Result{Stdout: `{"ID":"c1","Names":"sind-default-worker-0","State":"running"}` + "\n"}
		}
		return mock.Result{Stdout: "sind-default-worker-0\n"}
	}}

	code := run(withClient(t.Context(), docker.NewClient(m)),
		[]string{"--progress-log", "/dev/fd/" + strconv.Itoa(int(stderr.Fd())), "power", "cut", "worker-0"}, stderr)

	assert.Equal(t, exitOK, code)
	written, err := os.ReadFile(stderr.Name())
	require.NoError(t, err)
	screen := &progresstest.Screen{Width: 1000}
	_, err = screen.Write(written)
	require.NoError(t, err)
	var events int
	for _, line := range strings.Split(strings.TrimSuffix(screen.String(), "\n"), "\n") {
		if line == "✓ killing  1.0s  1 ok" {
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event), "a line of the log is whole: %q in\n%s", line, screen.String())
		events++
	}
	// The trace, and the command's start and end at least.
	assert.GreaterOrEqual(t, events, 3, screen.String())
}

// The wait for the realm lock is drawn with the holder it waits for, under
// the warning that names it too, which stays, and beside it the docker
// command that polls the lock. Once the command is interrupted the tree
// says so, and the summary says it was canceled.
func TestTheTreeShowsTheWaitForTheRealmLock(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	c := fakeDisplays(t)
	screen := &progresstest.Screen{Width: screenWidth}
	onTerminals(t, nil, screen)
	d := newLockDaemon(t)
	d.add("sind-lock", map[string]string{
		"sind.lock.token":   "x",
		"sind.lock.command": "sind create cluster",
		"sind.lock.host":    "elsewhere",
		"sind.lock.pid":     "7",
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var frames []string
	calls := 0
	ctx = withClient(ctx, docker.NewClient(&mock.Executor{OnCall: func(args []string, stdin string) mock.Result {
		calls++
		if calls == 4 {
			cancel()
		}
		if calls > 2 {
			c.draw()
			frames = append(frames, screen.String())
		}
		return d.onCall(args, stdin)
	}}))

	code := run(ctx, []string{"delete", "cluster"}, screen)

	assert.Equal(t, exitInterrupted, code)
	since := time.Date(2026, 10, 4, 10, 2, 3, 0, time.UTC).Local().Format(time.DateTime)
	warning := `Warning: waiting for another sind command in realm "sind" to finish: sind create cluster (pid 7 on elsewhere, since ` + since + ")\n"
	holder := "sind create cluster (pid 7 on elsewhere, since " + since + ")"
	want := []string{
		warning + "delete cluster · 0:01.0\n  realm lock  1.0s  " + holder + "\n  docker network create  1.0s\n",
		warning + "delete cluster · interrupting · 0:02.0\n  realm lock  2.0s  " + holder + "\n  docker network inspect  1.0s\n",
	}
	assert.Equal(t, want, frames)
	assert.Equal(t, warning+`sind: delete cluster: canceled in 2.0s
HH:MM:SS.mmm ERRO interrupted: taking the realm lock on the Docker daemon: context canceled
`, withoutTimestamps(screen.String()))
}

// The wait for the realm lock is a span of its own, from the first wait to
// the end: the file lock another process holds, whose holder is not known,
// and then the daemon lock, whose holder its message names.
func TestAcquireRealmLock_WaitIsASpan(t *testing.T) {
	stateHome := t.TempDir()
	first := newLockDaemon(t)
	unlock, err := acquireRealmLock(first.withClient(t.Context()), "spans", stateHome)
	require.NoError(t, err)

	other := newLockDaemon(t)
	other.add("spans-lock", map[string]string{
		"sind.lock.token":   "x",
		"sind.lock.command": "sind create cluster",
		"sind.lock.host":    "elsewhere",
		"sind.lock.pid":     "7",
	})
	ctx, w := progresstest.Watch(t.Context(), t)
	var stderr syncBuffer
	ctx, cancel := context.WithCancel(other.withClient(withStderr(ctx, &stderr)))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := acquireRealmLock(ctx, "spans", stateHome)
		done <- err
	}()

	require.Eventually(t, func() bool { return strings.Count(stderr.String(), "Warning:") == 1 }, 5*time.Second, 10*time.Millisecond)
	unlock()
	require.Eventually(t, func() bool { return strings.Count(stderr.String(), "Warning:") == 2 }, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	since := time.Date(2026, 10, 4, 10, 2, 3, 0, time.UTC).Local().Format(time.DateTime)
	w.Finish()
	assert.Equal(t, "wait realm lock message=sind create cluster (pid 7 on elsewhere, since "+since+"): canceled (canceled): taking the realm lock on the Docker daemon: context canceled\n",
		withoutCalls(w.Events()))
}

// withoutCalls draws the spans of events as progresstest.Capture.Tree
// does, leaving out the calls and what is under them: the docker commands
// of a wait that polls, which run as often as the wait takes.
func withoutCalls(events []progress.Event) string {
	var c progresstest.Capture
	calls := map[progress.SpanID]bool{}
	for _, e := range events {
		if e.Kind == progress.KindCall || calls[e.Parent] {
			calls[e.Span] = true
			continue
		}
		c.Handle(e)
	}
	return c.Tree()
}

// The event log is a file of JSON lines readable by its owner alone,
// written with no display as with one: the trace it continues, the
// program and its version, then the command's span. sind takes the trace
// context out of its environment, so docker is handed none.
func TestTheEventLogOfACommand(t *testing.T) {
	onTerminals(t, nil)
	t.Setenv("TRACEPARENT", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	t.Setenv("TRACESTATE", "congo=t61rcWkgMzE")
	file := filepath.Join(t.TempDir(), "events.jsonl")
	var traceparent string
	ctx := powerCutFails(t.Context(), t, func() { traceparent = os.Getenv("TRACEPARENT") })
	var stderr bytes.Buffer

	code := run(ctx, []string{"--progress", "none", "--progress-log", file, "power", "cut", "worker-0"}, &stderr)

	assert.Equal(t, exitFailure, code)
	assert.Empty(t, traceparent, "docker inherits no trace context")
	info, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	lines := readJSONLines(t, file)
	require.GreaterOrEqual(t, len(lines), 3)
	for _, line := range lines {
		assert.EqualValues(t, 1, line["v"])
	}
	first := lines[0]
	assert.Equal(t, "trace", first["type"])
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", first["trace"])
	assert.Equal(t, "b7ad6b7169203331", first["parent"])
	assert.Equal(t, "congo=t61rcWkgMzE", first["traceState"])
	assert.Equal(t, "sind", first["program"])
	assert.Equal(t, strings.TrimPrefix(resolveVersion(), "v"), first["version"])
	start, end := lines[1], lines[len(lines)-1]
	assert.Equal(t, "start", start["type"])
	assert.Equal(t, "command", start["kind"])
	assert.Equal(t, "power cut", start["name"])
	assert.Equal(t, "end", end["type"])
	assert.Equal(t, "failed", end["status"])
	assert.Contains(t, end["err"], "listing containers")

	// A second run appends.
	run(powerCutFails(t.Context(), t, nil), []string{"--progress-log", file, "power", "cut", "worker-0"}, io.Discard)
	assert.Greater(t, len(readJSONLines(t, file)), len(lines))
}

func readJSONLines(t *testing.T, file string) []map[string]any {
	t.Helper()
	f, err := os.Open(file)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var lines []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &line), scanner.Text())
		lines = append(lines, line)
	}
	require.NoError(t, scanner.Err())
	return lines
}

// An event log that cannot be used refuses the command when the flag names
// it, before anything runs, and is said on stderr when the variable does,
// and the command runs without; one that others can read is not written.
// A log that cannot be written fails nothing, and is said once.
func TestAnEventLogThatCannotBeUsed(t *testing.T) {
	onTerminals(t, nil)
	shared := filepath.Join(t.TempDir(), "shared.jsonl")
	require.NoError(t, os.WriteFile(shared, nil, 0o644))
	missing := filepath.Join(t.TempDir(), "no", "such", "dir", "events.jsonl")

	t.Run("from the flag", func(t *testing.T) {
		for _, path := range []string{missing, shared} {
			calls := 0
			var stderr bytes.Buffer
			err := executeOn(powerCutFails(t.Context(), t, func() { calls++ }), io.Discard, &stderr,
				"--progress-log", path, "power", "cut", "worker-0")
			require.Error(t, err)
			assert.True(t, isUsageError(err), "a usage error: %v", err)
			assert.Contains(t, err.Error(), "--progress-log names a progress log that cannot be used: ")
			assert.Zero(t, calls, "nothing runs")
			assert.Empty(t, stderr.String())
		}
	})
	t.Run("from the variable", func(t *testing.T) {
		t.Setenv(envProgressLog, shared)
		var stderr bytes.Buffer
		err := executeOn(powerCutFails(t.Context(), t, nil), io.Discard, &stderr, "power", "cut", "worker-0")
		require.ErrorContains(t, err, "listing containers")
		assert.Equal(t, "sind: SIND_PROGRESS_LOG names a progress log that cannot be used: "+shared+
			" can be read or written by others than its owner (mode -rw-r--r--); run chmod 600 "+shared+"; no log is written\n", stderr.String())
		data, err := os.ReadFile(shared)
		require.NoError(t, err)
		assert.Empty(t, data)
	})
	t.Run("a write that fails", func(t *testing.T) {
		if _, err := os.Stat("/dev/full"); err != nil {
			t.Skip("no /dev/full")
		}
		var stderr bytes.Buffer
		err := executeOn(powerCutFails(t.Context(), t, nil), io.Discard, &stderr,
			"--progress-log", "/dev/full", "power", "cut", "worker-0")
		require.ErrorContains(t, err, "listing containers")
		assert.Equal(t, 1, strings.Count(stderr.String(), "sind: the progress log /dev/full stops short: "), stderr.String())
	})
}

// The MCP tools take neither progress flag, and a server's tool calls show
// no progress and write no event log whatever SIND_PROGRESS and
// SIND_PROGRESS_LOG the server inherited.
func TestMCPTools_NoProgress(t *testing.T) {
	t.Setenv(envProgress, "plain")
	t.Setenv(envProgressLog, "/dev/stderr")
	for name, tool := range exportMCPTools(t) {
		flags := tool.InputSchema.Properties.Flags.Properties
		assert.NotContains(t, flags, "progress", name)
		assert.NotContains(t, flags, "progress-log", name)
	}

	var got string
	logSet := true
	server := &cobra.Command{Use: "start", RunE: func(*cobra.Command, []string) error {
		got = os.Getenv(envProgress)
		_, logSet = os.LookupEnv(envProgressLog)
		return nil
	}}
	withoutProgress(server)
	server.SetArgs(nil)
	require.NoError(t, server.Execute())
	assert.Equal(t, cliprogress.ModeNone.String(), got)
	assert.False(t, logSet, "the tool calls inherit no event log")
}

// Under a display, what the command writes goes through the terminal: the
// warnings of cobra's stderr and of the context's, the logger's records,
// and stdout when it is the terminal too, above the tree; a question is
// asked with the display off the terminal. Once the command is over, the
// streams are back.
func TestTheDisplayCarriesWhatTheCommandWrites(t *testing.T) {
	t.Setenv("TERM", "xterm")
	c := fakeDisplays(t)
	screen := &progresstest.Screen{Width: screenWidth}
	onTerminals(t, nil, screen)

	root := NewRootCommand()
	var during, duringOut io.Writer
	var suspended bool
	work := &cobra.Command{Use: "work", RunE: withProgress(func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		during, duringOut = cmd.ErrOrStderr(), cmd.OutOrStdout()
		cmd.PrintErrln("Warning: from cobra")
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "output")
		c.draw()
		_, _ = fmt.Fprintln(stderrFrom(ctx), "Warning: from the context")
		meshMgrFrom(ctx, clientFrom(ctx), "sind").OnWarning("from the mesh")
		c.draw()
		sindlog.From(ctx).InfoContext(ctx, "from the logger")
		resume := progress.Suspend(ctx)
		suspended = !strings.Contains(screen.String(), "work ·")
		resume()
		return errors.New("work failed")
	})}
	root.AddCommand(work)
	root.SetOut(screen)
	root.SetErr(screen)
	root.SetArgs([]string{"-v", "work"})
	root.SetContext(context.Background())

	err := root.Execute()

	require.EqualError(t, err, "work failed")
	assert.NotEqual(t, screen, during, "cobra's stderr is the terminal's writer")
	assert.Equal(t, io.Writer(screen), root.ErrOrStderr(), "cobra's stderr is back")
	assert.NotEqual(t, screen, duringOut, "cobra's stdout is the terminal's writer")
	assert.Equal(t, io.Writer(screen), root.OutOrStdout(), "cobra's stdout is back")
	assert.True(t, suspended, "the tree is off the terminal while suspended")
	assert.Equal(t, `Warning: from cobra
output
Warning: from the context
Warning: from the mesh
HH:MM:SS.mmm INFO from the logger
sind: work: failed in 2.0s
`, withoutTimestamps(screen.String()))
}

// A panic in the command ends its span as one, takes the display off the
// terminal and leaves the summary, before it goes on up.
func TestTheDisplayIsTakenOffWhenTheCommandPanics(t *testing.T) {
	t.Setenv("TERM", "xterm")
	c := fakeDisplays(t)
	screen := &progresstest.Screen{Width: screenWidth}
	onTerminals(t, nil, screen)
	root := NewRootCommand()
	root.AddCommand(&cobra.Command{Use: "work", RunE: withProgress(func(*cobra.Command, []string) error {
		c.draw()
		panic("boom")
	})})
	root.SetErr(screen)
	root.SetArgs([]string{"work"})
	root.SetContext(context.Background())

	assert.PanicsWithValue(t, "boom", func() { _ = root.Execute() })
	assert.Equal(t, "sind: work: failed in 1.0s\n", screen.String())
}

// The classes the spans of a command end with: canceled once the command
// was interrupted, whatever failed; the command line's, a timeout for
// nodes not ready within --wait, and the target's for the rest.
func TestProgressClass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	classify := progressClass(ctx)
	assert.Equal(t, progress.ClassUsage, classify(usagef("bad flag")))
	assert.Equal(t, progress.ClassTimeout, classify(fmt.Errorf("cluster x %w within 5m0s: probe", cluster.ErrNotReady)))
	assert.Equal(t, progress.ClassNone, classify(errors.New("signal: killed")))
	cancel()
	assert.Equal(t, progress.ClassCanceled, classify(errors.New("signal: killed")))
	assert.Equal(t, progress.ClassCanceled, progress.Classify(interrupted{context.DeadlineExceeded}, nil))
}

// The default seams look at the streams the way a command line does: a
// stream that is no file is no terminal, no pipe, of no size and in the
// foreground, and a pipe is a pipe. The probes themselves are the kit's.
func TestTheDefaultDisplaySeams(t *testing.T) {
	var buf bytes.Buffer
	assert.False(t, onTerminal(&buf))
	assert.False(t, intoPipe(&buf))
	_, _, err := terminalSize(&buf)
	assert.Error(t, err)
	assert.True(t, inForeground(&buf))

	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	assert.False(t, onTerminal(w))
	assert.True(t, intoPipe(w))
}

// The displays draw Classic on a terminal, in as many colours as it shows,
// and in its marks alone under NO_COLOR; plain text off a terminal and on
// a dumb one, where plain lines go to a log.
func TestTheDisplaysDrawClassic(t *testing.T) {
	savedTerm, savedProfile := onTerminal, colourProfile
	t.Cleanup(func() { onTerminal, colourProfile = savedTerm, savedProfile })
	t.Setenv("TERM", "xterm-256color")
	onTerminal = func(io.Writer) bool { return true }
	for profile, want := range map[termenv.Profile]display.Theme{
		termenv.TrueColor: display.Classic.In(display.Colours256),
		termenv.ANSI256:   display.Classic.In(display.Colours256),
		termenv.ANSI:      display.Classic.In(display.Colours16),
		termenv.Ascii:     display.Classic.In(display.NoColours),
	} {
		colourProfile = func(io.Writer) termenv.Profile { return profile }
		assert.Equal(t, want, progressTheme(io.Discard), profile)
	}
	t.Setenv("TERM", "dumb")
	assert.Equal(t, display.Theme{}, progressTheme(io.Discard), "dumb")
	t.Setenv("TERM", "xterm-256color")
	onTerminal = func(io.Writer) bool { return false }
	assert.Equal(t, display.Theme{}, progressTheme(io.Discard), "no terminal")

	// NO_COLOR reaches the profile the logger takes too.
	t.Setenv("NO_COLOR", "1")
	assert.Equal(t, termenv.Ascii, savedProfile(io.Discard))
}
