// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/cobra"
)

func newLogsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs NODE [SERVICE]",
		Short: "Show container or service logs",
		Long: `Show container logs (stdout/stderr) or service journal logs.

Without a SERVICE argument, shows container logs.
With a SERVICE argument, shows journalctl output for that systemd unit.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			follow, _ := cmd.Flags().GetBool("follow")
			return runLogs(cmd, args, follow)
		},
	}

	cmd.Flags().BoolP("follow", "f", false, "follow log output")

	return cmd
}

func runLogs(cmd *cobra.Command, args []string, follow bool) error {
	targets, err := parseNodeArgs(args[0])
	if err != nil {
		return err
	}
	if len(targets) != 1 {
		return fmt.Errorf("logs requires exactly one node, got %d", len(targets))
	}

	node := targets[0]
	var dockerArgs []string
	if len(args) == 2 {
		dockerArgs = cluster.ServiceLogArgs(node.ShortName, node.Cluster, args[1], follow)
	} else {
		dockerArgs = cluster.ContainerLogArgs(node.ShortName, node.Cluster, follow)
	}

	return dockerExec(cmd, dockerArgs)
}
