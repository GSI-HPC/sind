// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/cobra"
)

type powerFunc func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error

// withRealm adapts a power function that needs only the realm, not the
// mesh, to powerFunc.
func withRealm(fn func(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error) powerFunc {
	return func(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
		return fn(ctx, client, meshMgr.Realm, clusterName, shortNames)
	}
}

func newPowerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "power",
		Short: "Control node power state",
		// cobra completes no alias of a subcommand.
		ValidArgsFunction: completeAliases,
	}

	// Commands that start nodes update the realm's mesh (it starts a
	// stopped mesh, and rewrites the nodes' DNS records), so they hold the
	// realm lock.
	cmds := []struct {
		use        string
		aliases    []string
		suggestFor []string
		short      string
		fn         powerFunc
		locked     bool
		// waits: the command starts nodes, and takes --wait.
		waits bool
	}{
		// off is the verb users guess first; without an alias cobra
		// suggests the opposite action, "on". The docker verbs that the
		// help names get a suggestion.
		{"shutdown NODES", []string{"off"}, []string{"stop"}, "Gracefully shut down nodes (docker stop; alias: off)", withRealm(cluster.PowerShutdown), false, false},
		{"cut NODES", nil, []string{"kill"}, "Hard power off nodes (docker kill)", withRealm(cluster.PowerCut), false, false},
		{"on NODES", nil, []string{"start"}, "Power on nodes (docker start)", cluster.PowerOn, true, true},
		{"reboot NODES", nil, []string{"restart"}, "Gracefully reboot nodes (shutdown, then on)", cluster.PowerReboot, true, true},
		{"cycle NODES", nil, nil, "Hard power cycle nodes (cut, then on)", cluster.PowerCycle, true, true},
		{"freeze NODES", nil, []string{"pause"}, "Simulate unresponsive nodes (docker pause)", withRealm(cluster.PowerFreeze), false, false},
		{"unfreeze NODES", nil, []string{"unpause", "resume"}, "Resume frozen nodes (docker unpause)", withRealm(cluster.PowerUnfreeze), false, true},
	}
	for _, c := range cmds {
		fn, locked := c.fn, c.locked
		sub := &cobra.Command{
			Use:               c.use,
			Aliases:           c.aliases,
			SuggestFor:        c.suggestFor,
			Short:             c.short,
			Long:              c.short + ".\n\n" + nodesHelp,
			Example:           nodesExample("sind power "+strings.Fields(c.use)[0], "controller.dev"),
			Args:              cobra.MinimumNArgs(1),
			ValidArgsFunction: completeNodeNames,
			RunE: withProgressOn(nodesTarget, func(cmd *cobra.Command, args []string) error {
				return runPower(cmd, strings.Join(args, " "), fn, locked)
			}),
		}
		if c.waits {
			sub.Flags().Duration("wait", 0,
				"how long to wait for the nodes and Slurm to become ready, as create cluster waits (0: no limit) "+
					"(default: "+envvar.Wait+", else no wait: the command returns once the containers run)")
		}
		cmd.AddCommand(sub)
	}

	return cmd
}

func runPower(cmd *cobra.Command, nodeSpec string, fn powerFunc, locked bool) error {
	targets, err := parseNodeArgs(nodeSpec)
	if err != nil {
		return err
	}
	var wait time.Duration
	var waits bool
	if cmd.Flags().Lookup("wait") != nil {
		if wait, waits, err = waitSetting(cmd); err != nil {
			return err
		}
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)

	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	if locked {
		unlock, err := acquireRealmLock(ctx, realm, "")
		if err != nil {
			return err
		}
		defer unlock()
	}
	meshMgr := meshMgrFrom(ctx, client, realm)

	// A cluster that fails does not keep the others from their power
	// action.
	var errs []error
	for _, g := range groupByCluster(targets) {
		err := fn(ctx, client, meshMgr, g.Cluster, g.ShortNames)
		if err == nil && waits {
			err = cluster.WaitNodes(ctx, client, realm, g.Cluster, g.ShortNames, wait, cluster.DefaultReadinessInterval)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
