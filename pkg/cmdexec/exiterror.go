// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"os/exec"
	"strings"
)

// ExitError is the error of a command that ran and exited with a non-zero
// status. It keeps what the command wrote to stderr, which for docker is the
// only place that says what went wrong: "exit status 1" alone cannot tell a
// missing volume from one that is still in use.
type ExitError struct {
	Err    *exec.ExitError
	Stderr string
}

// Error returns the exit status followed by the command's stderr, if any.
func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return e.Err.Error()
	}
	return e.Err.Error() + ": " + msg
}

// Unwrap returns the underlying *exec.ExitError.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode returns the command's exit code.
func (e *ExitError) ExitCode() int { return e.Err.ExitCode() }

// WrapExitError returns a bare *exec.ExitError as an *ExitError carrying
// stderr, and any other error unchanged. Executors call it on the result of
// a finished command; test doubles call it so that their errors have the
// same shape as the real ones.
func WrapExitError(err error, stderr string) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return &ExitError{Err: exitErr, Stderr: stderr}
	}
	return err
}
