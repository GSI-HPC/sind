// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newSSHCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "ssh [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]",
		Short:              "SSH into a cluster node",
		DisableFlagParsing: true,
		ValidArgsFunction:  completeSSHNodeArg,
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

	target, err := parseNodeArgs(node)
	if err != nil {
		return err
	}
	if len(target) != 1 {
		return usagef("ssh requires exactly one node, got %d", len(target))
	}

	isTTY := stdinIsTTY(cmd.InOrStdin())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	sshContainer := mesh.NewManager(nil, realm).SSHContainerName()
	dockerArgs := cluster.BuildSSHArgs(sshContainer, target[0].ShortName, target[0].Cluster, realm, isTTY, sshOptions, command)

	return dockerExec(cmd, dockerArgs)
}

func newEnterCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "enter [CLUSTER]",
		Short:             "Interactive shell on submitter or controller",
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := config.DefaultClusterName
			if len(args) > 0 {
				name = args[0]
			}
			return runEnter(cmd, name)
		},
	}
}

func runEnter(cmd *cobra.Command, clusterName string) error {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}

	target, workDir, err := cluster.EnterTarget(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}

	containerName := cluster.ContainerName(realm, clusterName, target)
	isTTY := stdinIsTTY(cmd.InOrStdin())
	dockerArgs := cluster.BuildContainerExecArgs(containerName, workDir, isTTY, nil)

	return dockerExec(cmd, dockerArgs)
}

func newExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [CLUSTER] -- COMMAND [ARGS...]",
		Short: "Run a command on submitter or controller",
		// runExec parses the flags itself: with cobra parsing them, shell
		// completion could not tell whether the -- was typed yet.
		DisableFlagParsing: true,
		ValidArgsFunction:  completeExecClusterArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args)
		},
	}

	return cmd
}

func runExec(cmd *cobra.Command, args []string) error {
	// Parse exec's own flags, which end at the --, so that --realm, -v and
	// --help also work after "exec", as in the "exec --realm R CLUSTER --
	// COMMAND" the MCP server runs.
	flags := cmd.Flags()
	flags.AddFlagSet(cmd.InheritedFlags()) // --realm and -v
	if err := flags.Parse(args); err != nil {
		return err
	}
	if help, _ := flags.GetBool("help"); help {
		return cmd.Help()
	}
	applyVerbosity(cmd)

	clusterName, command, err := parseExecArgs(flags.Args(), flags.ArgsLenAtDash())
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}

	target, workDir, err := cluster.EnterTarget(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}

	containerName := cluster.ContainerName(realm, clusterName, target)
	dockerArgs := cluster.BuildContainerExecArgs(containerName, workDir, false, command)

	return dockerExec(cmd, dockerArgs)
}

// stdinIsTTY reports whether stdin, the command's input, is a terminal. A
// reader that is not a file, as set with cmd.SetIn, is not.
func stdinIsTTY(stdin io.Reader) bool {
	f, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// parseSSHArgs separates SSH options, node target, and remote command.
// Format: [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]
func parseSSHArgs(args []string) (sshOptions []string, node string, command []string, err error) {
	if len(args) == 0 {
		return nil, "", nil, usagef("node argument required")
	}

	// Find the -- separator
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}

	var preArgs []string
	if dashIdx >= 0 {
		preArgs = args[:dashIdx]
		command = args[dashIdx+1:]
	} else {
		preArgs = args
	}

	// Last non-option arg before -- is the node
	if len(preArgs) == 0 {
		return nil, "", nil, usagef("node argument required")
	}

	node = preArgs[len(preArgs)-1]
	sshOptions = preArgs[:len(preArgs)-1]

	return sshOptions, node, command, nil
}

// parseExecArgs separates cluster name and command from exec args.
// Format: [CLUSTER] -- COMMAND [ARGS...]. cobra removes the -- from args;
// dashIdx is the number of arguments before it (cmd.ArgsLenAtDash), -1
// when there was none.
func parseExecArgs(args []string, dashIdx int) (clusterName string, command []string, err error) {
	if dashIdx < 0 {
		return "", nil, usagef("-- separator and command required")
	}

	command = args[dashIdx:]
	if len(command) == 0 {
		return "", nil, usagef("command required after --")
	}

	if dashIdx > 1 {
		return "", nil, usagef("expected at most one argument before --, got %d", dashIdx)
	}

	clusterName = "default"
	if dashIdx == 1 {
		clusterName = args[0]
		if err := config.CheckName("cluster", clusterName); err != nil {
			return "", nil, usage(err)
		}
	}

	return clusterName, command, nil
}

// sshValueFlags lists SSH flags that consume the next argument as a value.
var sshValueFlags = map[string]bool{
	"-b": true, "-c": true, "-D": true, "-E": true, "-e": true,
	"-F": true, "-I": true, "-i": true, "-J": true, "-L": true,
	"-l": true, "-m": true, "-O": true, "-o": true, "-p": true,
	"-Q": true, "-R": true, "-S": true, "-W": true, "-w": true,
}

// completeSSHNodeArg provides completion for the NODE argument of the ssh command.
// It uses a heuristic to skip SSH option flags and their values.
func completeSSHNodeArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// After --, we're in remote command territory.
	for _, a := range args {
		if a == "--" {
			return nil, cobra.ShellCompDirectiveDefault
		}
	}

	// If the previous arg is an SSH flag that takes a value, this position is
	// the flag's value — not the node name.
	if len(args) > 0 && sshValueFlags[args[len(args)-1]] {
		return nil, cobra.ShellCompDirectiveDefault
	}

	// Check if a node name was already provided (a non-flag arg that isn't a flag value).
	for i, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if i > 0 && sshValueFlags[args[i-1]] {
			continue
		}
		// Node name already present.
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	if strings.HasPrefix(toComplete, "-") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return completeNodeNames(cmd, nil, toComplete)
}

// completeExecClusterArg provides completion for the CLUSTER argument of the exec command.
func completeExecClusterArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// After --, we're in command territory.
	for _, a := range args {
		if a == "--" {
			return nil, cobra.ShellCompDirectiveDefault
		}
	}

	// Cluster name already provided — wait for --.
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return completeClusterNames(cmd, nil, toComplete)
}

// dockerExec runs a docker command with stdin/stdout/stderr from the cobra command.
func dockerExec(cmd *cobra.Command, args []string) error {
	dockerCmd := exec.CommandContext(cmd.Context(), "docker", args...)
	dockerCmd.Stdin = cmd.InOrStdin()
	dockerCmd.Stdout = cmd.OutOrStdout()
	dockerCmd.Stderr = cmd.ErrOrStderr()
	return dockerCmd.Run()
}
