// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	Count       int
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

// --- Exported functions ---

// WorkerAdd adds worker nodes to an existing cluster.
//
// For managed workers (default), the flow is:
//  1. Validate: controller exists, sind-nodes.conf present
//  2. Create worker container(s)
//  3. Wait for readiness, inject SSH keys, collect host keys
//  4. Register DNS + known_hosts
//  5. Update sind-nodes.conf with new node definitions
//  6. Reconfigure slurmctld
//  7. Enable slurmd on new nodes
//
// For unmanaged workers (Unmanaged=true), steps 5–7 are skipped.
func WorkerAdd(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, opts WorkerAddOptions, readinessInterval time.Duration) (result []*Node, retErr error) {
	log := sindlog.From(ctx)
	realm := meshMgr.Realm

	log.InfoContext(ctx, "adding workers", "cluster", opts.ClusterName, "count", opts.Count)

	// List cluster containers once for validation + index + image resolution.
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	controller, ok := findController(containers, realm, opts.ClusterName)
	if !ok {
		return nil, fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}
	controllerName := controller.Name

	// Validate sind-nodes.conf for managed workers.
	if !opts.Unmanaged {
		_, err := client.ReadFile(ctx, controllerName, slurm.NodesConfPath)
		if err != nil {
			return nil, errSindNodesConfMissing
		}
	}

	// Determine next index from existing containers.
	startIdx := nextWorkerIndexFromContainers(containers, realm, opts.ClusterName)

	// Resolve infrastructure: DNS IP, SSH pubkey, slurm version.
	dnsIP, sshPubKey, slurmVersion, err := resolveWorkerInfra(ctx, client, meshMgr, controllerName)
	if err != nil {
		return nil, err
	}

	// Inherit data host path from existing cluster containers.
	dataHostPath := controller.Labels[LabelDataHostPath]

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
			Managed:         !opts.Unmanaged,
			ContainerNumber: startIdx + i + 1,
			Pull:            opts.Pull,
			CapAdd:          opts.CapAdd,
			CapDrop:         opts.CapDrop,
			Devices:         opts.Devices,
			SecurityOpt:     opts.SecurityOpt,
		}
	}

	// From this point on, worker containers may exist. Clean them up on failure
	// so the user does not have to remove them manually before retrying.
	needsCleanup := true
	defer func() {
		if retErr != nil && needsCleanup {
			log.ErrorContext(ctx, "cleaning up partial resources, please wait")
			cleanupCtx := context.WithoutCancel(ctx)
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
// For managed workers, it verifies that sind-nodes.conf exists on the
// controller (indicating sind-generated Slurm configuration is in use).
// Unmanaged workers bypass the sind-nodes.conf check.
func ValidateWorkerAdd(ctx context.Context, client *docker.Client, realm string, opts WorkerAddOptions) error {
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	controller, ok := findController(containers, realm, opts.ClusterName)
	if !ok {
		return fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}

	if opts.Unmanaged {
		return nil
	}

	_, err = client.ReadFile(ctx, controller.Name, slurm.NodesConfPath)
	if err != nil {
		return errSindNodesConfMissing
	}

	return nil
}

// NextComputeIndex determines the next worker node index by examining
// existing containers in the cluster. Returns max(existing indices) + 1,
// or 0 if no worker containers exist.
func NextComputeIndex(ctx context.Context, client *docker.Client, realm, clusterName string) (int, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
	if err != nil {
		return 0, fmt.Errorf("listing containers: %w", err)
	}
	return nextWorkerIndexFromContainers(containers, realm, clusterName), nil
}

// --- Unexported helpers ---

// findController returns the controller's container entry from the list.
// Returns false if no controller exists for the given cluster.
func findController(containers []docker.ContainerListEntry, realm, clusterName string) (docker.ContainerListEntry, bool) {
	controllerName := ContainerName(realm, clusterName, string(config.RoleController))
	for _, c := range containers {
		if c.Name == controllerName {
			return c, true
		}
	}
	return docker.ContainerListEntry{}, false
}

var errSindNodesConfMissing = errors.New("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration; use --unmanaged to add nodes without modifying Slurm config")

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
// appends the new node definitions, writes it back, and reconfigures slurmctld.
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
