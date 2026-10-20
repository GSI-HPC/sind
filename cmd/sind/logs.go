// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/cobra"
)

func newLogsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs NODE [SERVICE]",
		Short: "Show container or service logs",
		Long: `Show container logs (stdout/stderr) or service journal logs.

Without a SERVICE argument, shows container logs.
With a SERVICE argument, shows journalctl output for that systemd unit.

` + nodeHelp,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeLogsArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, args)
		},
	}

	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	cmd.Flags().IntP("tail", "n", 0, "show only the last N lines (default: all lines; 0 shows none)")

	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	node, err := parseOneNode("logs", args[0])
	if err != nil {
		return err
	}

	opts := cluster.LogOptions{}
	opts.Follow, _ = cmd.Flags().GetBool("follow")
	if cmd.Flags().Changed("tail") {
		tail, _ := cmd.Flags().GetInt("tail")
		if tail < 0 {
			return usagef("--tail must not be negative, got %d", tail)
		}
		opts.Tail = &tail
	}

	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	// docker would answer a missing node with its own error, naming the
	// internal container.
	ctx := cmd.Context()
	if err := cluster.CheckNode(ctx, clientFrom(ctx), realm, node.Cluster, node.ShortName); err != nil {
		return err
	}
	var dockerArgs []string
	if len(args) == 2 {
		dockerArgs = cluster.ServiceLogArgs(realm, node.ShortName, node.Cluster, args[1], opts)
	} else {
		dockerArgs = cluster.ContainerLogArgs(realm, node.ShortName, node.Cluster, opts)
	}

	return dockerExec(cmd, dockerArgs)
}
