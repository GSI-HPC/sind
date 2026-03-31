// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/njayp/ophis"
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

		Version:          version,
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			v, _ := cmd.Root().Flags().GetCount("verbose")
			logger := newLogger(cmd.ErrOrStderr(), v)
			cmd.SetContext(sindlog.With(cmd.Context(), logger))
			return nil
		},
	}

	cmd.Flags().String("realm", "", "realm namespace for resource isolation (overrides config and SIND_REALM)")
	cmd.Flags().CountP("verbose", "v", "increase log verbosity (-v=info, -vv=debug, -vvv=trace)")

	cmd.AddCommand(newCreateCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newGetCommand())
	cmd.AddCommand(newStatusCommand())
	cmd.AddCommand(newPowerCommand())
	cmd.AddCommand(newSSHCommand())
	cmd.AddCommand(newEnterCommand())
	cmd.AddCommand(newExecCommand())
	cmd.AddCommand(newLogsCommand())
	cmd.AddCommand(newDoctorCommand())
	cmd.AddCommand(ophis.Command(nil))

	return cmd
}
