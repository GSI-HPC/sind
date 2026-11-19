// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"os/exec"
	"strconv"
	"strings"
)

// ExitError is the error of a command that ran and exited with a non-zero
// status. It keeps what the command wrote to stderr, which for docker is the
// only place that says what went wrong: "exit status 1" alone cannot tell a
// missing volume from one that is still in use.
//
// It carries its own exit code, so that an Executor that does not run a
// local process, or a test double, can return one with NewExitError.
type ExitError struct {
	// Code is the command's exit code, -1 for a command that a signal
	// ended.
	Code int
	// Stderr is what the command wrote to its standard error.
	Stderr string
	// Err is the error the command ended with, such as the *exec.ExitError
	// of OSExecutor, or nil.
	Err error
}

// NewExitError returns the error of a command that exited with code and
// wrote stderr.
func NewExitError(code int, stderr string) *ExitError {
	return &ExitError{Code: code, Stderr: stderr}
}

// Error returns the exit status followed by the command's stderr, if any.
func (e *ExitError) Error() string {
	status := "exit status " + strconv.Itoa(e.Code)
	if e.Err != nil {
		status = e.Err.Error()
	}
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return status
	}
	return status + ": " + msg
}

// Unwrap returns the error the command ended with, if any.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode returns the command's exit code.
func (e *ExitError) ExitCode() int { return e.Code }

// WrapExitError returns a bare *exec.ExitError as an *ExitError carrying
// its exit code and stderr, and any other error unchanged. Executors call
// it on the result of a finished command.
func WrapExitError(err error, stderr string) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return &ExitError{Code: exitErr.ExitCode(), Stderr: stderr, Err: exitErr}
	}
	return err
}
