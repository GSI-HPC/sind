// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"strings"

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
	}

	// Commands that start nodes update the realm's mesh (it starts a
	// stopped mesh, and rewrites the nodes' DNS records), so they hold the
	// realm lock.
	cmds := []struct {
		use     string
		aliases []string
		short   string
		fn      powerFunc
		locked  bool
	}{
		// off is the verb users guess first; without an alias cobra
		// suggests the opposite action, "on".
		{"shutdown NODES", []string{"off"}, "Gracefully shut down nodes (docker stop)", withRealm(cluster.PowerShutdown), false},
		{"cut NODES", nil, "Hard power off nodes (docker kill)", withRealm(cluster.PowerCut), false},
		{"on NODES", nil, "Power on nodes (docker start)", cluster.PowerOn, true},
		{"reboot NODES", nil, "Gracefully reboot nodes (shutdown, then on)", cluster.PowerReboot, true},
		{"cycle NODES", nil, "Hard power cycle nodes (cut, then on)", cluster.PowerCycle, true},
		{"freeze NODES", nil, "Simulate unresponsive nodes (docker pause)", withRealm(cluster.PowerFreeze), false},
		{"unfreeze NODES", nil, "Resume frozen nodes (docker unpause)", withRealm(cluster.PowerUnfreeze), false},
	}

	for _, c := range cmds {
		fn, locked := c.fn, c.locked
		cmd.AddCommand(&cobra.Command{
			Use:               c.use,
			Aliases:           c.aliases,
			Short:             c.short,
			Long:              c.short + ".\n\n" + nodesHelp,
			Example:           nodesExample("sind power "+strings.Fields(c.use)[0], "controller.dev"),
			Args:              cobra.MinimumNArgs(1),
			ValidArgsFunction: completeNodeNames,
			RunE: withProgress(func(cmd *cobra.Command, args []string) error {
				return runPower(cmd, strings.Join(args, " "), fn, locked)
			}),
		})
	}

	return cmd
}

func runPower(cmd *cobra.Command, nodeSpec string, fn powerFunc, locked bool) error {
	targets, err := parseNodeArgs(nodeSpec)
	if err != nil {
		return err
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
		errs = append(errs, fn(ctx, client, meshMgr, g.Cluster, g.ShortNames))
	}
	return errors.Join(errs...)
}
