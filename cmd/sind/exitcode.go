// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/spf13/cobra"
)

// The exit statuses of sind. Scripts branch on them, so they may be added
// to but never renumbered. ssh, exec, enter and logs exit with the status
// of the program they run instead (childExitError).
const (
	exitOK = 0
	// exitFailure is the exit status of a command that ran and failed.
	exitFailure = 1
	// exitUsage is the exit status of a command line that sind rejected
	// before acting on it (cobratree.IsUsage).
	exitUsage = 2
	// exitInterrupted is the exit status of a command that SIGINT or
	// SIGTERM stopped: 128 + SIGINT, as a shell reports an interrupt.
	// SIGTERM exits with it too, so that callers check one status for
	// "interrupted".
	exitInterrupted = 130
)

// invalidFlagValue returns the usage error for a value of the flag --name
// that is not one of the words it takes, the one form of every such error:
// invalid --output value "yaml": want human or json.
func invalidFlagValue(name, value string, words ...string) error {
	return cobratree.InvalidValue(name, value, oneOf(words))
}

// invalidEnvValue returns the error for a value of the variable name that
// sind cannot use, in the form of invalidFlagValue: invalid SIND_OUTPUT
// value "yaml": want human or json. It is no usage error, as the variable
// is not the command line: the command exits 1.
func invalidEnvValue(name, value, want string) error {
	return fmt.Errorf("invalid %s value %q: want %s", name, value, want)
}

// oneOf lists words as "a, b or c".
func oneOf(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// argsUsageError marks an error in the arguments of a command, whose
// message ends with the usage line of the command, line (withUsageLine).
type argsUsageError struct {
	err  error
	line string
}

func (e *argsUsageError) Error() string {
	if e.line == "" {
		return e.err.Error()
	}
	return fmt.Sprintf("%s (usage: %s)", e.err, e.line)
}

func (e *argsUsageError) Unwrap() error { return e.err }

// argsError marks err as a usage error in the arguments, whose message
// ends with the usage line of the command (withUsageLine); nil stays nil.
func argsError(err error) error {
	if err == nil {
		return nil
	}
	return cobratree.Usage(&argsUsageError{err: err})
}

// argsErrorf returns a usage error in the arguments with a formatted
// message.
func argsErrorf(format string, args ...any) error {
	return argsError(fmt.Errorf(format, args...))
}

// usageLines makes a usage error in the arguments that a command which
// runs returns end with the command's usage line (withUsageLine).
// cobratree.Strict ends the errors of argument checks with it; sind also
// checks some arguments, such as node names, only when the command runs.
func usageLines(cmd *cobra.Command) {
	if run := cmd.RunE; run != nil && !cmd.HasSubCommands() {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return withUsageLine(c, run(c, args))
		}
	}
	for _, sub := range cmd.Commands() {
		usageLines(sub)
	}
}

// withUsageLine makes a usage error in the arguments (argsError) end with
// the usage line of cmd, the command that rejected them, unless it already
// ends with one. Any other error, a flag value that is not valid among
// them, is returned as it is: the usage line does not show the flags.
func withUsageLine(cmd *cobra.Command, err error) error {
	marked, ok := errors.AsType[*argsUsageError](err)
	if !ok || marked.line != "" {
		return err
	}
	return cobratree.Usage(&argsUsageError{err: err, line: strings.TrimSuffix(cmd.UseLine(), " [flags]")})
}

// childExitError is the error of a program that sind ran on the user's
// terminal, docker exec or docker logs, that exited non-zero. The program
// has written its own diagnostics, so sind exits with its status and adds
// no error line.
type childExitError struct {
	err  *exec.ExitError
	code int
}

// newChildExitError returns the error of a program that exited non-zero.
// One killed by signal N exits 128+N, as a shell reports it, except that
// SIGINT and SIGTERM exit exitInterrupted: they reach docker when they are
// sent to sind's process group, and sind reports them as an interrupt
// whichever process ends first.
func newChildExitError(err *exec.ExitError) *childExitError {
	code := err.ExitCode()
	if ws, ok := err.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal())
		if ws.Signal() == syscall.SIGINT || ws.Signal() == syscall.SIGTERM {
			code = exitInterrupted
		}
	}
	return &childExitError{err: err, code: code}
}

func (e *childExitError) Error() string { return e.err.Error() }

func (e *childExitError) Unwrap() error { return e.err }
