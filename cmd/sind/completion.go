// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
)

// completeClusterNames provides shell completion for cluster name arguments.
func completeClusterNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := cmd.Context()
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

// completeRealmNames provides shell completion for the --realm flag.
func completeRealmNames(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	realms, err := cluster.GetRealms(ctx, clientFrom(ctx))
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	names := make([]string, len(realms))
	for i, r := range realms {
		names[i] = r.Name
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// registerOutputCompletion completes the values of the -o flag of cmd.
func registerOutputCompletion(cmd *cobra.Command) {
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions([]string{outputHuman, outputJSON}, cobra.ShellCompDirectiveNoFileComp))
}

// completeNodeNames provides shell completion for node name arguments.
func completeNodeNames(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return nodeNames(cmd)
}

// completeNodeName completes the one node argument of a command that takes
// a single node, and nothing after it.
func completeNodeName(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return nodeNames(cmd)
}

// completeWorkerNames completes the node arguments of delete worker, which
// removes workers only.
func completeWorkerNames(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return nodeNames(cmd, config.RoleWorker)
}

// nodeNames returns the realm's nodes of roles, every node without any, as
// completions.
func nodeNames(cmd *cobra.Command, roles ...config.Role) ([]string, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	names, err := cluster.GetNodeNames(ctx, client, realm, roles...)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeLogsArgs provides shell completion for the logs command.
// First arg: node name, second arg: a service of that node.
func completeLogsArgs(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return nodeNames(cmd)
	case 1:
		return nodeServices(cmd, args[0])
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// nodeServices returns the services of the node arg names as completions,
// those its role runs. An argument that names no single node has none.
func nodeServices(cmd *cobra.Command, arg string) ([]string, cobra.ShellCompDirective) {
	targets, err := parseNodeArgs(arg)
	if err != nil || len(targets) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := cmd.Context()
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	services, err := cluster.NodeServices(ctx, clientFrom(ctx), realm, targets[0].Cluster, targets[0].ShortName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return services, cobra.ShellCompDirectiveNoFileComp
}

// completeContainers returns the completion of the CONTAINER arguments of
// connect and disconnect container: the containers list returns, but those
// already named.
func completeContainers(list func(context.Context, *docker.Client, *mesh.Manager) ([]docker.ContainerName, error)) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		ctx := cmd.Context()
		realm, err := realmFromFlag(cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		client := clientFrom(ctx)
		names, err := list(ctx, client, meshMgrFrom(ctx, client, realm))
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var comps []string
		for _, name := range names {
			if !slices.Contains(args, string(name)) {
				comps = append(comps, string(name))
			}
		}
		return comps, cobra.ShellCompDirectiveNoFileComp
	}
}

// noFileCompletions makes every command in the tree that completes no
// argument, as one that takes none, complete nothing rather than the
// shell's file names.
func noFileCompletions(cmd *cobra.Command) {
	if cmd.Runnable() && !cmd.HasSubCommands() && cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0 {
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}
	for _, sub := range cmd.Commands() {
		noFileCompletions(sub)
	}
}

// completeAliases completes the aliases of a command group's subcommands,
// which cobra leaves out, once a prefix of one is typed: without one, the
// group lists each subcommand once, by its name.
func completeAliases(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var comps []string
	if len(args) == 0 && toComplete != "" {
		for _, sub := range cmd.Commands() {
			for _, alias := range sub.Aliases {
				if strings.HasPrefix(alias, toComplete) {
					comps = append(comps, cobra.CompletionWithDesc(alias, sub.Short))
				}
			}
		}
	}
	return comps, cobra.ShellCompDirectiveNoFileComp
}
