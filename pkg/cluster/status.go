// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
)

// NodeHealth holds the health status of a single node.
type NodeHealth struct {
	Container string          // container state: "running", "exited", etc.
	IP        string          // container IP address
	Munge     bool            // munge service healthy
	SSHD      bool            // sshd accepting connections
	Services  map[string]bool // role-specific services (e.g., "slurmctld", "slurmd")
}

// GetNodeHealth checks the health of a single node container.
// If the container is not running, remaining checks are skipped and
// default to false. The role determines which Slurm services are checked.
func GetNodeHealth(ctx context.Context, client *docker.Client, containerName string, role string) (*NodeHealth, error) {
	name := docker.ContainerName(containerName)

	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	health := &NodeHealth{
		Container: info.Status,
		IP:        firstIP(info.IPs),
		Services:  make(map[string]bool),
	}

	// If container is not running, skip all service checks.
	if info.Status != "running" {
		for _, svc := range roleServices(role) {
			health.Services[svc] = false
		}
		return health, nil
	}

	health.Munge = probe.MungeReady(ctx, client, name) == nil
	health.SSHD = probe.SSHDReady(ctx, client, name) == nil

	for _, svc := range roleServices(role) {
		var check probe.Func
		switch svc {
		case "slurmctld":
			check = probe.SlurmctldReady
		case "slurmd":
			check = probe.SlurmdReady
		}
		health.Services[svc] = check(ctx, client, name) == nil
	}

	return health, nil
}

// NetworkHealth holds the health status of cluster networking.
type NetworkHealth struct {
	Mesh    bool // sind-mesh network exists
	DNS     bool // sind-dns container exists and running
	Cluster bool // cluster network exists
}

// GetNetworkHealth checks the health of mesh, DNS, and cluster networking.
func GetNetworkHealth(ctx context.Context, client *docker.Client, clusterName string) (*NetworkHealth, error) {
	health := &NetworkHealth{}

	meshExists, err := client.NetworkExists(ctx, "sind-mesh")
	if err != nil {
		return nil, fmt.Errorf("checking mesh network: %w", err)
	}
	health.Mesh = meshExists

	dnsExists, err := client.ContainerExists(ctx, "sind-dns")
	if err != nil {
		return nil, fmt.Errorf("checking DNS container: %w", err)
	}
	health.DNS = dnsExists

	clusterNet := NetworkName(clusterName)
	clusterExists, err := client.NetworkExists(ctx, docker.NetworkName(clusterNet))
	if err != nil {
		return nil, fmt.Errorf("checking cluster network: %w", err)
	}
	health.Cluster = clusterExists

	return health, nil
}

// VolumeHealth holds the existence status of cluster volumes.
type VolumeHealth struct {
	Config bool // sind-<cluster>-config volume exists
	Munge  bool // sind-<cluster>-munge volume exists
	Data   bool // sind-<cluster>-data volume exists
}

// GetVolumeHealth checks whether the cluster's config, munge, and data volumes exist.
func GetVolumeHealth(ctx context.Context, client *docker.Client, clusterName string) (*VolumeHealth, error) {
	health := &VolumeHealth{}

	for _, vtype := range []string{"config", "munge", "data"} {
		volName := VolumeName(clusterName, vtype)
		exists, err := client.VolumeExists(ctx, docker.VolumeName(volName))
		if err != nil {
			return nil, fmt.Errorf("checking volume %s: %w", volName, err)
		}
		switch vtype {
		case "config":
			health.Config = exists
		case "munge":
			health.Munge = exists
		case "data":
			health.Data = exists
		}
	}

	return health, nil
}

// NodeStatus combines node identity with health information.
type NodeStatus struct {
	Name   string // DNS-style name: "controller.dev"
	Role   string // "controller", "submitter", "compute"
	Health *NodeHealth
}

// ClusterStatus holds the full status of a sind cluster.
type ClusterStatus struct {
	Name    string
	Status  Status
	Nodes   []*NodeStatus
	Network *NetworkHealth
	Volumes *VolumeHealth
}

// GetStatus returns the full status of a cluster, aggregating node, network,
// and volume health information.
func GetStatus(ctx context.Context, client *docker.Client, clusterName string) (*ClusterStatus, error) {
	// List all containers in this cluster.
	containers, err := client.ListContainers(ctx, "label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	prefix := "sind-" + clusterName + "-"
	var nodes []*NodeStatus
	var states []string
	for _, c := range containers {
		shortName := strings.TrimPrefix(string(c.Name), prefix)
		role := c.Labels[LabelRole]

		health, err := GetNodeHealth(ctx, client, string(c.Name), role)
		if err != nil {
			return nil, fmt.Errorf("checking node %s: %w", shortName, err)
		}

		nodes = append(nodes, &NodeStatus{
			Name:   shortName + "." + clusterName,
			Role:   role,
			Health: health,
		})
		states = append(states, c.State)
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodeStatusOrder(nodes[i]) < nodeStatusOrder(nodes[j])
	})

	network, err := GetNetworkHealth(ctx, client, clusterName)
	if err != nil {
		return nil, err
	}

	volumes, err := GetVolumeHealth(ctx, client, clusterName)
	if err != nil {
		return nil, err
	}

	return &ClusterStatus{
		Name:    clusterName,
		Status:  aggregateStatus(states),
		Nodes:   nodes,
		Network: network,
		Volumes: volumes,
	}, nil
}

// nodeStatusOrder returns a sort key for NodeStatus (controller, submitter, compute).
func nodeStatusOrder(n *NodeStatus) string {
	return roleSortKey(n.Role, n.Name)
}

// roleServices returns the Slurm service names for the given role.
func roleServices(role string) []string {
	switch role {
	case "controller":
		return []string{"slurmctld"}
	case "compute":
		return []string{"slurmd"}
	default:
		return nil
	}
}

// firstIP returns the first IP address from the network map.
// When multiple networks are present, the result is non-deterministic
// but always non-empty if any network has an IP.
func firstIP(ips map[docker.NetworkName]string) string {
	for _, ip := range ips {
		if ip != "" {
			return ip
		}
	}
	return ""
}
