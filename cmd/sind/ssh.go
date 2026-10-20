// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newSSHCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh [SSH_OPTIONS] [USER@]NODE [-- COMMAND [ARGS...]]",
		Short: "SSH into a cluster node",
		Long: `SSH into a cluster node.

SSH options, before or after NODE, are passed through to ssh. A remote
command must follow --: NODE is the only other argument before it. sind's
own flags, such as --realm, go before ssh: sind --realm R ssh NODE.

ssh runs in the realm's SSH relay container, so the ports and files that
options such as -L and -i name are the relay's. To forward a port to this
machine, use your own ssh with the exported config:

  ssh -F "$(sind get ssh-config)" -L 8080:localhost:80 controller.default.sind.sind`,
		DisableFlagParsing: true,
		// Every argument after ssh is passed through: sind's flags go
		// before it.
		DisableFlagsInUseLine: true,
		ValidArgsFunction:     completeSSHNodeArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSH(cmd, args)
		},
	}

	return cmd
}

func runSSH(cmd *cobra.Command, args []string) error {
	// ssh passes its arguments through to SSH, so cobra never sees -h or
	// --help. OpenSSH has neither option, so a leading one asks for sind's
	// help.
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		return cmd.Help()
	}

	sshOptions, node, command, err := parseSSHArgs(args)
	if err != nil {
		return err
	}

	// USER@NODE logs in as USER, as ssh -l USER NODE does.
	if user, host, ok := strings.Cut(node, "@"); ok {
		if err := config.CheckUserName(user); err != nil {
			return argsError(err)
		}
		sshOptions = append(sshOptions, "-l", user)
		node = host
	}

	target, err := parseNodeArgs(node)
	if err != nil {
		return err
	}
	if len(target) != 1 {
		return argsErrorf("ssh requires exactly one node, got %d", len(target))
	}

	// Through a pseudo-terminal, the remote output would come back with CRLF
	// line endings and with stderr in stdout, so -t is only for a terminal
	// at both ends: a script that captures or redirects the output gets it
	// unchanged even when it runs in an interactive shell.
	isTTY := interactive(cmd)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	// The relay would answer a missing node with ssh's own error, and a
	// realm without clusters has no relay for docker to exec in.
	ctx := cmd.Context()
	if err := cluster.CheckNode(ctx, clientFrom(ctx), realm, target[0].Cluster, target[0].ShortName); err != nil {
		return err
	}
	sshContainer := mesh.NewManager(nil, realm).SSHContainerName()
	dockerArgs := cluster.BuildSSHArgs(sshContainer, target[0].ShortName, target[0].Cluster, realm, isTTY, sshOptions, command)

	return dockerExec(cmd, dockerArgs)
}

func newEnterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "enter [CLUSTER]",
		Short:             "Open a shell on the submitter or controller",
		Long:              "Open an interactive login shell on the submitter or controller.\n\n" + defaultClusterHelp("CLUSTER"),
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnter(cmd, clusterArg(args))
		},
	}
	addUserFlag(cmd, "shell")
	return cmd
}

// addUserFlag adds the --user flag of enter and exec, which run what (the
// shell or the command) as a cluster user.
func addUserFlag(cmd *cobra.Command, what string) {
	cmd.Flags().StringP("user", "u", "", "run the "+what+" as this cluster user, in its home directory")
}

// userFlag returns the value of --user, checked to be a user name.
func userFlag(cmd *cobra.Command) (string, error) {
	user, _ := cmd.Flags().GetString("user")
	if user == "" {
		return "", nil
	}
	if err := config.CheckUserName(user); err != nil {
		return "", usage(err)
	}
	return user, nil
}

func runEnter(cmd *cobra.Command, clusterName string) error {
	return execOnEnterTarget(cmd, clusterName, isTerminal(cmd.InOrStdin()), nil)
}

// execOnEnterTarget runs command, or a login shell when it is nil, as --user
// on the node enter and exec go to: the submitter or the controller.
func execOnEnterTarget(cmd *cobra.Command, clusterName string, isTTY bool, command []string) error {
	user, err := userFlag(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	target, workDir, err := cluster.EnterTarget(ctx, clientFrom(ctx), realm, clusterName)
	if err != nil {
		return err
	}
	containerName := cluster.ContainerName(realm, clusterName, target)
	return dockerExec(cmd, cluster.BuildContainerExecArgs(containerName, user, workDir, isTTY, command))
}

func newExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [flags] [CLUSTER] -- COMMAND [ARGS...]",
		Short: "Run a command on the submitter or controller",
		Long:  "Run a command on the submitter or controller.\n\n" + defaultClusterHelp("CLUSTER"),
		// runExec parses the flags itself: with cobra parsing them, shell
		// completion could not tell whether the -- was typed yet.
		DisableFlagParsing: true,
		// The flags go before the --; after it, they are the command's.
		DisableFlagsInUseLine: true,
		ValidArgsFunction:     completeExecClusterArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args)
		},
	}
	addUserFlag(cmd, "command")

	return cmd
}

func runExec(cmd *cobra.Command, args []string) error {
	// Parse exec's own flags, which end at the --, so that --realm, -v,
	// --progress, --user and --help also work after "exec", as in the
	// "exec --realm R CLUSTER -- COMMAND" the MCP server runs.
	flags := cmd.Flags()
	flags.AddFlagSet(cmd.InheritedFlags()) // --realm, --progress and -v
	if err := flags.Parse(args); err != nil {
		return err
	}
	if help, _ := flags.GetBool("help"); help {
		return cmd.Help()
	}
	// PersistentPreRunE checked --progress before these flags were parsed.
	if err := checkProgressFlag(cmd); err != nil {
		return err
	}
	applyVerbosity(cmd)

	clusterName, command, err := parseExecArgs(flags.Args(), flags.ArgsLenAtDash())
	if err != nil {
		return err
	}
	return execOnEnterTarget(cmd, clusterName, interactive(cmd), command)
}

// interactive reports whether the command talks to a terminal at both ends.
// Only then does a command get a pseudo-terminal: captured or redirected
// output must not come back with CRLF line endings. MCP tool calls have no
// terminal, so they never get one.
func interactive(cmd *cobra.Command) bool {
	return isTerminal(cmd.InOrStdin()) && isTerminal(cmd.OutOrStdout())
}

// isTerminal reports whether stream is a file open on a terminal. A reader
// or writer that is not a file, as set with cmd.SetIn or cmd.SetOut, is not.
func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// parseSSHArgs separates SSH options, node target, and remote command.
// Format: [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]
//
// Before the --, the arguments are read the way ssh's getopt reads them:
// SSH options, before or after the node, and their values. The node is the
// one argument that is neither. Another such argument is a usage error,
// not a second node: ssh would run it as the remote command, which sind
// ssh takes only after the --, so the misplaced command is named rather
// than taken for the node.
func parseSSHArgs(args []string) (sshOptions []string, node string, command []string, err error) {
	preArgs := args
	if i := slices.Index(args, "--"); i >= 0 {
		preArgs, command = args[:i], args[i+1:]
	}

	for i := 0; i < len(preArgs); i++ {
		a := preArgs[i]
		switch {
		case strings.HasPrefix(a, "--"):
			return nil, "", nil, argsErrorf("ssh has no option %s: sind's own flags go before ssh, as in sind --realm R ssh NODE", a)
		case isSSHOption(a):
			sshOptions = append(sshOptions, a)
			if sshOptionTakesNext(a) {
				if i+1 == len(preArgs) {
					return nil, "", nil, argsErrorf("ssh option %s needs a value", a)
				}
				i++
				sshOptions = append(sshOptions, preArgs[i])
			}
		case node == "":
			node = a
		default:
			return nil, "", nil, argsErrorf("unexpected argument %q after node %q: a remote command goes after --, as in sind ssh NODE -- COMMAND", a, node)
		}
	}

	if node == "" {
		return nil, "", nil, argsErrorf("node argument required")
	}
	return sshOptions, node, command, nil
}

// parseExecArgs separates cluster name and command from exec args.
// Format: [CLUSTER] -- COMMAND [ARGS...]. cobra removes the -- from args;
// dashIdx is the number of arguments before it (cmd.ArgsLenAtDash), -1
// when there was none.
func parseExecArgs(args []string, dashIdx int) (clusterName string, command []string, err error) {
	if dashIdx < 0 {
		return "", nil, argsErrorf("-- separator and command required")
	}

	command = args[dashIdx:]
	if len(command) == 0 {
		return "", nil, argsErrorf("command required after --")
	}

	if dashIdx > 1 {
		return "", nil, argsErrorf("expected at most one argument before --, got %d", dashIdx)
	}

	clusterName = "default"
	if dashIdx == 1 {
		clusterName = args[0]
		if err := config.CheckName("cluster", clusterName); err != nil {
			return "", nil, argsError(err)
		}
	}

	return clusterName, command, nil
}

// sshValueOptions are the SSH options that take a value, from the getopt
// string of OpenSSH's ssh.c.
const sshValueOptions = "BDEFIJLOPQRSWbceilmopw"

// isSSHOption reports whether arg is a group of SSH options, such as -v,
// -tt or -p2222, which getopt takes to be any argument beginning with "-"
// but "-" itself.
func isSSHOption(arg string) bool {
	return len(arg) > 1 && arg[0] == '-'
}

// sshOptionTakesNext reports whether arg, a group of SSH options, ends in
// an option whose value is the next argument: in a group, the first option
// that takes a value takes the rest of the group as its value, or the next
// argument when nothing is left, as -L in -vL does.
func sshOptionTakesNext(arg string) bool {
	for i := 1; i < len(arg); i++ {
		if strings.IndexByte(sshValueOptions, arg[i]) >= 0 {
			return i == len(arg)-1
		}
	}
	return false
}

// completeSSHNodeArg provides completion for the NODE argument of the ssh
// command. It reads the arguments as parseSSHArgs does, skipping SSH
// options and their values.
func completeSSHNodeArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// After --, we're in remote command territory.
	if slices.Contains(args, "--") {
		return nil, cobra.ShellCompDirectiveDefault
	}

	value := false // whether the next argument is an option's value
	for _, a := range args {
		switch {
		case value:
			value = false
		case isSSHOption(a):
			value = sshOptionTakesNext(a)
		default:
			// Node name already present.
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}

	// This position is the value of an SSH option, not the node name.
	if value {
		return nil, cobra.ShellCompDirectiveDefault
	}

	if strings.HasPrefix(toComplete, "-") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return completeNodeNames(cmd, nil, toComplete)
}

// completeExecClusterArg provides completion for the CLUSTER argument of the exec command.
func completeExecClusterArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// After --, we're in command territory.
	if slices.Contains(args, "--") {
		return nil, cobra.ShellCompDirectiveDefault
	}

	// Cluster name already provided — wait for --.
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return completeClusterNames(cmd, nil, toComplete)
}

// dockerExec runs a docker command with stdin/stdout/stderr from the cobra
// command. When docker exits non-zero, the error is a *childExitError, so
// that sind exits with docker's status, which docker exec takes from the
// command it ran.
func dockerExec(cmd *cobra.Command, args []string) error {
	dockerCmd := exec.CommandContext(cmd.Context(), "docker", args...)
	dockerCmd.Stdin = cmd.InOrStdin()
	dockerCmd.Stdout = cmd.OutOrStdout()
	dockerCmd.Stderr = cmd.ErrOrStderr()
	err := dockerCmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return newChildExitError(exitErr)
	}
	return err
}
