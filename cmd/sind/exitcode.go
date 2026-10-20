// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The exit statuses of sind. Scripts branch on them, so they may be added
// to but never renumbered. ssh, exec, enter and logs exit with the status
// of the program they run instead (childExitError).
const (
	exitOK = 0
	// exitFailure is the exit status of a command that ran and failed.
	exitFailure = 1
	// exitUsage is the exit status of a command line that sind rejected
	// before acting on it (usageError).
	exitUsage = 2
	// exitInterrupted is the exit status of a command that SIGINT or
	// SIGTERM stopped: 128 + SIGINT, as a shell reports an interrupt.
	// SIGTERM exits with it too, so that callers check one status for
	// "interrupted".
	exitInterrupted = 130
)

// usageError marks an error in the command line: an unknown command or
// flag, a flag value or argument that is not valid, or the wrong number of
// arguments. It exits exitUsage.
type usageError struct {
	err error
	// inArgs marks an error in the arguments, such as the wrong number of
	// them, whose message ends with the usage line of the command,
	// line (withUsageLine).
	inArgs bool
	line   string
}

func (e *usageError) Error() string {
	if e.line == "" {
		return e.err.Error()
	}
	return fmt.Sprintf("%s (usage: %s)", e.err, e.line)
}

func (e *usageError) Unwrap() error { return e.err }

// usage marks err as a usage error; nil stays nil.
func usage(err error) error {
	if err == nil {
		return nil
	}
	return &usageError{err: err}
}

// usagef returns a usage error with a formatted message.
func usagef(format string, args ...any) error {
	return usage(fmt.Errorf(format, args...))
}

// invalidFlagValue returns the usage error for a value of the flag --name
// that is not one of the words it takes, the one form of every such error:
// invalid --output value "yaml": want human or json.
func invalidFlagValue(name, value string, words ...string) error {
	return invalidValue(name, value, oneOf(words))
}

// invalidValue returns the usage error for a value of the flag --name, with
// what the flag wants instead.
func invalidValue(name, value, want string) error {
	return usagef("invalid --%s value %q: want %s", name, value, want)
}

// oneOf lists words as "a, b or c".
func oneOf(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// flagValueWants says what a flag of each pflag type wants, for the values
// pflag's parsers reject with their own text (strconv.ParseInt: …, time:
// missing unit in duration …).
var flagValueWants = map[string]string{
	"int":      "a whole number",
	"count":    "a whole number",
	"bool":     "true or false",
	"duration": "a duration with a unit, such as 90s, 5m or 1h30m",
}

// flagError is the FlagErrorFunc of every command. A value that pflag
// cannot parse as the flag's type reads as the other invalid flag values
// do (invalidValue), instead of Go's parser error; every other flag error
// is kept. Both are usage errors (isUsageError).
func flagError(_ *cobra.Command, err error) error {
	if invalid, ok := errors.AsType[*pflag.InvalidValueError](err); ok {
		if want, ok := flagValueWants[invalid.GetFlag().Value.Type()]; ok {
			return invalidValue(invalid.GetFlag().Name, invalid.GetValue(), want)
		}
	}
	return err
}

// argsError marks err as a usage error in the arguments, whose message
// ends with the usage line of the command (withUsageLine); nil stays nil.
func argsError(err error) error {
	if err == nil {
		return nil
	}
	return &usageError{err: err, inArgs: true}
}

// argsErrorf returns a usage error in the arguments with a formatted
// message.
func argsErrorf(format string, args ...any) error {
	return argsError(fmt.Errorf(format, args...))
}

// usageArgs makes every argument check in the tree report a usage error,
// and every usage error in the arguments of a command that runs end with
// the command's usage line (withUsageLine). cobra's own checks, such as cobra.ExactArgs,
// return plain errors, which would otherwise exit exitFailure.
//
// Adapted from GSI-HPC/clusterctl internal/cli/root.go.
func usageArgs(cmd *cobra.Command) {
	// A command group's errors name the subcommand they do not know, and
	// help's the topic: their usage line would not help.
	leaf := !cmd.HasSubCommands() && cmd.Name() != "help"
	if check := cmd.Args; check != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			err := check(c, args)
			if !leaf {
				return usage(err)
			}
			// cobra's arity messages name neither the argument nor the
			// command, and the usage block is silenced.
			return withUsageLine(c, argsError(err))
		}
	}
	if run := cmd.RunE; run != nil && leaf {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return withUsageLine(c, run(c, args))
		}
	}
	for _, sub := range cmd.Commands() {
		usageArgs(sub)
	}
}

// withUsageLine makes a usage error in the arguments (argsError) end with
// the usage line of cmd, the command that rejected them, unless it already
// ends with one. Any other error, a flag value that is not valid among
// them, is returned as it is: the usage line does not show the flags.
func withUsageLine(cmd *cobra.Command, err error) error {
	var marked *usageError
	if !errors.As(err, &marked) || !marked.inArgs || marked.line != "" {
		return err
	}
	return &usageError{err: err, inArgs: true, line: strings.TrimSuffix(cmd.UseLine(), " [flags]")}
}

// isUsageError reports whether err is an error in the command line. A flag
// that pflag cannot parse is one wherever it is parsed: cobra passes only
// the flags of the command that runs through its FlagErrorFunc, not those
// of the parents it traverses (TraverseChildren), and exec parses its own.
func isUsageError(err error) bool {
	var (
		notExist      *pflag.NotExistError
		valueRequired *pflag.ValueRequiredError
		invalidValue  *pflag.InvalidValueError
		invalidSyntax *pflag.InvalidSyntaxError
		marked        *usageError
	)
	return errors.As(err, &marked) ||
		errors.As(err, &notExist) ||
		errors.As(err, &valueRequired) ||
		errors.As(err, &invalidValue) ||
		errors.As(err, &invalidSyntax)
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
