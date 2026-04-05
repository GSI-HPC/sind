// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// NodeHealth holds the health status of a single node.
type NodeHealth struct {
	Container docker.ContainerState // container state from Docker (e.g. "running", "exited")
	IP        string                // container IP address
	Munge     bool                  // munge service healthy
	SSHD      bool                  // sshd accepting connections
	Services  map[string]bool       // role-specific services (e.g., "slurmctld", "slurmd")
}

// GetNodeHealth checks the health of a single node container.
// If the container is not running, remaining checks are skipped and
// default to false. The role determines which Slurm services are checked.
// clusterName is used to select the cluster network IP.
func GetNodeHealth(ctx context.Context, client *docker.Client, containerName string, role config.Role, realm, clusterName string) (*NodeHealth, error) {
	name := docker.ContainerName(containerName)

	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	health := &NodeHealth{
		Container: info.Status,
		IP:        info.IPs[NetworkName(realm, clusterName)],
		Services:  make(map[string]bool),
	}

	// If container is not running, skip all service checks.
	if info.Status != docker.StateRunning {
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

// NetworkHealth holds the health and IPAM details of cluster networking.
type NetworkHealth struct {
	Mesh           bool   // sind-mesh network exists
	MeshName       string // mesh network name (e.g. "sind-mesh")
	MeshDriver     string // mesh network driver (e.g. "bridge")
	MeshSubnet     string // mesh network subnet
	MeshGateway    string // mesh network gateway
	DNS            bool   // sind-dns container exists
	DNSName        string // DNS container name (e.g. "sind-dns")
	Cluster        bool   // cluster network exists
	ClusterName    string // cluster network name (e.g. "sind-dev-net")
	ClusterDriver  string // cluster network driver (e.g. "bridge")
	ClusterSubnet  string // cluster network subnet
	ClusterGateway string // cluster network gateway
}

// GetNetworkHealth checks the health of mesh, DNS, and cluster networking.
func GetNetworkHealth(ctx context.Context, client *docker.Client, realm, clusterName string) (*NetworkHealth, error) {
	// nil Docker client: only realm-derived names are needed here.
	meshMgr := mesh.NewManager(nil, realm)
	clusterNet := NetworkName(realm, clusterName)

	health := &NetworkHealth{
		MeshName:    string(meshMgr.NetworkName()),
		DNSName:     string(meshMgr.DNSContainerName()),
		ClusterName: string(clusterNet),
	}

	meshExists, err := client.NetworkExists(ctx, meshMgr.NetworkName())
	if err != nil {
		return nil, fmt.Errorf("checking mesh network: %w", err)
	}
	health.Mesh = meshExists
	if meshExists {
		if info, err := client.InspectNetwork(ctx, meshMgr.NetworkName()); err == nil {
			health.MeshDriver = info.Driver
			health.MeshSubnet = info.Subnet
			health.MeshGateway = info.Gateway
		}
	}

	dnsExists, err := client.ContainerExists(ctx, meshMgr.DNSContainerName())
	if err != nil {
		return nil, fmt.Errorf("checking DNS container: %w", err)
	}
	health.DNS = dnsExists

	clusterExists, err := client.NetworkExists(ctx, clusterNet)
	if err != nil {
		return nil, fmt.Errorf("checking cluster network: %w", err)
	}
	health.Cluster = clusterExists
	if clusterExists {
		if info, err := client.InspectNetwork(ctx, clusterNet); err == nil {
			health.ClusterDriver = info.Driver
			health.ClusterSubnet = info.Subnet
			health.ClusterGateway = info.Gateway
		}
	}

	return health, nil
}

// MountPoint describes a volume or bind mount on cluster containers.
type MountPoint struct {
	Path   string // mount path inside the container (e.g. "/etc/slurm")
	Source string // volume name or host path
	Type   string // "volume" or "hostPath"
	OK     bool   // true if the Docker volume exists (always true for hostPath)
}

// GetMountPoints returns the mount points for a cluster, checking volume
// existence for Docker volumes. The data mount source is determined from
// the sind.data.hostpath label on cluster containers: when present it is
// a host-path bind mount, otherwise it is a Docker volume.
func GetMountPoints(ctx context.Context, client *docker.Client, realm, clusterName string, containers []docker.ContainerListEntry) ([]MountPoint, error) {
	// Determine data mount source from container labels.
	dataHostPath := ""
	for _, c := range containers {
		if hp := c.Labels[LabelDataHostPath]; hp != "" {
			dataHostPath = hp
			break
		}
	}

	// Config and munge are always Docker volumes.
	mounts := []MountPoint{
		{Path: slurm.ConfDir, Source: string(VolumeName(realm, clusterName, VolumeConfig)), Type: "volume"},
		{Path: slurm.MungeDir, Source: string(VolumeName(realm, clusterName, VolumeMunge)), Type: "volume"},
	}

	if dataHostPath != "" {
		mounts = append(mounts, MountPoint{Path: DefaultDataMountPath, Source: dataHostPath, Type: "hostPath", OK: true})
	} else {
		mounts = append(mounts, MountPoint{Path: DefaultDataMountPath, Source: string(VolumeName(realm, clusterName, VolumeData)), Type: "volume"})
	}

	// Check existence of Docker volumes.
	for i := range mounts {
		if mounts[i].Type != "volume" {
			continue
		}
		exists, err := client.VolumeExists(ctx, docker.VolumeName(mounts[i].Source))
		if err != nil {
			return nil, fmt.Errorf("checking volume %s: %w", mounts[i].Source, err)
		}
		mounts[i].OK = exists
	}

	return mounts, nil
}

// NodeStatus combines node identity with health information.
type NodeStatus struct {
	Name   string      // DNS-style name: "controller.dev"
	Role   config.Role // "controller", "submitter", "worker"
	Health *NodeHealth
}

// Status holds the full status of a sind cluster.
type Status struct {
	Name    string
	State   State
	Nodes   []*NodeStatus
	Network *NetworkHealth
	Mounts  []MountPoint
}

// GetStatus returns the full status of a cluster, aggregating node, network,
// and volume health information.
func GetStatus(ctx context.Context, client *docker.Client, realm, clusterName string) (*Status, error) {
	// List all containers in this cluster.
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	prefix := ContainerPrefix(realm, clusterName)
	var nodes []*NodeStatus
	var states []docker.ContainerState
	for _, c := range containers {
		shortName := strings.TrimPrefix(string(c.Name), prefix)
		role := config.Role(c.Labels[LabelRole])

		health, err := GetNodeHealth(ctx, client, string(c.Name), role, realm, clusterName)
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

	network, err := GetNetworkHealth(ctx, client, realm, clusterName)
	if err != nil {
		return nil, err
	}

	mounts, err := GetMountPoints(ctx, client, realm, clusterName, containers)
	if err != nil {
		return nil, err
	}

	return &Status{
		Name:    clusterName,
		State:   aggregateState(states),
		Nodes:   nodes,
		Network: network,
		Mounts:  mounts,
	}, nil
}

// nodeStatusOrder returns a sort key for NodeStatus (controller, submitter, worker).
func nodeStatusOrder(n *NodeStatus) string {
	return roleSortKey(n.Role, n.Name)
}

// roleServices returns the Slurm service names for the given role.
func roleServices(role config.Role) []string {
	switch role {
	case config.RoleController:
		return []string{"slurmctld"}
	case config.RoleWorker:
		return []string{"slurmd"}
	default:
		return nil
	}
}
