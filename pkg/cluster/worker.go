// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"golang.org/x/sync/errgroup"
)

// WorkerAddOptions holds the parameters for adding worker nodes to a cluster.
type WorkerAddOptions struct {
	ClusterName string
	Count       int // 0 or less adds one worker
	Image       string
	CPUs        int
	Memory      string
	TmpSize     string
	Unmanaged   bool
	Pull        bool
	CapAdd      []string
	CapDrop     []string
	Devices     []string
	SecurityOpt []string
}

// Check reports the first option WorkerAdd cannot act on: a negative CPU
// count, an unknown capability or a device path that is not absolute. It
// calls docker for none of them, so that the CLI can reject them as usage
// errors before it takes the realm lock.
func (o WorkerAddOptions) Check() error {
	if o.CPUs < 0 {
		return fmt.Errorf("--cpus must not be negative, got %d", o.CPUs)
	}
	if err := config.CheckCapabilities("--cap-add", o.CapAdd); err != nil {
		return err
	}
	if err := config.CheckCapabilities("--cap-drop", o.CapDrop); err != nil {
		return err
	}
	if err := config.CheckDevices(o.Devices); err != nil {
		return err
	}
	return config.CheckSecurityOpts("--security-opt", o.SecurityOpt)
}

// --- Exported functions ---

// WorkerAdd adds worker nodes to an existing cluster.
//
// The caller holds the realm lock (state.LockRealm) until WorkerAdd
// returns.
//
// For managed workers (default), the flow is:
//  1. Validate: options, controller exists, sind-nodes.conf present
//  2. Create worker container(s)
//  3. Wait for readiness, inject SSH keys, collect host keys
//  4. Register DNS + known_hosts
//  5. Update sind-nodes.conf with new node definitions
//  6. Reconfigure slurmctld
//  7. Enable slurmd on new nodes
//
// For unmanaged workers (Unmanaged=true), steps 5–7 are skipped. Workers
// added to an unmanaged cluster are always unmanaged. A failure after the
// first container exists removes the new containers, mesh entries and
// NodeName lines again.
func WorkerAdd(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, opts WorkerAddOptions, readinessInterval time.Duration) (result []*Node, retErr error) {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	log.InfoContext(ctx, "adding workers", "cluster", opts.ClusterName, "count", opts.Count)

	// Check the options as the config's are checked, before any container
	// exists.
	if err := opts.Check(); err != nil {
		return nil, err
	}

	// List cluster containers once for validation + index + image resolution.
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	controller, ok := findController(containers, realm, opts.ClusterName)
	if !ok {
		return nil, fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}
	controllerName := controller.Name

	// sind wrote no Slurm configuration for an unmanaged cluster to add
	// workers to.
	if !opts.Unmanaged && !IsManaged(controller.Labels) {
		log.DebugContext(ctx, "cluster is unmanaged, adding unmanaged workers", "cluster", opts.ClusterName)
		opts.Unmanaged = true
	}

	// Validate sind-nodes.conf for managed workers.
	if !opts.Unmanaged {
		if _, err := readNodesConf(ctx, client, controller); err != nil {
			return nil, err
		}
	}

	// Determine next index from existing containers.
	startIdx := nextWorkerIndexFromContainers(containers, realm, opts.ClusterName)

	// Inherit the data and CVMFS mounts, the users and the identity mode
	// from existing cluster containers. A managed worker gets the users
	// only with identity local; with nssSlurm and clientIds it resolves
	// them with nss_slurm instead.
	dataHostPath := controller.Labels[LabelDataHostPath]
	dataMountPath := DataMountPath(controller.Labels)
	cvmfs := config.StorageType(controller.Labels[LabelCVMFS])
	users, err := LinuxUsersFromLabels(controller.Labels)
	if err != nil {
		return nil, err
	}
	identity := config.Identity{Mode: IdentityFromLabels(controller.Labels)}

	// Resolve infrastructure: DNS IP, SSH pubkey, slurm version.
	dnsIP, sshPubKey, slurmVersion, err := resolveWorkerInfra(ctx, client, meshMgr, controllerName)
	if err != nil {
		return nil, err
	}

	// Resolve image: use opts or fall back to controller's image.
	image := opts.Image
	if image == "" {
		image = controller.Image
	}

	// Apply defaults for unset resource limits.
	cpus := opts.CPUs
	if cpus <= 0 {
		cpus = config.DefaultCPUs
	}
	memory := opts.Memory
	if memory == "" {
		memory = config.DefaultMemory
	}
	tmpSize := opts.TmpSize
	if tmpSize == "" {
		tmpSize = config.DefaultTmpSize
	}

	// Build RunConfig entries for new nodes.
	count := opts.Count
	if count <= 0 {
		count = 1
	}
	nodeConfigs := make([]RunConfig, count)
	for i := range count {
		nodeConfigs[i] = RunConfig{
			Realm:           realm,
			ClusterName:     opts.ClusterName,
			ShortName:       fmt.Sprintf("worker-%d", startIdx+i),
			Role:            config.RoleWorker,
			Image:           image,
			CPUs:            cpus,
			Memory:          memory,
			TmpSize:         tmpSize,
			SlurmVersion:    slurmVersion,
			DNSIP:           dnsIP,
			DataHostPath:    dataHostPath,
			DataMountPath:   dataMountPath,
			Managed:         !opts.Unmanaged,
			ContainerNumber: startIdx + i + 1,
			Pull:            opts.Pull,
			CapAdd:          opts.CapAdd,
			CapDrop:         opts.CapDrop,
			Devices:         opts.Devices,
			SecurityOpt:     opts.SecurityOpt,
			CVMFS:           cvmfs,
			Users:           users,
			AddUsers:        nodeGetsUsers(identity, config.RoleWorker, !opts.Unmanaged, false),
			Identity:        identity.Mode,
			NSSSlurm:        !opts.Unmanaged && identity.UsesNSSSlurm(),
		}
	}
	logExtraPrivileges(ctx, nodeConfigs)

	// From this point on, worker containers, and their NodeName lines in
	// sind-nodes.conf, may exist. Clean them up on failure so the user does
	// not have to remove them manually before retrying: a retry picks the
	// same names.
	needsCleanup := true
	confUpdated := false
	defer func() {
		if retErr != nil && needsCleanup {
			log.ErrorContext(ctx, "cleaning up partial resources, please wait")
			cleanupCtx := context.WithoutCancel(ctx)
			if confUpdated {
				revertNodesConf(cleanupCtx, client, controllerName, nodeConfigs)
			}
			cleanupWorkers(cleanupCtx, client, meshMgr, realm, opts.ClusterName, nodeConfigs)
		}
	}()

	// Start event watcher before creating nodes.
	prefix := ContainerPrefix(realm, opts.ClusterName)
	watcher, stopWatcher := startWatcher(ctx, client, prefix, opts.ClusterName)
	defer stopWatcher()

	// Create nodes, start systemd monitors, and wait for readiness.
	nodeResults, err := setupNodes(ctx, client, meshMgr, realm, opts.ClusterName, sshPubKey, nodeConfigs, readinessInterval, watcher)
	if err != nil {
		return nil, err
	}

	// Register DNS + known_hosts and build result.
	nodes, err := registerNodes(ctx, meshMgr, opts.ClusterName, nodeConfigs, nodeResults)
	if err != nil {
		return nil, err
	}

	// For managed workers: update sind-nodes.conf + reconfigure slurmctld.
	if !opts.Unmanaged {
		confUpdated = true
		if err := updateNodesConf(ctx, client, controllerName, nodeConfigs); err != nil {
			return nil, err
		}
		if err := enableSlurm(ctx, client, realm, opts.ClusterName, nodeConfigs, readinessInterval, watcher); err != nil {
			return nil, err
		}
	}

	needsCleanup = false
	return nodes, nil
}

// ValidateWorkerAdd checks prerequisites for adding workers to a cluster.
// For managed workers, it verifies that the controller runs and that
// sind-nodes.conf exists on it (indicating sind-generated Slurm
// configuration is in use). Unmanaged workers, and every worker of an
// unmanaged cluster, bypass the sind-nodes.conf check.
func ValidateWorkerAdd(ctx context.Context, client *docker.Client, realm string, opts WorkerAddOptions) error {
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	controller, ok := findController(containers, realm, opts.ClusterName)
	if !ok {
		return fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}

	if opts.Unmanaged || !IsManaged(controller.Labels) {
		return nil
	}

	_, err = readNodesConf(ctx, client, controller)
	return err
}

// NextComputeIndex determines the next worker node index by examining
// existing containers in the cluster. Returns max(existing indices) + 1,
// or 0 if no worker containers exist.
func NextComputeIndex(ctx context.Context, client *docker.Client, realm, clusterName string) (int, error) {
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return 0, fmt.Errorf("listing containers: %w", err)
	}
	return nextWorkerIndexFromContainers(containers, realm, clusterName), nil
}

// --- Unexported helpers ---

// findController returns the controller container sind uses to edit the
// shared Slurm configuration and run scontrol: the primary controller, or
// the backup when the primary is gone or stopped and the backup runs (e.g.
// after a failover). Both mount the same config volume, and scontrol always
// reaches the controller in control. Returns false if the cluster has
// neither.
func findController(containers []docker.ContainerListEntry, realm, clusterName string) (docker.ContainerListEntry, bool) {
	primaryName := ContainerName(realm, clusterName, string(config.RoleController))
	backupName := ContainerName(realm, clusterName, ControllerBackupShortName)
	var primary, backup *docker.ContainerListEntry
	for i := range containers {
		switch containers[i].Name {
		case primaryName:
			primary = &containers[i]
		case backupName:
			backup = &containers[i]
		}
	}
	backupRuns := backup != nil && backup.State == docker.StateRunning
	switch {
	case primary != nil && (primary.State == docker.StateRunning || !backupRuns):
		return *primary, true
	case backup != nil:
		return *backup, true
	}
	return docker.ContainerListEntry{}, false
}

// clusterManaged reports whether sind manages the Slurm configuration and
// daemons of a cluster: the sind.managed label of the controller container
// findController picks. A cluster without a controller counts as managed.
func clusterManaged(containers []docker.ContainerListEntry, realm, clusterName string) bool {
	controller, ok := findController(containers, realm, clusterName)
	return !ok || IsManaged(controller.Labels)
}

var errSindNodesConfMissing = errors.New("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration; use --unmanaged to add nodes without modifying Slurm config")

// readNodesConf reads sind-nodes.conf through the controller, which has to
// run: docker exec cannot reach a stopped or paused container, and sind
// would otherwise add or remove containers without telling Slurm. A file
// that is not there, because the user replaced sind's configuration, is
// errSindNodesConfMissing; any other failure is returned as it is.
func readNodesConf(ctx context.Context, client *docker.Client, controller docker.ContainerListEntry) (string, error) {
	if controller.State != docker.StateRunning {
		return "", fmt.Errorf("controller %s is not running (%s): sind updates sind-nodes.conf and reconfigures Slurm through it; start it with sind power on or sind power unfreeze first", controller.Name, controller.State)
	}
	content, err := client.ReadFile(ctx, controller.Name, slurm.NodesConfPath)
	if err == nil {
		return content, nil
	}
	if exitErr, ok := errors.AsType[*cmdexec.ExitError](err); ok &&
		strings.Contains(exitErr.Stderr, slurm.NodesConfPath+": No such file or directory") {
		return "", errSindNodesConfMissing
	}
	return "", fmt.Errorf("reading sind-nodes.conf: %w", err)
}

// resolveWorkerInfra fetches DNS IP, SSH public key, and slurm version
// concurrently. The Slurm version is read from the controller's labels
// (unlike resolveInfra, which discovers it from the image).
//
//	┌──────────┐  ┌──────────┐  ┌──────────────┐
//	│  DNS IP  │  │ SSH key  │  │Slurm version │
//	└────┬─────┘  └────┬─────┘  └──────┬───────┘
//	     └─────────────┼───────────────┘
func resolveWorkerInfra(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, controllerName docker.ContainerName) (dnsIP, sshPubKey, slurmVersion string, err error) {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var meshErr error
		dnsIP, sshPubKey, meshErr = resolveMeshInfra(gctx, client, meshMgr)
		return meshErr
	})
	g.Go(func() error {
		info, err := client.InspectContainer(gctx, controllerName)
		if err != nil {
			return fmt.Errorf("inspecting controller: %w", err)
		}
		slurmVersion = info.Labels[LabelSlurmVersion]
		return nil
	})
	err = g.Wait()
	return
}

// updateNodesConf reads the current sind-nodes.conf from the controller,
// adds the new node definitions, writes it back, and reconfigures slurmctld.
func updateNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, nodeConfigs []RunConfig) error {
	current, err := client.ReadFile(ctx, controllerName, slurm.NodesConfPath)
	if err != nil {
		return fmt.Errorf("reading sind-nodes.conf: %w", err)
	}

	var entries []slurm.NodeEntry
	for _, nc := range nodeConfigs {
		memMB, err := slurm.ParseMemoryMB(nc.Memory)
		if err != nil {
			return fmt.Errorf("parsing memory %q for %s: %w", nc.Memory, nc.ShortName, err)
		}
		entries = append(entries, slurm.NodeEntry{
			Name:     nc.ShortName,
			CPUs:     nc.CPUs,
			MemoryMB: memMB,
		})
	}
	updated := slurm.AddNodesToConf(current, entries)

	return writeNodesConfAndReconfigure(ctx, client, controllerName, updated)
}

// revertNodesConf removes the nodes of a failed WorkerAdd from
// sind-nodes.conf again, and reconfigures slurmctld, so that neither a
// phantom node nor, after a retry, a duplicate definition stays behind.
// Errors are logged: this is best-effort cleanup.
func revertNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, nodeConfigs []RunConfig) {
	names := make([]string, len(nodeConfigs))
	for i, nc := range nodeConfigs {
		names[i] = nc.ShortName
	}
	current, err := client.ReadFile(ctx, controllerName, slurm.NodesConfPath)
	if err == nil {
		err = removeNodesConf(ctx, client, controllerName, current, names)
	}
	if err != nil {
		sindlog.From(ctx).ErrorContext(ctx, "cleanup: removing the new nodes from sind-nodes.conf", "nodes", strings.Join(names, ","), "error", err)
	}
}

// writeNodesConfAndReconfigure writes sind-nodes.conf to the controller
// and triggers slurmctld to reload.
func writeNodesConfAndReconfigure(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, content string) error {
	if err := client.WriteFile(ctx, controllerName, slurm.NodesConfPath, content); err != nil {
		return fmt.Errorf("updating sind-nodes.conf: %w", err)
	}
	if _, err := client.Exec(ctx, controllerName, "scontrol", "reconfigure"); err != nil {
		return fmt.Errorf("reconfiguring slurmctld: %w", err)
	}
	return nil
}

// nextComputeIndexFromContainers computes the next worker node index from
// a pre-fetched container list.
func nextWorkerIndexFromContainers(containers []docker.ContainerListEntry, realm, clusterName string) int {
	prefix := string(ContainerName(realm, clusterName, "worker-"))
	maxIdx := -1
	for _, c := range containers {
		name := string(c.Name)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := name[len(prefix):]
		idx, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	return maxIdx + 1
}

// cleanupWorkers removes containers and mesh registrations for the given
// worker node configs. Used to roll back a failed WorkerAdd. Errors are
// logged but not returned — this is best-effort cleanup.
func cleanupWorkers(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, realm, clusterName string, nodeConfigs []RunConfig) {
	log := sindlog.From(ctx)

	dnsNames := make([]string, len(nodeConfigs))
	for i, nc := range nodeConfigs {
		dnsNames[i] = DNSName(nc.ShortName, clusterName, meshMgr.Realm)
	}
	if err := meshMgr.RemoveDNSRecords(ctx, dnsNames); err != nil {
		log.DebugContext(ctx, "cleanup: removing DNS records", "error", err)
	}
	if err := meshMgr.RemoveKnownHosts(ctx, dnsNames); err != nil {
		log.DebugContext(ctx, "cleanup: removing known hosts", "error", err)
	}

	for _, nc := range nodeConfigs {
		containerName := ContainerName(realm, clusterName, nc.ShortName)
		logContainerDiagnostics(ctx, client, containerName)
		if err := client.RemoveContainer(ctx, containerName); err != nil {
			log.DebugContext(ctx, "cleanup: removing container", "node", nc.ShortName, "error", err)
		}
	}
}
