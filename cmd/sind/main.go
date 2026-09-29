// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/GSI-HPC/sind/internal/termtext"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

func main() {
	ctx, stop := interruptContext()
	code := run(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

// interruptContext returns a context that the first SIGINT or SIGTERM
// cancels, so that the work in progress stops and deferred cleanup (e.g.
// Create rollback) runs; `timeout` and `docker stop` send SIGTERM.
//
// The handler is removed before the returned context is cancelled, so a
// second signal gets the default action and ends the process at once, even
// while a hung rollback does not watch the context. Returning a separate
// context that is cancelled only after stop, rather than the signal
// context itself, means nothing that waits on the context can see it end
// while the handler would still swallow a second signal.
func interruptContext() (context.Context, context.CancelFunc) {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithCancelCause(context.Background())
	context.AfterFunc(sigCtx, func() {
		stop()
		cancel(context.Cause(sigCtx))
	})
	return ctx, func() {
		stop()
		cancel(nil)
	}
}

// run executes the command tree with args and returns the process exit
// status. A command that fails after ctx was cancelled was interrupted,
// whatever its error says: a docker call killed by the cancellation reports
// only "signal: killed".
//
// A program that ssh, exec, enter or logs ran on the user's terminal and
// that exited non-zero gives its own status and adds no error line: it has
// written its own diagnostics.
//
// The error is written to stderr escaped: it can quote what docker or a
// container wrote, and the logger quotes attribute values but not the
// message, so a control sequence in it would otherwise reach the terminal.
func run(ctx context.Context, args []string, stderr io.Writer) int {
	cmd := NewRootCommand()
	cmd.SetArgs(args)
	cmd.SetErr(stderr)

	// Seed context with an error-level logger so errors are visible even
	// when PersistentPreRunE doesn't run (e.g., unknown flag, help).
	// PersistentPreRunE upgrades this based on the -v flag count.
	cmd.SetContext(sindlog.With(ctx, newLogger(stderr, 0)))

	err := cmd.Execute()
	if err == nil {
		return exitOK
	}
	if child, ok := errors.AsType[*childExitError](err); ok {
		if ctx.Err() != nil {
			return exitInterrupted
		}
		return child.code
	}
	msg := termtext.EscapeText(err.Error())
	log := sindlog.From(cmd.Context())
	if ctx.Err() != nil {
		log.ErrorContext(cmd.Context(), "interrupted: "+msg)
		return exitInterrupted
	}
	log.ErrorContext(cmd.Context(), msg)
	if isUsageError(err) {
		return exitUsage
	}
	return exitFailure
}
