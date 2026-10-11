// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// childEnv makes the test binary act as the process under test: "main" runs
// sind with the arguments it was given, and "wait" only waits for
// interrupts, which is how a rollback that ignores the context behaves.
// Started under the name "docker", the test binary is a fake docker instead:
// it records that it started in the file fakeDockerEnv names and then
// hangs, like a docker call still running when the user presses Ctrl-C, or
// fails at once when fakeDockerEnv is empty. fakeDockerExitEnv makes it end
// at once instead, with the exit status it names or killed by the signal
// it names (fakeDockerSignals), like a docker exec whose command failed.
// fakeDockerArgsEnv names a file the fake docker writes its arguments to,
// one per line, before anything else.
//
// Adapted from GSI-HPC/clusterctl cmd/clusterctl/signal_test.go.
const (
	childEnv          = "SIND_TEST_PROCESS"
	fakeDockerEnv     = "SIND_TEST_DOCKER_STARTED"
	fakeDockerExitEnv = "SIND_TEST_DOCKER_EXIT"
	fakeDockerArgsEnv = "SIND_TEST_DOCKER_ARGS"
)

// fakeDockerSignals are the signals fakeDockerExitEnv can name.
var fakeDockerSignals = map[string]syscall.Signal{
	"SIGINT":  syscall.SIGINT,
	"SIGKILL": syscall.SIGKILL,
	"SIGTERM": syscall.SIGTERM,
}

// TestMain runs the tests without the caller's sind settings: an exported
// SIND_REALM, as sind-action sets it in CI, would move every command into
// another realm than the one the tests expect, XDG_STATE_HOME would
// point the state directory at the developer's own, and SIND_PROGRESS=plain
// would add progress lines to stderr. A test that needs one
// of them sets it with t.Setenv.
//
// Adapted from GSI-HPC/clusterctl internal/cli/main_test.go.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "docker" {
		fakeDocker()
	}
	switch os.Getenv(childEnv) {
	case "main":
		main()
	case "wait":
		ctx, _ := interruptContext()
		fmt.Println("ready")
		<-ctx.Done()
		fmt.Println("cancelled:", ctx.Err())
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	for _, name := range []string{"SIND_REALM", "XDG_STATE_HOME", envProgress, envProgressLog} {
		_ = os.Unsetenv(name)
	}
	os.Exit(m.Run())
}

func fakeDocker() {
	if file := os.Getenv(fakeDockerArgsEnv); file != "" {
		_ = os.WriteFile(file, []byte(strings.Join(os.Args[1:], "\n")), 0o600)
	}
	if exit := os.Getenv(fakeDockerExitEnv); exit != "" {
		if sig, ok := fakeDockerSignals[exit]; ok {
			_ = syscall.Kill(os.Getpid(), sig)
			time.Sleep(time.Minute)
		}
		code, _ := strconv.Atoi(exit)
		fmt.Fprintf(os.Stderr, "fake docker exits %d\n", code)
		os.Exit(code)
	}
	started := os.Getenv(fakeDockerEnv)
	if started == "" {
		fmt.Fprintln(os.Stderr, "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
		os.Exit(1)
	}
	_ = os.WriteFile(started, []byte("started\n"), 0o600)
	time.Sleep(time.Minute)
	os.Exit(0)
}

// child starts the test binary as the process under test, with the fake
// docker first on PATH.
func child(t *testing.T, mode string, args ...string) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	require.NoError(t, err)
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))
	require.NoError(t, os.Symlink(self, filepath.Join(bin, "docker")))
	started := filepath.Join(dir, "docker-started")

	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(),
		childEnv+"="+mode,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+dir,
		"XDG_STATE_HOME="+filepath.Join(dir, "state"),
		fakeDockerEnv+"="+started,
	)
	return cmd, started
}

// waitFor waits for a process to end, killing it and failing the test if it
// is still running after d.
func waitFor(t *testing.T, cmd *exec.Cmd, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("the process was still running %v after it was interrupted", d)
		return nil
	}
}

// waitForFile waits until a file exists.
func waitForFile(t *testing.T, path string, cmd *exec.Cmd, stderr *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	t.Fatalf("docker was never started; stderr:\n%s", stderr)
}

// TestSecondInterruptEndsTheProcess checks that the first interrupt cancels
// the context and the second ends the process. The handler used to stay
// installed, so every later signal was swallowed and a hung rollback could
// not be interrupted at all.
func TestSecondInterruptEndsTheProcess(t *testing.T) {
	t.Parallel()
	cmd, _ := child(t, "wait")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	lines := bufio.NewScanner(stdout)
	expect := func(want string) {
		t.Helper()
		if !lines.Scan() || lines.Text() != want {
			_ = cmd.Process.Kill()
			t.Fatalf("the process said %q, want %q", lines.Text(), want)
		}
	}

	expect("ready")
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	expect("cancelled: context canceled")
	require.NoError(t, cmd.Process.Signal(os.Interrupt))

	err = waitFor(t, cmd, 5*time.Second)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "the process has to be killed by the signal")
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	assert.True(t, status.Signaled(), "the process ended with %v, want it killed by SIGINT", exitErr)
	assert.Equal(t, syscall.SIGINT, status.Signal())
}

// TestInterruptExits130 checks that SIGINT and SIGTERM stop a command whose
// docker call is still running, and that it exits 130. SIGTERM was not
// caught, so `timeout` or `docker stop` killed sind without cleanup, and an
// interrupt exited 1 with "signal: killed".
func TestInterruptExits130(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			cmd, started := child(t, "main", "get", "clusters")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			require.NoError(t, cmd.Start())
			waitForFile(t, started, cmd, &stderr)
			require.NoError(t, cmd.Process.Signal(sig))

			err := waitFor(t, cmd, 15*time.Second)
			assert.Equal(t, exitInterrupted, cmd.ProcessState.ExitCode(), "%v; stderr:\n%s", err, &stderr)
			assert.Contains(t, stderr.String(), "interrupted: ")
		})
	}
}

// TestFailureExits1 checks that a command that fails on its own, without an
// interrupt, still exits 1.
func TestFailureExits1(t *testing.T) {
	t.Parallel()
	cmd, _ := child(t, "main", "get", "clusters")
	cmd.Env = append(cmd.Env, fakeDockerEnv+"=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := waitFor(t, func() *exec.Cmd { require.NoError(t, cmd.Start()); return cmd }(), 15*time.Second)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "%v", err)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.NotContains(t, stderr.String(), "interrupted")
	assert.True(t, strings.Contains(stderr.String(), "Cannot connect to the Docker daemon"), stderr.String())
}

// failingDocker returns a context whose docker client fails its first call
// with exit code 1 and stderr.
func failingDocker(ctx context.Context, t *testing.T, stderr string) context.Context {
	t.Helper()
	var m mock.Executor
	m.AddResult("", stderr, testutil.ExitCode1(t))
	return withClient(ctx, docker.NewClient(&m))
}

func TestRun_Success(t *testing.T) {
	var stderr bytes.Buffer
	assert.Equal(t, 0, run(t.Context(), []string{"version", "--json"}, &stderr))
	assert.Empty(t, stderr.String())
}

// TestRun_EscapesTheError checks that control characters docker or a
// container wrote into an error do not reach the terminal: the logger
// quotes attribute values but not the message.
func TestRun_EscapesTheError(t *testing.T) {
	ctx := failingDocker(t.Context(), t, "Error response from daemon: \x1b]52;c;ZXZpbA==\x07\r\x1b[1Aforged\n")
	var stderr bytes.Buffer

	code := run(ctx, []string{"get", "clusters"}, &stderr)

	assert.Equal(t, 1, code)
	assert.NotContains(t, stderr.String(), "\x1b")
	assert.NotContains(t, stderr.String(), "\x07")
	assert.NotContains(t, stderr.String(), "\r")
	assert.Contains(t, stderr.String(), `Error response from daemon: \x1b]52;c;ZXZpbA==\x07\r\x1b[1Aforged`)
}

func TestRun_Interrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ctx = failingDocker(ctx, t, "")
	var stderr bytes.Buffer

	code := run(ctx, []string{"get", "clusters"}, &stderr)

	assert.Equal(t, exitInterrupted, code)
	assert.Contains(t, stderr.String(), "interrupted: ")
}
