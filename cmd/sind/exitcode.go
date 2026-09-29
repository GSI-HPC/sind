// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The exit statuses of sind. Scripts branch on them, so they may be added
// to but never renumbered.
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
}

func (e *usageError) Error() string { return e.err.Error() }

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

// usageArgs makes every argument check in the tree report a usage error.
// cobra's own checks, such as cobra.ExactArgs, return plain errors, which
// would otherwise exit exitFailure.
//
// Adapted from GSI-HPC/clusterctl internal/cli/root.go.
func usageArgs(cmd *cobra.Command) {
	if check := cmd.Args; check != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			return usage(check(c, args))
		}
	}
	for _, sub := range cmd.Commands() {
		usageArgs(sub)
	}
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
