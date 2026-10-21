// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// Process represents a running long-lived command whose stdout can be
// read incrementally. The caller must call Close to release resources.
type Process struct {
	Stdout io.ReadCloser
	cmd    *exec.Cmd
}

// Start starts a command and returns a Process for reading its stdout, and
// its stderr too under WithStderrMerged. The command is killed when ctx is
// cancelled.
func Start(ctx context.Context, name string, args ...string) (*Process, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if StderrMerged(ctx) {
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
		return &Process{Stdout: r, cmd: cmd}, nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return &Process{Stdout: stdout, cmd: cmd}, nil
}

// Close kills the process (if still running) and waits for it to exit.
// If the Process was created without a command (e.g. in tests), Close
// just closes Stdout. The "signal: killed" error from the intentional
// Kill is suppressed so normal shutdown returns nil.
func (p *Process) Close() error {
	if p.cmd == nil {
		return p.Stdout.Close()
	}
	killed := false
	if p.cmd.Process != nil {
		if err := p.cmd.Process.Kill(); err == nil {
			killed = true
		}
	}
	err := p.cmd.Wait()
	if killed && isKillSignalError(err) {
		return nil
	}
	return err
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
