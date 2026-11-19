// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/GSI-HPC/sind/internal/execopt"
)

// streamStderrHalf is half of what a Process keeps of its command's
// standard error: the first and the last 2 KiB.
const streamStderrHalf = 2 << 10

// Process represents a running long-lived command whose stdout can be
// read incrementally. The caller must call Close to release resources.
type Process struct {
	Stdout io.ReadCloser
	cmd    *exec.Cmd
	stdin  io.Closer
	// stderr keeps the head and the tail of the command's standard error,
	// outside execopt.WithStderrMerged.
	stderr *headTail
	// group is set for a command in a process group of its own
	// (execopt.WithProcessGroup), which Close kills whole; groupDone ends what
	// inProcessGroup started once the command has ended.
	group     bool
	groupDone func()

	closeOnce sync.Once
	closeErr  error
}

// Start starts a command and returns a Process for reading its stdout, and its
// stderr too under execopt.WithStderrMerged. The command is killed when ctx is
// cancelled. Outside execopt.WithStderrMerged, the Process keeps the first and
// the last 2 KiB of the command's stderr for the *ExitError of Close, which
// says why a stream that failed ended.
//
// The command's stdin is a pipe that stays open, without data, until the
// command has ended or Close kills it, so that a docker exec -i whose command
// ends with its stdin (docker.TiedExecArgs) runs as long as the docker CLI.
//
// As OSExecutor.Run does, Start runs the command with the environment of
// execopt.WithEnv, and in a process group of its own under
// execopt.WithProcessGroup, which the end of ctx and Close kill whole.
func Start(ctx context.Context, name string, args ...string) (*Process, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env := execopt.Env(ctx); len(env) > 0 {
		cmd.Env = env
	}
	p := &Process{cmd: cmd, groupDone: func() {}}
	if execopt.InProcessGroup(ctx) {
		p.group = true
		p.groupDone = inProcessGroup(cmd, execopt.StopGrace(ctx))
	}
	// Close closes the pipe, and so does Wait.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	p.stdin = stdin
	if execopt.StderrMerged(ctx) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		cmd.Stdout, cmd.Stderr = w, w
		err = cmd.Start()
		_ = w.Close() // the child holds its own copy
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		p.Stdout = r
		return p, nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Wait waits for the copy of stderr, which a process the command left
	// behind may hold open: at most outputWaitDelay, or the grace of
	// execopt.WithStopGrace on top of it.
	p.stderr = &headTail{half: streamStderrHalf}
	cmd.Stderr = p.stderr
	cmd.WaitDelay = max(cmd.WaitDelay, outputWaitDelay)
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, err
	}
	p.Stdout = stdout
	return p, nil
}

// Close kills the process (if still running), waits for it to exit and
// closes Stdout, in either mode of Start. If the Process was created
// without a command, as a test double or an Executor that runs no local
// process creates it, Close just closes Stdout and returns its error, so
// that Stdout's Close can end the command and report how it exited. The
// "signal: killed" error from the intentional Kill is suppressed so normal
// shutdown returns nil; a command that exited with a non-zero status by
// itself, as a stream that failed, returns an *ExitError with what Start
// kept of its stderr. Later calls return the result of the first.
func (p *Process) Close() error {
	p.closeOnce.Do(func() { p.closeErr = p.close() })
	return p.closeErr
}

func (p *Process) close() error {
	if p.cmd == nil {
		return p.Stdout.Close()
	}
	killed := false
	if p.cmd.Process != nil {
		kill := p.cmd.Process.Kill
		if p.group {
			kill = func() error { return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL) }
		}
		if err := kill(); err == nil {
			killed = true
		}
	}
	// A process the command left behind that reads stdin, such as the cat
	// of sh -c 'cat', ends with it and lets go of stderr.
	_ = p.stdin.Close()
	err := p.cmd.Wait()
	p.groupDone()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil // it exited 0; a process it left behind held stderr
	}
	// Wait closes the pipe of StdoutPipe, but not the one Start made under
	// execopt.WithStderrMerged, which would stay open until a finalizer ran.
	_ = p.Stdout.Close()
	if killed && isKillSignalError(err) {
		return nil
	}
	var stderr string
	if p.stderr != nil {
		stderr = p.stderr.String()
	}
	return WrapExitError(err, stderr)
}

// isKillSignalError reports whether err is the expected ExitError from
// a process terminated by SIGKILL.
func isKillSignalError(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return false
	}
	return status.Signaled() && status.Signal() == syscall.SIGKILL
}
