// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// exitInterrupted is the exit status of a command that SIGINT or SIGTERM
// stopped: 128 + SIGINT, as a shell reports an interrupt. SIGTERM exits
// with it too, so that callers check one status for "interrupted".
const exitInterrupted = 130

func main() {
	ctx, stop := interruptContext()
	code := run(ctx)
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

// run executes the command tree and returns the process exit status. A
// command that fails after ctx was cancelled was interrupted, whatever its
// error says: a docker call killed by the cancellation reports only
// "signal: killed".
func run(ctx context.Context) int {
	cmd := NewRootCommand()

	// Seed context with an error-level logger so errors are visible even
	// when PersistentPreRunE doesn't run (e.g., unknown flag, help).
	// PersistentPreRunE upgrades this based on the -v flag count.
	cmd.SetContext(sindlog.With(ctx, newLogger(os.Stderr, 0)))

	err := cmd.Execute()
	if err == nil {
		return 0
	}
	log := sindlog.From(cmd.Context())
	if ctx.Err() != nil {
		log.ErrorContext(cmd.Context(), "interrupted: "+err.Error())
		return exitInterrupted
	}
	log.ErrorContext(cmd.Context(), err.Error())
	return 1
}
