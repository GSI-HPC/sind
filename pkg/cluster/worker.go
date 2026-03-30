// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
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
func WorkerAdd(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, opts WorkerAddOptions, readinessInterval time.Duration) ([]*Node, error) {
	realm := meshMgr.Realm

	// List cluster containers once for validation + index + image resolution.
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("listing cluster containers: %w", err)
	}

	controller, ok := findController(containers, realm, opts.ClusterName)
	if !ok {
		return nil, fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}
	controllerName := controller.Name

	// Validate sind-nodes.conf for managed workers.
	if !opts.Unmanaged {
		_, err := client.ReadFile(ctx, controllerName, "/etc/slurm/sind-nodes.conf")
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
			Role:            "worker",
			Image:           image,
			CPUs:            cpus,
			Memory:          memory,
			TmpSize:         tmpSize,
			SlurmVersion:    slurmVersion,
			DNSIP:           dnsIP,
			Managed:         !opts.Unmanaged,
			ContainerNumber: startIdx + i + 1,
			Pull:            opts.Pull,
		}
	}

	// Create and start node containers.
	if err := createAllNodes(ctx, client, meshMgr, nodeConfigs); err != nil {
		return nil, err
	}

	// Wait for readiness, inject SSH keys, collect host keys.
	nodeResults, err := setupNodes(ctx, client, realm, opts.ClusterName, sshPubKey, nodeConfigs, readinessInterval)
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
		if err := enableSlurm(ctx, client, realm, opts.ClusterName, nodeConfigs, readinessInterval); err != nil {
			return nil, err
		}
	}

	return nodes, nil
}

// WorkerRemove removes worker nodes from a cluster.
//
// For managed nodes (those present in sind-nodes.conf), the flow is:
//  1. Update sind-nodes.conf to remove the node definitions
//  2. Reconfigure slurmctld
//  3. Deregister DNS + known_hosts
//  4. Stop + remove containers
//
// For unmanaged nodes, only steps 3–4 are performed.
func WorkerRemove(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, clusterName string, shortNames []string) error {
	realm := meshMgr.Realm

	if len(shortNames) == 0 {
		return nil
	}

	// List cluster containers to find controller and validate targets.
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
	if err != nil {
		return fmt.Errorf("listing cluster containers: %w", err)
	}

	controller, hasController := findController(containers, realm, clusterName)
	containerMap := make(map[docker.ContainerName]docker.ContainerListEntry, len(containers))
	for _, c := range containers {
		containerMap[c.Name] = c
	}

	// Resolve which nodes to remove, checking they exist and are worker nodes.
	seen := make(map[string]bool, len(shortNames))
	var targets []docker.ContainerListEntry
	for _, name := range shortNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		cn := ContainerName(realm, clusterName, name)
		c, ok := containerMap[cn]
		if !ok {
			return fmt.Errorf("node %q not found in cluster %q", name, clusterName)
		}
		if role := c.Labels[LabelRole]; role != "worker" {
			return fmt.Errorf("node %q has role %q: only worker nodes can be removed with worker remove", name, role)
		}
		targets = append(targets, c)
	}

	// For managed nodes: check if sind-nodes.conf exists and update it.
	if hasController {
		nodesConf, err := client.ReadFile(ctx, controller.Name, "/etc/slurm/sind-nodes.conf")
		if err == nil {
			// sind-nodes.conf exists → remove managed nodes from it.
			if err := removeNodesConf(ctx, client, controller.Name, nodesConf, shortNames); err != nil {
				return err
			}
		}
		// If ReadFile fails, sind-nodes.conf doesn't exist → treat as unmanaged.
	}

	// Deregister DNS + known_hosts.
	if err := DeregisterMesh(ctx, meshMgr, clusterName, targets); err != nil {
		return err
	}

	// Stop + remove containers.
	return DeleteContainers(ctx, client, targets)
}

// ValidateWorkerAdd checks prerequisites for adding workers to a cluster.
// For managed workers, it verifies that sind-nodes.conf exists on the
// controller (indicating sind-generated Slurm configuration is in use).
// Unmanaged workers bypass the sind-nodes.conf check.
func ValidateWorkerAdd(ctx context.Context, client *docker.Client, realm string, opts WorkerAddOptions) error {
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return fmt.Errorf("listing cluster containers: %w", err)
	}

	controller, ok := findController(containers, realm, opts.ClusterName)
	if !ok {
		return fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}

	if opts.Unmanaged {
		return nil
	}

	_, err = client.ReadFile(ctx, controller.Name, "/etc/slurm/sind-nodes.conf")
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
		return 0, fmt.Errorf("listing cluster containers: %w", err)
	}
	return nextWorkerIndexFromContainers(containers, realm, clusterName), nil
}

// --- Unexported helpers ---

// findController returns the controller's container entry from the list.
// Returns false if no controller exists for the given cluster.
func findController(containers []docker.ContainerListEntry, realm, clusterName string) (docker.ContainerListEntry, bool) {
	controllerName := ContainerName(realm, clusterName, "controller")
	for _, c := range containers {
		if c.Name == controllerName {
			return c, true
		}
	}
	return docker.ContainerListEntry{}, false
}

var errSindNodesConfMissing = fmt.Errorf("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration; use --unmanaged to add nodes without modifying Slurm config")

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
	current, err := client.ReadFile(ctx, controllerName, "/etc/slurm/sind-nodes.conf")
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

// removeNodesConf removes node definitions from sind-nodes.conf and
// reconfigures slurmctld.
func removeNodesConf(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, currentConf string, shortNames []string) error {
	updated := slurm.RemoveNodesFromConf(currentConf, shortNames)
	return writeNodesConfAndReconfigure(ctx, client, controllerName, updated)
}

// writeNodesConfAndReconfigure writes sind-nodes.conf to the controller
// and triggers slurmctld to reload.
func writeNodesConfAndReconfigure(ctx context.Context, client *docker.Client, controllerName docker.ContainerName, content string) error {
	if err := client.WriteFile(ctx, controllerName, "/etc/slurm/sind-nodes.conf", content); err != nil {
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
