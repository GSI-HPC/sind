// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/GSI-HPC/sind/internal/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

// defaultWait is the --wait default of create cluster and create worker.
const defaultWait = 5 * time.Minute

// addWaitFlag adds --wait to a create command.
func addWaitFlag(cmd *cobra.Command) {
	cmd.Flags().Duration("wait", defaultWait,
		"how long to wait for the nodes and Slurm to become ready, counted from when the node containers have started (0: no limit)")
}

// waitFlag returns the value of --wait. A negative one is a usage error.
func waitFlag(cmd *cobra.Command) (time.Duration, error) {
	wait, _ := cmd.Flags().GetDuration("wait")
	if wait < 0 {
		return 0, usagef("--wait must not be negative, got %s", wait)
	}
	return wait, nil
}

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
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			}
			return runCreateCluster(cmd, name, configFile)
		},
	}

	cmd.Flags().StringVar(&configFile, "config", "", `path to cluster configuration file, or "-" for stdin`)
	cmd.Flags().String("data", ".", `host directory to mount as /data (use "volume" for Docker volume)`)
	cmd.Flags().Bool("pull", false, "pull images before creating containers")
	addWaitFlag(cmd)

	return cmd
}

func runCreateCluster(cmd *cobra.Command, name, configFile string) error {
	wait, err := waitFlag(cmd)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(cmd.InOrStdin(), cmd.ErrOrStderr(), configFile)
	if err != nil {
		return err
	}

	if name != "" {
		cfg.Name = name
	}

	dataFlag, _ := cmd.Flags().GetString("data")
	if err := applyDataStorage(cfg, dataFlag); err != nil {
		return err
	}
	if ds := cfg.Storage.DataStorage; ds.UsesHostPath() {
		if w := cluster.DataPathWarning(ds.HostPath); w != "" {
			cmd.PrintErrln("Warning:", termtext.EscapeText(w))
		}
	}

	pull, _ := cmd.Flags().GetBool("pull")
	cfg.Pull = pull
	cfg.Wait = wait

	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	for _, w := range cfg.Warnings() {
		cmd.PrintErrln("Warning:", w)
	}

	ctx := cmd.Context()
	client := clientFrom(ctx)
	realm, err := resolveRealm(cmd, cfg.Realm)
	if err != nil {
		return err
	}
	// The cluster goes to the resolved realm; cluster.Create refuses a
	// config realm that differs from its mesh manager's.
	cfg.Realm = realm

	// Refuse a daemon that cannot start the nodes before anything is pulled.
	if err := cluster.CheckDaemon(ctx, client); err != nil {
		return err
	}

	unlock, err := acquireRealmLock(ctx, realm, "")
	if err != nil {
		return err
	}
	defer unlock()

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

	_, err = cluster.Create(ctx, client, meshMgr, cfg, cluster.DefaultReadinessInterval)
	if err != nil {
		return err
	}

	if dir, dirErr := sindStateDir(realm); dirErr == nil {
		if exportErr := syncSSHExport(ctx, client, meshMgr, afero.NewOsFs(), dir); exportErr != nil {
			cmd.PrintErrln("Warning: could not update SSH config:", termtext.EscapeText(exportErr.Error()))
		}
	}

	return nil
}

// applyDataStorage settles where the cluster's data comes from: the config's
// dataStorage or, when that sets neither type nor hostPath, the --data flag.
// A relative hostPath from the config is taken relative to the working
// directory and made absolute, because sind create worker reuses it from a
// container label, possibly run from another directory.
func applyDataStorage(cfg *config.Cluster, dataFlag string) error {
	ds := &cfg.Storage.DataStorage
	if ds.Type == "" && ds.HostPath == "" {
		return applyDataFlag(cfg, dataFlag)
	}
	if ds.HostPath == "" {
		return nil
	}
	abs, err := filepath.Abs(ds.HostPath)
	if err != nil {
		return fmt.Errorf("resolving data path %q: %w", ds.HostPath, err)
	}
	ds.HostPath = abs
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
	cfg.Storage.DataStorage.Type = config.StorageHostPath
	cfg.Storage.DataStorage.HostPath = abs
	return nil
}

// configStdin is the --config value that reads the configuration from
// stdin.
const configStdin = "-"

// stdinDeprecation is the warning printed before sind reads the
// configuration from a stdin that is not a terminal without --config -.
const stdinDeprecation = "Warning: reading the cluster configuration from stdin without --config - is deprecated; " +
	"pass --config - to read it, or redirect stdin from /dev/null for the default cluster"

// loadConfig reads the cluster configuration from path, from stdin when
// path is "-", or else returns the default configuration. stdin is the
// command's input (cmd.InOrStdin()), so that tests can set it with
// cmd.SetIn instead of replacing the process-wide os.Stdin.
//
// Without a path, a stdin that is not a terminal is still read, for one
// release: sind writes a deprecation warning to stderr first, since it
// waits for the end of the input, and takes empty input for the default
// configuration. An inherited pipe, as with ssh HOST sind create cluster,
// or a read loop's input, would otherwise hang sind or be taken for the
// configuration. With --config -, empty input is an error.
func loadConfig(stdin io.Reader, stderr io.Writer, path string) (*config.Cluster, error) {
	switch path {
	case "":
	case configStdin:
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading config from stdin: %w", err)
		}
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, errors.New("reading config from stdin: empty configuration")
		}
		return config.Parse(data)
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		return config.Parse(data)
	}

	if stdinHasData(stdin) {
		_, _ = fmt.Fprintln(stderr, stdinDeprecation)
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading config from stdin: %w", err)
		}
		if len(bytes.TrimSpace(data)) > 0 {
			return config.Parse(data)
		}
	}

	return config.Parse([]byte("kind: Cluster\n"))
}

// stdinHasData reports whether stdin is a pipe or file (not a terminal). A
// reader that is not a file, as set with cmd.SetIn, always has data.
func stdinHasData(stdin io.Reader) bool {
	f, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}
