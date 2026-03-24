// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/spf13/cobra"
)

const defaultReadinessInterval = 500 * time.Millisecond

func newCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a resource",
	}

	cmd.AddCommand(newCreateClusterCommand())
	cmd.AddCommand(newCreateWorkerCommand())

	return cmd
}

func newCreateClusterCommand() *cobra.Command {
	var configFile string

	cmd := &cobra.Command{
		Use:   "cluster [--name NAME] [--config FILE]",
		Short: "Create a Slurm cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			return runCreateCluster(cmd, name, configFile)
		},
	}

	cmd.Flags().String("name", "default", "cluster name")
	cmd.Flags().StringVar(&configFile, "config", "", "path to cluster configuration file")

	return cmd
}

func runCreateCluster(cmd *cobra.Command, name, configFile string) error {
	cfg, err := loadConfig(configFile)
	if err != nil {
		return err
	}

	if cmd.Flags().Changed("name") {
		cfg.Name = name
	}

	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)
	meshMgr := meshMgrFrom(ctx, client)
	if err := meshMgr.EnsureMesh(ctx); err != nil {
		return fmt.Errorf("setting up mesh: %w", err)
	}

	result, err := cluster.Create(ctx, client, meshMgr, cfg, defaultReadinessInterval)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Cluster %q created with %d node(s)\n", result.Name, len(result.Nodes))
	return nil
}

func loadConfig(path string) (*config.Cluster, error) {
	if path == "" {
		return config.Parse([]byte("kind: Cluster\n"))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	return config.Parse(data)
}
