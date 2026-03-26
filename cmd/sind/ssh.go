// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newSSHCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "ssh [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]",
		Short:              "SSH into a cluster node",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSH(cmd, args)
		},
	}

	return cmd
}

func runSSH(cmd *cobra.Command, args []string) error {
	sshOptions, node, command, err := parseSSHArgs(args)
	if err != nil {
		return err
	}

	target, err := parseNodeArgs(node)
	if err != nil {
		return err
	}
	if len(target) != 1 {
		return fmt.Errorf("ssh requires exactly one node, got %d", len(target))
	}

	isTTY := stdinIsTTY()
	dockerArgs := cluster.BuildSSHArgs(mesh.SSHContainerName, target[0].ShortName, target[0].Cluster, isTTY, sshOptions, command)

	return dockerExec(cmd, dockerArgs)
}

func newEnterCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "enter [CLUSTER]",
		Short: "Interactive shell on submitter or controller",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
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
	realm := realmFromFlag(cmd)
	meshMgr := meshMgrFrom(ctx, client, realm)

	target, err := cluster.EnterTarget(ctx, client, realm, clusterName)
	if err != nil {
		return err
	}

	isTTY := stdinIsTTY()
	dockerArgs := cluster.BuildSSHArgs(meshMgr.SSHContainerName(), target, clusterName, isTTY, nil, nil)

	return dockerExec(cmd, dockerArgs)
}

func newExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [CLUSTER] -- COMMAND [ARGS...]",
		Short: "Run a command on submitter or controller",
		// Flag parsing disabled to allow -- separator handling
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args)
		},
	}

	return cmd
}

func runExec(cmd *cobra.Command, args []string) error {
	clusterName, command, err := parseExecArgs(args)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)

	dockerArgs, err := cluster.ExecArgs(ctx, client, realmFromFlag(cmd), mesh.SSHContainerName, clusterName, command)
	if err != nil {
		return err
	}

	return dockerExec(cmd, dockerArgs)
}

func stdinIsTTY() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// parseSSHArgs separates SSH options, node target, and remote command.
// Format: [SSH_OPTIONS] NODE [-- COMMAND [ARGS...]]
func parseSSHArgs(args []string) (sshOptions []string, node string, command []string, err error) {
	if len(args) == 0 {
		return nil, "", nil, fmt.Errorf("node argument required")
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
		return nil, "", nil, fmt.Errorf("node argument required")
	}

	node = preArgs[len(preArgs)-1]
	sshOptions = preArgs[:len(preArgs)-1]

	return sshOptions, node, command, nil
}

// parseExecArgs separates cluster name and command from exec args.
// Format: [CLUSTER] -- COMMAND [ARGS...]
func parseExecArgs(args []string) (clusterName string, command []string, err error) {
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}

	if dashIdx < 0 {
		return "", nil, fmt.Errorf("-- separator and command required")
	}

	command = args[dashIdx+1:]
	if len(command) == 0 {
		return "", nil, fmt.Errorf("command required after --")
	}

	if dashIdx > 1 {
		return "", nil, fmt.Errorf("expected at most one argument before --, got %d", dashIdx)
	}

	clusterName = "default"
	if dashIdx == 1 {
		clusterName = args[0]
	}

	return clusterName, command, nil
}

// dockerExec runs a docker command with stdin/stdout/stderr from the cobra command.
func dockerExec(cmd *cobra.Command, args []string) error {
	dockerCmd := exec.CommandContext(cmd.Context(), "docker", args...)
	dockerCmd.Stdin = os.Stdin
	dockerCmd.Stdout = cmd.OutOrStdout()
	dockerCmd.Stderr = cmd.ErrOrStderr()
	return dockerCmd.Run()
}
