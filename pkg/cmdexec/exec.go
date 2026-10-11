// SPDX-License-Identifier: LGPL-3.0-or-later

// Package cmdexec provides a testable abstraction for running external commands.
package cmdexec

import (
	"bytes"
	"context"
	"io"
	"os/exec"

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

func (e *OSExecutor) run(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	var outBuf, errBuf bytes.Buffer
	// Under a progress span that shows lines, such as the target of an
	// image docker pulls, its display shows the newest line the command
	// wrote, of standard error alone when standard output is data
	// (WithStdoutAsData); otherwise Tee returns the buffer itself.
	cmd.Stdout = &outBuf
	if !StdoutIsData(ctx) {
		cmd.Stdout = progress.Tee(ctx, &outBuf, progress.Stdout, nil)
	}
	cmd.Stderr = progress.Tee(ctx, &errBuf, progress.Stderr, nil)
	err := cmd.Run()
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
