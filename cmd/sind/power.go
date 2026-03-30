// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/spf13/cobra"
)

type powerFunc func(ctx context.Context, client *docker.Client, realm, clusterName string, shortNames []string) error

func newPowerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "power",
		Short: "Control node power state",
	}

	cmds := []struct {
		use   string
		short string
		fn    powerFunc
	}{
		{"shutdown NODES", "Graceful shutdown", cluster.PowerShutdown},
		{"cut NODES", "Hard power off", cluster.PowerCut},
		{"on NODES", "Power on", cluster.PowerOn},
		{"reboot NODES", "Graceful reboot", cluster.PowerReboot},
		{"cycle NODES", "Hard power cycle", cluster.PowerCycle},
		{"freeze NODES", "Simulate unresponsive node", cluster.PowerFreeze},
		{"unfreeze NODES", "Resume frozen node", cluster.PowerUnfreeze},
	}

	for _, c := range cmds {
		fn := c.fn
		cmd.AddCommand(&cobra.Command{
			Use:               c.use,
			Short:             c.short,
			Args:              cobra.MinimumNArgs(1),
			ValidArgsFunction: completeNodeNames,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPower(cmd, strings.Join(args, ","), fn)
			},
		})
	}

	return cmd
}

func runPower(cmd *cobra.Command, nodeSpec string, fn powerFunc) error {
	targets, err := parseNodeArgs(nodeSpec)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)

	for clusterName, shortNames := range groupByCluster(targets) {
		if err := fn(ctx, client, realmFromFlag(cmd), clusterName, shortNames); err != nil {
			return err
		}
	}
	return nil
}
