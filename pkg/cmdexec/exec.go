// SPDX-License-Identifier: LGPL-3.0-or-later

// Package cmdexec provides a testable abstraction for running external commands.
package cmdexec

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
)

// Executor runs external commands and captures their output.
type Executor interface {
	// Run executes the named command with the given arguments and returns
	// the captured stdout and stderr. If the command exits with a non-zero
	// status, the returned error is an *ExitError that carries stderr and
	// wraps the *exec.ExitError.
	Run(ctx context.Context, name string, args ...string) (stdout string, stderr string, err error)

	// RunWithStdin is like Run but pipes the given reader to the command's stdin.
	RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (stdout string, stderr string, err error)

	// Start starts a long-lived command and returns a Process for reading
	// its stdout incrementally. The command is killed when ctx is cancelled.
	// The caller must call Process.Close to release resources.
	Start(ctx context.Context, name string, args ...string) (*Process, error)
}

// OSExecutor runs commands using os/exec.
type OSExecutor struct{}

// processGroupWaitDelay bounds how long a command run WithProcessGroup
// waits, once its context ended or it exited, for the processes that hold
// its output open, before its output is closed.
const processGroupWaitDelay = 2 * time.Second

// inProcessGroup makes cmd run in a process group of its own, which the
// end of its context kills whole (WithProcessGroup).
func inProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = processGroupWaitDelay
}

func (e *OSExecutor) run(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	if env := Env(ctx); len(env) > 0 {
		cmd.Env = env
	}
	if InProcessGroup(ctx) {
		inProcessGroup(cmd)
	}
	var outBuf, errBuf bytes.Buffer
	// Under a progress span that shows lines, such as the target of an
	// image docker pulls, its display shows the newest line the command
	// wrote, of standard error alone when standard output is data
	// (WithStdoutAsData); otherwise Tee returns the buffer itself. A line
	// parser (WithLineParser) is handed the lines of the same streams.
	parse := LineParser(ctx)
	cmd.Stdout = &outBuf
	if !StdoutIsData(ctx) {
		cmd.Stdout = progress.Tee(ctx, &outBuf, progress.StreamStdout, parse)
	}
	cmd.Stderr = progress.Tee(ctx, &errBuf, progress.StreamStderr, parse)
	err := cmd.Start()
	if err == nil {
		if started := Started(ctx); started != nil {
			started(cmd.Process.Pid)
		}
		err = cmd.Wait()
	}
	return outBuf.String(), errBuf.String(), WrapExitError(err, errBuf.String())
}

// Run implements Executor.
func (e *OSExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	return e.run(ctx, nil, name, args...)
}

// RunWithStdin implements Executor.
func (e *OSExecutor) RunWithStdin(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	return e.run(ctx, stdin, name, args...)
}

// Start implements Executor.
func (e *OSExecutor) Start(ctx context.Context, name string, args ...string) (*Process, error) {
	return Start(ctx, name, args...)
}
