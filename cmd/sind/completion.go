// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/cobra"
)

// completeClusterNames provides shell completion for cluster name arguments.
func completeClusterNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	names, err := cluster.DiscoverClusterNames(ctx, client, realm)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeNodeNames provides shell completion for node name arguments.
func completeNodeNames(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	names, err := cluster.GetNodeNames(ctx, client, realm)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeLogsArgs provides shell completion for the logs command.
// First arg: node name, second arg: service name.
func completeLogsArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeNodeNames(cmd, args, toComplete)
	case 1:
		return []string{"slurmctld", "slurmd", "slurmdbd", "mariadb", "sackd", "slurmrestd", "sshd", "munge"}, cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}
