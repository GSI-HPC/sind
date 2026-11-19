// SPDX-License-Identifier: LGPL-3.0-or-later

// Package cmdexec provides a testable abstraction for running external commands.
package cmdexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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

// outputWaitDelay bounds how long a command waits, once its context ended
// or it exited, for the processes that hold its output open, such as one
// it started and left behind, before its output is closed. Without it, a
// context deadline would not bound a command whose child outlives it.
const outputWaitDelay = 2 * time.Second

// inProcessGroup makes cmd run in a process group of its own, which the
// end of its context kills whole (WithProcessGroup), or with a grace
// (WithStopGrace), stops with SIGTERM and kills whole once grace has
// passed. The caller calls done once cmd.Wait has returned.
func inProcessGroup(cmd *exec.Cmd, grace time.Duration) (done func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	killGroup := func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Cancel = killGroup
	if grace <= 0 {
		return func() {}
	}
	// Wait returns only after Cancel, if exec called it, has returned, so
	// done reads kill after it was set.
	var kill *time.Timer
	cmd.Cancel = func() error {
		kill = time.AfterFunc(grace, func() { _ = killGroup() })
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = grace + outputWaitDelay
	return func() {
		if kill != nil {
			kill.Stop()
		}
	}
}

func (e *OSExecutor) run(ctx context.Context, stdin io.Reader, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.WaitDelay = outputWaitDelay
	if env := Env(ctx); len(env) > 0 {
		cmd.Env = env
	}
	if InProcessGroup(ctx) {
		done := inProcessGroup(cmd, StopGrace(ctx))
		defer done()
	}
	if OrphanSafe(ctx) {
		return runOrphanSafe(ctx, cmd)
	}
	outBuf, errBuf := newOutput(ctx), newOutput(ctx)
	// Under a progress span that shows lines, such as the target of an
	// image docker pulls, its display shows the newest line the command
	// wrote, of standard error alone when standard output is data
	// (WithStdoutAsData); otherwise Tee returns the buffer itself. A line
	// parser (WithLineParser) is handed the lines of the same streams.
	parse := LineParser(ctx)
	cmd.Stdout = outBuf
	if !StdoutIsData(ctx) {
		cmd.Stdout = progress.Tee(ctx, outBuf, progress.StreamStdout, parse)
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
	cmd.Stderr = progress.Tee(ctx, errBuf, progress.StreamStderr, parse)
	err := cmd.Start()
	if term != nil {
		term.started(err == nil)
	}
	if err == nil {
		if started := Started(ctx); started != nil {
			started(cmd.Process.Pid)
		}
		err = cmd.Wait()
		// The command succeeded, and a process it left behind held its
		// output open for longer than outputWaitDelay.
		if errors.Is(err, exec.ErrWaitDelay) {
			err = nil
		}
	}
	if term != nil {
		term.wait()
	}
	return outBuf.String(), errBuf.String(), WrapExitError(err, errBuf.String())
}

// runOrphanSafe runs cmd as WithOrphanSafe describes: its standard output
// goes to the null device and its standard error to a temporary file that
// is unlinked at once, so that nothing is left of it once both processes
// have ended. The goroutine keeps its thread until cmd ends, as the signal
// of the parent's death (orphanSignal) comes when the thread that started
// cmd ends.
func runOrphanSafe(ctx context.Context, cmd *exec.Cmd) (string, string, error) {
	f, err := os.CreateTemp("", "sind-stderr-")
	if err != nil {
		return "", "", fmt.Errorf("creating a file for the standard error of %s: %w", cmd.Path, err)
	}
	defer func() { _ = f.Close() }()
	_ = os.Remove(f.Name())
	cmd.Stdout, cmd.Stderr = nil, f
	orphanSignal(cmd)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = cmd.Start()
	if err == nil {
		if started := Started(ctx); started != nil {
			started(cmd.Process.Pid)
		}
		err = cmd.Wait()
	}
	var errBuf bytes.Buffer
	if _, serr := f.Seek(0, io.SeekStart); serr == nil {
		_, _ = errBuf.ReadFrom(f)
	}
	return "", errBuf.String(), WrapExitError(err, errBuf.String())
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
	if p, err = pollable(p); err != nil {
		_ = tty.Close()
		return nil
	}
	return &terminal{pty: p, tty: tty, out: NewTerminalLines(w), done: make(chan struct{})}
}

// pollable returns a non-blocking copy of f, which the runtime's poller
// reads, and closes f. pty.Open and pty.Setsize call f.Fd(), which puts f
// in blocking mode, where closing f does not end a read of it.
func pollable(f *os.File) (*os.File, error) {
	defer func() { _ = f.Close() }()
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
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
// outputWaitDelay after the command ended, as a process it left behind may
// hold the terminal, and closes the terminal, which ends the read.
func (t *terminal) wait() {
	select {
	case <-t.done:
	case <-time.After(outputWaitDelay):
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
