// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"os"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

func main() {
	cmd := NewRootCommand()

	// Seed context with an error-level logger so errors are visible even
	// when PersistentPreRunE doesn't run (e.g., unknown flag, help).
	// PersistentPreRunE upgrades this based on the -v flag count.
	cmd.SetContext(sindlog.With(context.Background(), newLogger(os.Stderr, 0)))

	if err := cmd.Execute(); err != nil {
		sindlog.From(cmd.Context()).ErrorContext(cmd.Context(), err.Error())
		os.Exit(1)
	}
}
