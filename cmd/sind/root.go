// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"strconv"

	"github.com/GSI-HPC/go-clikit/cobratree"
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
	cmd.SetFlagErrorFunc(flagError)

	cmd.PersistentFlags().String("realm", "", "realm namespace for resource isolation (overrides config and SIND_REALM)")
	cmd.PersistentFlags().CountP("verbose", "v", "increase log verbosity (-v=info, -vv=debug, -vvv=trace)")
	cmd.PersistentFlags().String("progress", "",
		"how create, delete, connect, disconnect and power show their progress on standard error: "+progressModes()+
			" (default: "+envProgress+", else auto, a live tree when standard error is a terminal; plain writes lines for a log)")
	cmd.PersistentFlags().String("progress-log", "",
		"append the progress events of create, delete, connect, disconnect and power to this file, one JSON object per line, "+
			"created readable by you alone (default: "+envProgressLog+", else $XDG_STATE_HOME/sind/activity.jsonl; an empty value writes no log)")

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
	cmd.AddCommand(newUICommand())
	cmd.AddCommand(newVersionCommand())
	cmd.AddCommand(newMCPCommand(mcpConfig()))

	// Groups refuse an unknown subcommand and help an unknown topic, naming
	// the command that replaced one sind dropped, and every argument check
	// returns a usage error that ends with the usage line.
	cobratree.Strict(cmd, map[string]string{"status": "get cluster"})
	addHelpTopics(cmd)
	groupHelpFlags(cmd)
	usageLines(cmd)
	annotateMCPTools(cmd)
	noFileCompletions(cmd)

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
		return cobratree.Usagef("--realm: %w", err)
	}
	return nil
}

// groupHelpFlags gives every command group, the root among them, the
// -h, --help flag groupHelp, so that a group refuses an unknown subcommand
// followed by --help. It runs after cobratree.Strict, which adds the
// completion group and makes every group print its help when given no
// argument.
func groupHelpFlags(cmd *cobra.Command) {
	for _, sub := range cmd.Commands() {
		groupHelpFlags(sub)
	}
	if !cmd.HasSubCommands() {
		return
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
