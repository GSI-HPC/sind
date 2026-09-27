// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"strings"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
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
	cmd.AddCommand(newMCPCommand(nil))

	builtins(cmd)
	requireKnownSubcommand(cmd)

	return cmd
}

// builtins adds cobra's help and completion commands to the tree now,
// rather than when it runs, so that the argument checks cover them: cobra's
// completion printed its help and succeeded for a shell it does not know,
// and its help printed the root help and succeeded for a topic that names
// no command. completion is a group like any other, so
// requireKnownSubcommand makes it strict; help gets helpTopic.
//
// The completion scripts are written to the output the root has when the
// command is made, not when it runs.
//
// Adapted from GSI-HPC/clusterctl internal/cli/root.go.
func builtins(cmd *cobra.Command) {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultCompletionCmd()
	for _, sub := range cmd.Commands() {
		if sub.Name() == "help" {
			sub.Args = helpTopic
		}
	}
}

// helpTopic is the argument check of the help command. The topic has to
// name a command, all of it: cobra's help prints the root help for a topic
// it cannot find, and the help of the nearest command for one with words
// left over, and succeeds either way.
func helpTopic(cmd *cobra.Command, args []string) error {
	// Find reports a word the root has no subcommand for as an error, but
	// still returns the root and the words it could not place.
	found, rest, _ := cmd.Root().Find(args)
	if len(rest) == 0 {
		return nil
	}
	return fmt.Errorf("unknown help topic %q: %w", strings.Join(args, " "), noUnknownSubcommand(found, rest))
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
