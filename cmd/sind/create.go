// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/spf13/afero"
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
		Use:               "cluster [NAME] [--config FILE]",
		Short:             "Create a Slurm cluster",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			}
			return runCreateCluster(cmd, name, configFile)
		},
	}

	cmd.Flags().StringVar(&configFile, "config", "", "path to cluster configuration file")
	cmd.Flags().String("data", ".", `host directory to mount as /data (use "volume" for Docker volume)`)
	cmd.Flags().Bool("pull", false, "pull images before creating containers")

	return cmd
}

func runCreateCluster(cmd *cobra.Command, name, configFile string) error {
	cfg, err := loadConfig(configFile)
	if err != nil {
		return err
	}

	if name != "" {
		cfg.Name = name
	}

	// Apply --data flag when config doesn't already specify data storage.
	if cfg.Storage.DataStorage.Type == "" && cfg.Storage.DataStorage.HostPath == "" {
		dataFlag, _ := cmd.Flags().GetString("data")
		if err := applyDataFlag(cfg, dataFlag); err != nil {
			return err
		}
	}

	pull, _ := cmd.Flags().GetBool("pull")
	cfg.Pull = pull

	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm := resolveRealm(cmd, cfg.Realm)
	meshMgr := meshMgrFrom(ctx, client, realm)
	meshMgr.Pull = pull
	if err := meshMgr.EnsureMesh(ctx); err != nil {
		if meshMgr.Created() {
			log := sindlog.From(ctx)
			log.ErrorContext(ctx, "cleaning up partial resources, please wait")
			cleanupCtx := context.WithoutCancel(ctx)
			if cleanupErr := meshMgr.CleanupMesh(cleanupCtx); cleanupErr != nil {
				log.ErrorContext(ctx, "mesh cleanup failed", "error", cleanupErr)
			}
		}
		return fmt.Errorf("setting up mesh: %w", err)
	}

	_, err = cluster.Create(ctx, client, meshMgr, cfg, defaultReadinessInterval)
	if err != nil {
		return err
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", exportErr)
		}
	}

	return nil
}

// applyDataFlag sets the data storage config from the --data CLI flag.
// "volume" means use a Docker-managed volume; any other value is treated
// as a host directory path and resolved to an absolute path.
func applyDataFlag(cfg *config.Cluster, value string) error {
	if value == "volume" {
		return nil
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return fmt.Errorf("resolving data path %q: %w", value, err)
	}
	cfg.Storage.DataStorage.Type = "hostPath"
	cfg.Storage.DataStorage.HostPath = abs
	return nil
}

func loadConfig(path string) (*config.Cluster, error) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		return config.Parse(data)
	}

	if stdinHasData() {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading config from stdin: %w", err)
		}
		return config.Parse(data)
	}

	return config.Parse([]byte("kind: Cluster\n"))
}

// stdinHasData reports whether stdin is a pipe or file (not a terminal).
func stdinHasData() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}
