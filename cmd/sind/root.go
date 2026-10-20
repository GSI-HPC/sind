// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
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

		// --version prints what `sind version` does; sind version --json
		// stays the machine-readable form that sind-action reads.
		Version: versionLine(),

		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			applyVerbosity(cmd)
			cmd.SetContext(withStderr(cmd.Context(), cmd.ErrOrStderr()))
			if err := checkRealmFlag(cmd); err != nil {
				return err
			}
			return checkProgressFlag(cmd)
		},
	}

	cmd.SetVersionTemplate("{{.Version}}\n")

	cmd.PersistentFlags().String("realm", "", "realm namespace for resource isolation (overrides config and SIND_REALM)")
	cmd.PersistentFlags().CountP("verbose", "v", "increase log verbosity (-v=info, -vv=debug, -vvv=trace)")
	cmd.PersistentFlags().String("progress", "",
		"how create, delete and power show their progress on standard error: "+progressModes()+
			" (default: "+envProgress+", else auto, a live tree when standard error is a terminal; plain writes lines for a log)")
	cmd.PersistentFlags().String("progress-log", "",
		"append the progress events of create, delete and power to this file, one JSON object per line, "+
			"created readable by you alone (default: "+envProgressLog+")")

	_ = cmd.RegisterFlagCompletionFunc("realm", completeRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("progress", cobra.FixedCompletions(progressModeNames(), cobra.ShellCompDirectiveNoFileComp))

	cmd.AddCommand(newCreateCommand())
	cmd.AddCommand(newDeleteCommand())
	cmd.AddCommand(newConnectCommand())
	cmd.AddCommand(newDisconnectCommand())
	cmd.AddCommand(newGetCommand())
	cmd.AddCommand(newPowerCommand())
	cmd.AddCommand(newSSHCommand())
	cmd.AddCommand(newEnterCommand())
	cmd.AddCommand(newExecCommand())
	cmd.AddCommand(newLogsCommand())
	cmd.AddCommand(newDoctorCommand())
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newMCPCommand(mcpConfig()))

	builtins(cmd)
	requireKnownSubcommand(cmd)
	usageArgs(cmd)
	annotateMCPTools(cmd)

	return cmd
}

// applyVerbosity installs the logger for the -v count parsed so far.
func applyVerbosity(cmd *cobra.Command) {
	v, _ := cmd.Root().Flags().GetCount("verbose")
	cmd.SetContext(sindlog.With(cmd.Context(), newLogger(cmd.ErrOrStderr(), v)))
}

// checkRealmFlag rejects an invalid --realm before any command runs, as a
// usage error, so that a command that does not use the realm (version,
// get realms, mcp, help) does not accept a mistyped one silently.
func checkRealmFlag(cmd *cobra.Command) error {
	if !cmd.Root().Flags().Changed("realm") {
		return nil
	}
	r, _ := cmd.Root().Flags().GetString("realm")
	if err := config.CheckName("realm", r); err != nil {
		return usagef("--realm: %w", err)
	}
	return nil
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
// fail on any other argument, with exitUsage. Cobra's default for such
// groups is to print help and exit 0 even for a mistyped or removed
// subcommand, so a script calling e.g. `sind status` would silently
// succeed.
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
	cmd.Flags().VarP(new(groupHelp), "help", "h", "help for "+cmd.DisplayName())
	cmd.Flags().Lookup("help").NoOptDefVal = "true"
	_ = cmd.Flags().SetAnnotation("help", cobra.FlagSetByCobraAnnotation, []string{"true"})
}

// groupHelp is the -h, --help flag of a command group. cobra prints the
// help of a command whose help flag is set before it checks the command's
// arguments, so `sind get bogus --help` printed the help of get and
// succeeded. groupHelp reads as false to cobra whatever it is set to, so
// the group's argument check runs, and its RunE prints the help once the
// check has passed.
type groupHelp struct{}

func (*groupHelp) String() string { return "false" }

func (*groupHelp) Set(s string) error {
	_, err := strconv.ParseBool(s)
	return err
}

func (*groupHelp) Type() string { return "bool" }

func (*groupHelp) IsBoolFlag() bool { return true }

// removedCommands maps top-level commands that sind dropped to their
// replacement, for the hint on the error that a script gets.
var removedCommands = map[string]string{"status": "get cluster"}

// noUnknownSubcommand rejects positional arguments on a command group,
// suggesting the closest subcommand if there is one.
func noUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	msg := fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath())
	if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
		msg += fmt.Sprintf(" (did you mean %q?)", s[0])
	} else if hint, ok := removedCommands[args[0]]; ok && cmd.Parent() == nil {
		msg += fmt.Sprintf(" (it is now %q)", hint)
	}
	return errors.New(msg)
}
