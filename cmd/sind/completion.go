// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
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
	ctx := cmd.Context()
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
