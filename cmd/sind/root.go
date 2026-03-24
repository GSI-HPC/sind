// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags.
var version = "dev"

// NewRootCommand creates the root sind command.
func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sind",
		Short: "Slurm in Docker",
		Long:  "sind creates and manages containerized Slurm clusters for development, testing, and CI/CD workflows.",

		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	return cmd
}
