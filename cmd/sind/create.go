// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
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
		return 0, cobratree.Usagef("--wait must not be negative, got %s", wait)
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
		Use:   "cluster [NAME]",
		Short: "Create a Slurm cluster",
		Long:  "Create a Slurm cluster.\n\nNAME overrides the name in the configuration, which defaults to \"" + config.DefaultClusterName + "\".",
		Args:  optionalCluster,
		// The name is new: the existing ones would conflict.
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: withProgressOn(createClusterTarget(&configFile), func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			}
			return runCreateCluster(cmd, name, configFile)
		}),
	}

	cmd.Flags().StringVar(&configFile, "config", "", `path to cluster configuration file, or "-" for stdin`)
	cmd.Flags().String("data", ".", `host directory to mount read-write as /data on every node, or "volume" for a Docker volume`)
	cmd.Flags().Bool("pull", false, "pull images before creating containers")
	cmd.Flags().Bool("monitoring", false, "collect the cluster's metrics and logs in the realm's monitoring stack (adds to the config's monitoring)")
	addWaitFlag(cmd)

	return cmd
}

// createClusterTarget returns the target of create cluster, whose
// configuration is read from *configFile: the cluster that NAME names, else
// the one the configuration file names, else the default one when the
// configuration is the default one. A configuration from stdin is read only
// once, as the command runs, which names the cluster then.
func createClusterTarget(configFile *string) commandTarget {
	return func(cmd *cobra.Command, args []string) string {
		switch {
		case len(args) > 0:
			return args[0]
		case *configFile == configStdin:
			return ""
		case *configFile == "":
			if stdinHasData(cmd.InOrStdin()) {
				return ""
			}
			return config.DefaultClusterName
		}
		data, err := os.ReadFile(*configFile)
		if err != nil {
			return "" // the command fails on it
		}
		cfg, err := config.Parse(data)
		if err != nil {
			return ""
		}
		return cfg.Name
	}
}

func runCreateCluster(cmd *cobra.Command, name, configFile string) error {
	wait, err := waitFlag(cmd)
	if err != nil {
		return err
	}
	if err := checkNotEmpty(cmd, "config"); err != nil {
		return err
	}

	// --config - may read what the user types at the terminal; the
	// progress display is off it meanwhile.
	resume := func() {}
	if configFile == configStdin {
		resume = progress.Suspend(cmd.Context())
	}
	cfg, source, err := loadConfig(cmd.InOrStdin(), cmd.ErrOrStderr(), configFile)
	resume()
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
			cmd.PrintErrln("Warning:", termtext.EscapeLines(w))
		}
	}

	pull, _ := cmd.Flags().GetBool("pull")
	cfg.Pull = pull
	cfg.Wait = wait
	// --monitoring=false leaves a config's monitoring: true alone.
	if monitoring, _ := cmd.Flags().GetBool("monitoring"); monitoring {
		cfg.Monitoring = true
	}

	cfg.ApplyDefaults()
	nameTarget(cmd.Context(), cfg.Name)
	if err := cfg.Validate(); err != nil {
		if source != "" {
			return fmt.Errorf("config %s: %w", source, err)
		}
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
		err = fmt.Errorf("setting up mesh: %w", err)
		if meshMgr.Created() {
			// The cleanup is the progress step "rollback", and its
			// failure is joined to the error, as cluster.Create's are.
			sindlog.From(ctx).WarnContext(ctx, "cleaning up partial resources, please wait")
			cleanupCtx, rollback := progress.Start(context.WithoutCancel(ctx), progress.KindStep, "rollback")
			if cleanupErr := meshMgr.CleanupMesh(cleanupCtx); cleanupErr != nil {
				rollback.End(rollbackFailed{cleanupErr})
				err = errors.Join(err, fmt.Errorf("rolling back: removing the mesh: %w", cleanupErr))
			} else {
				rollback.End(nil)
			}
		}
		return err
	}

	_, err = cluster.Create(ctx, client, meshMgr, cfg, cluster.DefaultReadinessInterval)
	if err != nil {
		return err
	}

	updateSSHExport(cmd, client, meshMgr)
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

// checkNotEmpty rejects each of the flags names that is given but empty,
// as a usage error: an unset variable in --config "$CFG" would otherwise
// quietly stand for the flag's default.
func checkNotEmpty(cmd *cobra.Command, names ...string) error {
	for _, name := range names {
		if value, _ := cmd.Flags().GetString(name); value == "" && cmd.Flags().Changed(name) {
			return cobratree.Usagef("--%s must not be empty: leave it out for the default", name)
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
	// filepath.Abs("") is the working directory: an unset variable in
	// --data "$DATA" would mount it read-write into every node.
	if value == "" {
		return cobratree.Usagef(`--data must not be empty: give a host directory or "volume"`)
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

// stdinSource names stdin as the source of the configuration.
const stdinSource = "<stdin>"

// loadConfig reads the cluster configuration from path, from stdin when
// path is "-", or else returns the default configuration, and names where
// it came from: path, stdinSource, or "" for the default. An error names
// that source too. stdin is the command's input (cmd.InOrStdin()), so that
// tests can set it with cmd.SetIn instead of replacing the process-wide
// os.Stdin.
//
// Without a path, a stdin that is not a terminal is still read, for one
// release: sind writes a deprecation warning to stderr first, since it
// waits for the end of the input, and takes empty input for the default
// configuration. An inherited pipe, as with ssh HOST sind create cluster,
// or a read loop's input, would otherwise hang sind or be taken for the
// configuration. With --config -, empty input is an error.
func loadConfig(stdin io.Reader, stderr io.Writer, path string) (*config.Cluster, string, error) {
	switch path {
	case "":
	case configStdin:
		data, err := io.ReadAll(stdin)
		return parseConfig(stdinSource, data, err)
	default:
		data, err := os.ReadFile(path)
		return parseConfig(path, data, err)
	}

	if stdinHasData(stdin) {
		_, _ = fmt.Fprintln(stderr, stdinDeprecation)
		data, err := io.ReadAll(stdin)
		if err != nil || len(bytes.TrimSpace(data)) > 0 {
			return parseConfig(stdinSource, data, err)
		}
	}

	cfg, err := config.Parse([]byte("kind: Cluster\n"))
	return cfg, "", err
}

// parseConfig parses the configuration data read from source, or returns
// readErr, the error reading it, naming source in the error.
func parseConfig(source string, data []byte, readErr error) (*config.Cluster, string, error) {
	err := readErr
	if err == nil {
		var cfg *config.Cluster
		if cfg, err = config.Parse(data); err == nil {
			return cfg, source, nil
		}
	}
	// The source names the file already.
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return nil, source, fmt.Errorf("config %s: %w", source, err)
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
