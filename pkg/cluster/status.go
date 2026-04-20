// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"golang.org/x/sync/errgroup"
)

// statusNodeConcurrency bounds the number of concurrent per-node readiness
// probes issued by GetStatus. Each goroutine performs a small number of
// docker exec round-trips, which are I/O-bound on the daemon socket; a
// limit of 8 saturates a typical docker host without fork-storming the CLI.
const statusNodeConcurrency = 8

// ServiceHealth maps a readiness-check service to its health status.
type ServiceHealth map[probe.Service]bool

// NodeHealth holds the health status of a single node.
type NodeHealth struct {
	State    docker.ContainerState `json:"status"`   // container state from Docker (e.g. "running", "exited")
	IP       string                `json:"ip"`       // container IP address
	Services ServiceHealth         `json:"services"` // all readiness-checked services (munge, sshd, and role-specific services like slurmctld/slurmd)
}

// GetNodeHealth checks the health of a single node container.
// If the container is not running, remaining checks are skipped and
// default to false. The role determines which Slurm services are checked.
// clusterName is used to select the cluster network IP.
func GetNodeHealth(ctx context.Context, client *docker.Client, containerName string, role config.Role, realm, clusterName string) (*NodeHealth, error) {
	info, err := client.InspectContainer(ctx, docker.ContainerName(containerName))
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}
	return nodeHealthFromInfo(ctx, client, info, role, realm, clusterName), nil
}

// nodeHealthFromInfo runs readiness probes against a pre-inspected container
// and returns its health. Callers that already hold a *docker.ContainerInfo
// (e.g. from a batched InspectContainers) should use this to avoid re-issuing
// a per-node docker inspect.
func nodeHealthFromInfo(ctx context.Context, client *docker.Client, info *docker.ContainerInfo, role config.Role, realm, clusterName string) *NodeHealth {
	health := &NodeHealth{
		State:    info.Status,
		IP:       info.IPs[NetworkName(realm, clusterName)],
		Services: make(ServiceHealth),
	}

	services := append([]probe.Service{probe.ServiceMunge, probe.ServiceSSHD}, roleServices(role)...)

	// If container is not running, skip all service checks.
	if info.Status != docker.StateRunning {
		for _, svc := range services {
			health.Services[svc] = false
		}
		return health
	}

	// One fused readiness exec per node (plus scontrol ping for
	// controllers) instead of three or four serial docker execs.
	snap, err := probe.Snapshot(ctx, client, info.Name, role)
	if err != nil {
		sindlog.From(ctx).DebugContext(ctx, "node probe snapshot failed", "container", string(info.Name), "err", err)
		for _, svc := range services {
			health.Services[svc] = false
		}
		return health
	}

	for _, svc := range services {
		health.Services[svc] = snap[svc]
	}

	return health
}

// NetworkHealth holds the health and IPAM details of cluster networking.
type NetworkHealth struct {
	Mesh           bool   `json:"mesh_ok"`         // sind-mesh network exists
	MeshName       string `json:"mesh_name"`       // mesh network name (e.g. "sind-mesh")
	MeshDriver     string `json:"mesh_driver"`     // mesh network driver (e.g. "bridge")
	MeshSubnet     string `json:"mesh_subnet"`     // mesh network subnet
	MeshGateway    string `json:"mesh_gateway"`    // mesh network gateway
	DNS            bool   `json:"dns_ok"`          // sind-dns container exists
	DNSName        string `json:"dns_name"`        // DNS container name (e.g. "sind-dns")
	Cluster        bool   `json:"cluster_ok"`      // cluster network exists
	ClusterName    string `json:"cluster_name"`    // cluster network name (e.g. "sind-dev-net")
	ClusterDriver  string `json:"cluster_driver"`  // cluster network driver (e.g. "bridge")
	ClusterSubnet  string `json:"cluster_subnet"`  // cluster network subnet
	ClusterGateway string `json:"cluster_gateway"` // cluster network gateway
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

	// One inspect per resource: missing → not exists, any other error → propagate.
	if info, err := client.InspectNetwork(ctx, meshMgr.NetworkName()); err == nil {
		health.Mesh = true
		health.MeshDriver = info.Driver
		health.MeshSubnet = info.Subnet
		health.MeshGateway = info.Gateway
	} else if !docker.IsNotFound(err) {
		return nil, fmt.Errorf("checking mesh network: %w", err)
	}

	if _, err := client.InspectContainer(ctx, meshMgr.DNSContainerName()); err == nil {
		health.DNS = true
	} else if !docker.IsNotFound(err) {
		return nil, fmt.Errorf("checking DNS container: %w", err)
	}

	if info, err := client.InspectNetwork(ctx, clusterNet); err == nil {
		health.Cluster = true
		health.ClusterDriver = info.Driver
		health.ClusterSubnet = info.Subnet
		health.ClusterGateway = info.Gateway
	} else if !docker.IsNotFound(err) {
		return nil, fmt.Errorf("checking cluster network: %w", err)
	}

	return health, nil
}

// MountPoint describes a volume or bind mount on cluster containers.
type MountPoint struct {
	Path   string             `json:"path"`   // mount path inside the container (e.g. "/etc/slurm")
	Source string             `json:"source"` // volume name or host path
	Type   config.StorageType `json:"type"`   // "volume" or "hostPath"
	OK     bool               `json:"ok"`     // true if the Docker volume exists (always true for hostPath)
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
		{Path: slurm.ConfDir, Source: string(VolumeName(realm, clusterName, VolumeConfig)), Type: config.StorageVolume},
		{Path: slurm.MungeDir, Source: string(VolumeName(realm, clusterName, VolumeMunge)), Type: config.StorageVolume},
	}

	if dataHostPath != "" {
		mounts = append(mounts, MountPoint{Path: DefaultDataMountPath, Source: dataHostPath, Type: config.StorageHostPath, OK: true})
	} else {
		mounts = append(mounts, MountPoint{Path: DefaultDataMountPath, Source: string(VolumeName(realm, clusterName, VolumeData)), Type: config.StorageVolume})
	}

	// Check existence of Docker volumes.
	for i := range mounts {
		if mounts[i].Type != config.StorageVolume {
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
	Name   string      `json:"name"`   // DNS-style name: "controller.dev"
	Role   config.Role `json:"role"`   // "controller", "submitter", "worker"
	Health *NodeHealth `json:"health"` //nolint:revive // nested health is intentional
}

// Status holds the full status of a sind cluster.
type Status struct {
	Name         string         `json:"name"`
	SlurmVersion string         `json:"slurm_version"`
	State        State          `json:"status"`
	Nodes        []*NodeStatus  `json:"nodes"`
	Network      *NetworkHealth `json:"network"`
	Mounts       []MountPoint   `json:"mounts"`
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

	// Batch a single docker inspect for every container up front so that the
	// per-node probe loop below has status + IP without additional round
	// trips. One docker CLI fork instead of one per node.
	infoByName := make(map[docker.ContainerName]*docker.ContainerInfo, len(containers))
	if len(containers) > 0 {
		names := make([]docker.ContainerName, len(containers))
		for i, c := range containers {
			names[i] = c.Name
		}
		infos, err := client.InspectContainers(ctx, names...)
		if err != nil {
			return nil, fmt.Errorf("inspecting cluster containers: %w", err)
		}
		for _, info := range infos {
			infoByName[info.Name] = info
		}
	}

	prefix := ContainerPrefix(realm, clusterName)
	nodes := make([]*NodeStatus, len(containers))
	states := make([]docker.ContainerState, len(containers))

	// Probe nodes in parallel with a bounded worker pool. Each goroutine
	// writes to its own pre-allocated index so no mutex is needed; the
	// final sort restores deterministic output order.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(statusNodeConcurrency)
	for i, c := range containers {
		shortName := strings.TrimPrefix(string(c.Name), prefix)
		role := config.Role(c.Labels[LabelRole])

		info, ok := infoByName[c.Name]
		if !ok {
			return nil, fmt.Errorf("checking node %s: inspect returned no entry", shortName)
		}
		states[i] = c.State
		g.Go(func() error {
			health := nodeHealthFromInfo(gctx, client, info, role, realm, clusterName)
			nodes[i] = &NodeStatus{
				Name:   shortName + "." + clusterName,
				Role:   role,
				Health: health,
			}
			return nil
		})
	}
	// Goroutines above never return a non-nil error; Wait is purely a
	// synchronisation barrier.
	_ = g.Wait()

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

	var slurmVersion string
	if len(containers) > 0 {
		slurmVersion = containers[0].Labels[LabelSlurmVersion]
	}

	return &Status{
		Name:         clusterName,
		SlurmVersion: slurmVersion,
		State:        aggregateState(states),
		Nodes:        nodes,
		Network:      network,
		Mounts:       mounts,
	}, nil
}

// nodeStatusOrder returns a sort key for NodeStatus (controller, submitter,
// worker) with natural ordering of any numeric suffixes in the node name.
func nodeStatusOrder(n *NodeStatus) string {
	return rolePrefix(n.Role) + naturalSortKey(n.Name)
}

// roleServices returns the Slurm readiness-check services for the given role.
func roleServices(role config.Role) []probe.Service {
	if svc, ok := probe.ServiceForRole(role); ok {
		return []probe.Service{svc}
	}
	return nil
}
