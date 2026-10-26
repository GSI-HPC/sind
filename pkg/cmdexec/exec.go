// SPDX-License-Identifier: LGPL-3.0-or-later

// Package cmdexec provides a testable abstraction for running external commands.
package cmdexec

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/creack/pty"
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
	var term *terminal
	if Terminal(ctx) {
		term = openTerminal(cmd.Stdout)
	}
	if term != nil {
		cmd.Stdout = term.tty
		if opened := TerminalOpened(ctx); opened != nil {
			opened()
		}
	}
	cmd.Stderr = progress.Tee(ctx, &errBuf, progress.StreamStderr, parse)
	err := cmd.Start()
	if term != nil {
		term.started(err == nil)
	}
	if err == nil {
		if started := Started(ctx); started != nil {
			started(cmd.Process.Pid)
		}
		err = cmd.Wait()
	}
	if term != nil {
		term.wait()
	}
	return outBuf.String(), errBuf.String(), WrapExitError(err, errBuf.String())
}

// terminal is the pseudo-terminal of a command run WithTerminal: the
// command writes to tty, and what it writes is read from pty and written to
// out as plain lines.
type terminal struct {
	pty, tty *os.File
	out      *TerminalLines
	done     chan struct{}
}

// openTerminal opens a pseudo-terminal of TerminalWidth columns whose
// output goes to w, or returns nil when it cannot.
func openTerminal(w io.Writer) *terminal {
	p, tty, err := pty.Open()
	if err != nil {
		return nil
	}
	if err := pty.Setsize(p, &pty.Winsize{Rows: 24, Cols: TerminalWidth}); err != nil {
		_ = p.Close()
		_ = tty.Close()
		return nil
	}
	return &terminal{pty: p, tty: tty, out: NewTerminalLines(w), done: make(chan struct{})}
}

// started lets go of the parent's end of the terminal once the command
// has it, and reads what the command writes until no process holds the
// terminal any longer; ok is false for a command that did not start.
func (t *terminal) started(ok bool) {
	_ = t.tty.Close()
	if !ok {
		_ = t.pty.Close()
		close(t.done)
		return
	}
	go func() {
		defer close(t.done)
		// The read fails once the terminal has no process left (EIO).
		_, _ = io.Copy(t.out, t.pty)
		_ = t.out.Flush()
	}()
}

// wait waits for what the command wrote to have been read, at most
// processGroupWaitDelay after the command ended, as a process it left
// behind may hold the terminal, and closes the terminal.
func (t *terminal) wait() {
	select {
	case <-t.done:
	case <-time.After(processGroupWaitDelay):
	}
	_ = t.pty.Close()
	<-t.done
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
