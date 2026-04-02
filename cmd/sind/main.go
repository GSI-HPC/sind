// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"os/signal"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

func main() {
	// Catch SIGINT so the process shuts down gracefully, giving deferred
	// cleanup functions (e.g. Create rollback) a chance to run. A second
	// SIGINT terminates immediately.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd := NewRootCommand()

	// Seed context with an error-level logger so errors are visible even
	// when PersistentPreRunE doesn't run (e.g., unknown flag, help).
	// PersistentPreRunE upgrades this based on the -v flag count.
	cmd.SetContext(sindlog.With(ctx, newLogger(os.Stderr, 0)))

	if err := cmd.Execute(); err != nil {
		sindlog.From(cmd.Context()).ErrorContext(cmd.Context(), err.Error())
		os.Exit(1)
	}
}
