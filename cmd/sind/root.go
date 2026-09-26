// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/njayp/ophis"
	"github.com/spf13/cobra"
)

// version and commit are set at build time via -ldflags.
var (
	version = "dev"
	commit  string
)

// NewRootCommand creates the root sind command.
func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sind",
		Short: "Slurm in Docker",
		Long:  "sind creates and manages containerized Slurm clusters for development, testing, and CI/CD workflows.",

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

	cmd.PersistentFlags().String("realm", "", "realm namespace for resource isolation (overrides config and SIND_REALM)")
	cmd.PersistentFlags().CountP("verbose", "v", "increase log verbosity (-v=info, -vv=debug, -vvv=trace)")

	cmd.AddCommand(newCreateCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newGetCommand())
	cmd.AddCommand(newPowerCommand())
	cmd.AddCommand(newSSHCommand())
	cmd.AddCommand(newEnterCommand())
	cmd.AddCommand(newExecCommand())
	cmd.AddCommand(newLogsCommand())
	cmd.AddCommand(newDoctorCommand())
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(ophis.Command(nil))

	requireKnownSubcommand(cmd)

	return cmd
}

// requireKnownSubcommand makes every command group (a command with
// subcommands but no run function) print its help when invoked bare and
// fail on any other argument. Cobra's default for such groups is to print
// help and exit 0 even for a mistyped or removed subcommand, so a script
// calling e.g. `sind status` would silently succeed.
func requireKnownSubcommand(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		requireKnownSubcommand(sub)
	}
	if !cmd.HasSubCommands() || cmd.Runnable() {
		return
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2 // cobra's default, applied lazily only on its own error path
	}
	cmd.Args = noUnknownSubcommand
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	}
}

// noUnknownSubcommand rejects positional arguments on a command group,
// suggesting the closest subcommand if there is one.
func noUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
		msg += fmt.Sprintf(" (did you mean %q?)", s[0])
	}
	return errors.New(msg)
}
