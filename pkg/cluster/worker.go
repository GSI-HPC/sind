// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// WorkerAddOptions holds the parameters for adding compute workers to a cluster.
type WorkerAddOptions struct {
	ClusterName string
	Count       int
	Image       string
	CPUs        int
	Memory      string
	TmpSize     string
	Unmanaged   bool
}

// WorkerAdd adds compute workers to an existing cluster.
//
// For managed workers (default), the flow is:
//  1. Validate: controller exists, sind-nodes.conf present
//  2. Create compute container(s)
//  3. Wait for readiness, inject SSH keys, collect host keys
//  4. Register DNS + known_hosts
//  5. Update sind-nodes.conf with new node definitions
//  6. Reconfigure slurmctld
//  7. Enable slurmd on new nodes
//
// For unmanaged workers (Unmanaged=true), steps 5–7 are skipped.
func WorkerAdd(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, opts WorkerAddOptions, readinessInterval time.Duration) ([]*Node, error) {
	// List cluster containers once for validation + index + image resolution.
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("listing cluster containers: %w", err)
	}

	controllerName := ContainerName(opts.ClusterName, "controller")
	var controllerImage string
	for _, c := range containers {
		if c.Name == controllerName {
			controllerImage = c.Image
			break
		}
	}
	if controllerImage == "" {
		return nil, fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}

	// Validate sind-nodes.conf for managed workers.
	if !opts.Unmanaged {
		_, err := client.ReadFile(ctx, controllerName, "/etc/slurm/sind-nodes.conf")
		if err != nil {
			return nil, fmt.Errorf("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration; use --unmanaged to add nodes without modifying Slurm config")
		}
	}

	// Compute next index from existing containers.
	startIdx := nextComputeIndexFromContainers(containers, opts.ClusterName)

	// Resolve infrastructure: DNS IP, SSH pubkey, slurm version.
	dnsIP, sshPubKey, slurmVersion, err := resolveWorkerInfra(ctx, client, controllerName)
	if err != nil {
		return nil, err
	}

	// Resolve image: use opts or fall back to controller's image.
	image := opts.Image
	if image == "" {
		image = controllerImage
	}

	// Build RunConfig entries for new nodes.
	count := opts.Count
	if count <= 0 {
		count = 1
	}
	nodeConfigs := make([]RunConfig, count)
	for i := range count {
		nodeConfigs[i] = RunConfig{
			ClusterName:  opts.ClusterName,
			ShortName:    fmt.Sprintf("compute-%d", startIdx+i),
			Role:         "compute",
			Image:        image,
			CPUs:         opts.CPUs,
			Memory:       opts.Memory,
			TmpSize:      opts.TmpSize,
			SlurmVersion: slurmVersion,
			DNSIP:        dnsIP,
			Managed:      !opts.Unmanaged,
		}
	}

	// Create and start node containers.
	if err := createAllNodes(ctx, client, nodeConfigs); err != nil {
		return nil, err
	}

	// Wait for readiness, inject SSH keys, collect host keys.
	nodeResults, err := setupNodes(ctx, client, opts.ClusterName, sshPubKey, nodeConfigs, readinessInterval)
	if err != nil {
		return nil, err
	}

	// Register DNS + known_hosts and build result.
	var nodes []*Node
	for i, nc := range nodeConfigs {
		nr := nodeResults[i]
		nodeIP := nr.info.IPs[NetworkName(opts.ClusterName)]
		dnsName := DNSName(nc.ShortName, opts.ClusterName)

		if err := meshMgr.AddDNSRecord(ctx, dnsName, nodeIP); err != nil {
			return nil, fmt.Errorf("registering DNS for %s: %w", nc.ShortName, err)
		}
		if err := meshMgr.AddKnownHost(ctx, dnsName, nr.hostKey); err != nil {
			return nil, fmt.Errorf("registering host key for %s: %w", nc.ShortName, err)
		}

		nodes = append(nodes, &Node{
			Name:        nc.ShortName,
			Role:        nc.Role,
			ContainerID: nr.info.ID,
			IP:          nodeIP,
			Status:      StatusRunning,
		})
	}

	// For managed workers: update sind-nodes.conf + reconfigure slurmctld.
	if !opts.Unmanaged {
		if err := updateNodesConf(ctx, client, controllerName, nodeConfigs); err != nil {
			return nil, err
		}
		if err := enableSlurm(ctx, client, opts.ClusterName, nodeConfigs, readinessInterval); err != nil {
			return nil, err
		}
	}

	return nodes, nil
}

// resolveWorkerInfra fetches DNS IP, SSH public key, and slurm version
// needed for adding workers to an existing cluster.
func resolveWorkerInfra(ctx context.Context, client *docker.Client, controllerName docker.ContainerName) (dnsIP, sshPubKey, slurmVersion string, err error) {
	dnsInfo, err := client.InspectContainer(ctx, mesh.DNSContainerName)
	if err != nil {
		return "", "", "", fmt.Errorf("inspecting DNS container: %w", err)
	}
	dnsIP = dnsInfo.IPs[mesh.NetworkName]

	sshPubKey, err = client.ReadFile(ctx, mesh.SSHContainerName, "/root/.ssh/id_ed25519.pub")
	if err != nil {
		return "", "", "", fmt.Errorf("reading SSH public key: %w", err)
	}

	info, err := client.InspectContainer(ctx, controllerName)
	if err != nil {
		return "", "", "", fmt.Errorf("inspecting controller: %w", err)
	}
	slurmVersion = info.Labels[LabelSlurmVersion]

	return dnsIP, sshPubKey, slurmVersion, nil
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
		memMB, _ := slurm.ParseMemoryMB(nc.Memory)
		entries = append(entries, slurm.NodeEntry{
			Name:     nc.ShortName,
			CPUs:     nc.CPUs,
			MemoryMB: memMB,
		})
	}
	updated := slurm.AddNodesToConf(current, entries)

	if err := client.WriteFile(ctx, controllerName, "/etc/slurm/sind-nodes.conf", updated); err != nil {
		return fmt.Errorf("updating sind-nodes.conf: %w", err)
	}

	if _, err := client.Exec(ctx, controllerName, "scontrol", "reconfigure"); err != nil {
		return fmt.Errorf("reconfiguring slurmctld: %w", err)
	}

	return nil
}

// nextComputeIndexFromContainers computes the next compute node index from
// a pre-fetched container list.
func nextComputeIndexFromContainers(containers []docker.ContainerListEntry, clusterName string) int {
	prefix := string(ContainerName(clusterName, "compute-"))
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

// ValidateWorkerAdd checks prerequisites for adding workers to a cluster.
// For managed workers, it verifies that sind-nodes.conf exists on the
// controller (indicating sind-generated Slurm configuration is in use).
// Unmanaged workers bypass the sind-nodes.conf check.
func ValidateWorkerAdd(ctx context.Context, client *docker.Client, opts WorkerAddOptions) error {
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+opts.ClusterName)
	if err != nil {
		return fmt.Errorf("listing cluster containers: %w", err)
	}

	controllerName := ContainerName(opts.ClusterName, "controller")
	found := false
	for _, c := range containers {
		if c.Name == controllerName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("controller not found for cluster %q", opts.ClusterName)
	}

	if opts.Unmanaged {
		return nil
	}

	_, err = client.ReadFile(ctx, controllerName, "/etc/slurm/sind-nodes.conf")
	if err != nil {
		return fmt.Errorf("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration; use --unmanaged to add nodes without modifying Slurm config")
	}

	return nil
}

// NextComputeIndex determines the next compute node index by examining
// existing containers in the cluster. Returns max(existing indices) + 1,
// or 0 if no compute containers exist.
func NextComputeIndex(ctx context.Context, client *docker.Client, clusterName string) (int, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
	if err != nil {
		return 0, fmt.Errorf("listing cluster containers: %w", err)
	}
	return nextComputeIndexFromContainers(containers, clusterName), nil
}
