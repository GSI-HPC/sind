// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// Summary holds summary information about a sind cluster.
type Summary struct {
	Name         string
	SlurmVersion string
	State        State
	NodeCount    int
	Submitters   int
	Controllers  int
	Workers      int
}

// GetClusters lists all sind clusters by querying Docker for containers
// with the sind.cluster label. Containers are grouped by cluster name.
func GetClusters(ctx context.Context, client *docker.Client, realm string) ([]*Summary, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelRealm+"="+realm)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	if len(containers) == 0 {
		return nil, nil
	}

	// Group containers by cluster name.
	type clusterData struct {
		summary *Summary
		states  []docker.ContainerState
	}
	byCluster := make(map[string]*clusterData)
	for _, c := range containers {
		name := c.Labels[LabelCluster]
		if name == "" {
			continue
		}
		cd, ok := byCluster[name]
		if !ok {
			cd = &clusterData{
				summary: &Summary{
					Name:         name,
					SlurmVersion: c.Labels[LabelSlurmVersion],
				},
			}
			byCluster[name] = cd
		}
		cd.summary.NodeCount++
		cd.states = append(cd.states, c.State)
		switch config.Role(c.Labels[LabelRole]) {
		case config.RoleController:
			cd.summary.Controllers++
		case config.RoleSubmitter:
			cd.summary.Submitters++
		case config.RoleWorker:
			cd.summary.Workers++
		}
	}

	// Build sorted result.
	result := make([]*Summary, 0, len(byCluster))
	for _, cd := range byCluster {
		cd.summary.State = aggregateState(cd.states)
		result = append(result, cd.summary)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// NodeSummary holds summary information about a node in a sind cluster.
type NodeSummary struct {
	Name  string      // short name: "controller", "worker-0"
	Role  config.Role // "controller", "submitter", "worker"
	State State
}

// GetNodes lists all nodes in the named cluster.
func GetNodes(ctx context.Context, client *docker.Client, realm, clusterName string) ([]*NodeSummary, error) {
	containers, err := client.ListContainers(ctx,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	if len(containers) == 0 {
		return nil, nil
	}

	prefix := ContainerPrefix(realm, clusterName)
	result := make([]*NodeSummary, 0, len(containers))
	for _, c := range containers {
		shortName := strings.TrimPrefix(string(c.Name), prefix)
		result = append(result, &NodeSummary{
			Name:  shortName,
			Role:  config.Role(c.Labels[LabelRole]),
			State: containerStateToState(c.State),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return nodeOrder(result[i]) < nodeOrder(result[j])
	})
	return result, nil
}

// nodeOrder returns a sort key that orders nodes by role (controller, submitter, worker)
// then by name within each role.
func nodeOrder(n *NodeSummary) string {
	return roleSortKey(n.Role, n.Name)
}

// roleSortKey returns a sort key that orders by role
// (controller, db, submitter, worker) then by name within each role.
func roleSortKey(role config.Role, name string) string {
	var prefix string
	switch role {
	case config.RoleController:
		prefix = "0"
	case config.RoleDb:
		prefix = "1"
	case config.RoleSubmitter:
		prefix = "2"
	case config.RoleWorker:
		prefix = "3"
	default:
		prefix = "9"
	}
	return prefix + name
}

// NetworkSummary holds summary information about a sind network.
type NetworkSummary struct {
	Name    string
	Driver  string
	Subnet  string
	Gateway string
}

// GetNetworks lists all sind-related Docker networks with IPAM details.
// This includes per-cluster networks (sind-<cluster>-net) and the mesh network (sind-mesh).
func GetNetworks(ctx context.Context, client *docker.Client, realm string) ([]*NetworkSummary, error) {
	entries, err := client.ListNetworks(ctx, "name="+realm+"-")
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	result := make([]*NetworkSummary, 0, len(entries))
	for _, e := range entries {
		ns := &NetworkSummary{
			Name:   string(e.Name),
			Driver: e.Driver,
		}
		info, err := client.InspectNetwork(ctx, e.Name)
		if err == nil {
			ns.Subnet = info.Subnet
			ns.Gateway = info.Gateway
		}
		result = append(result, ns)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// VolumeSummary holds summary information about a sind volume.
type VolumeSummary struct {
	Name   string
	Driver string
}

// GetVolumes lists all sind-related Docker volumes.
func GetVolumes(ctx context.Context, client *docker.Client, realm string) ([]*VolumeSummary, error) {
	entries, err := client.ListVolumes(ctx, "name="+realm+"-")
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	result := make([]*VolumeSummary, 0, len(entries))
	for _, e := range entries {
		result = append(result, &VolumeSummary{
			Name:   string(e.Name),
			Driver: e.Driver,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// GetMungeKey reads the munge key from a cluster's node container.
// Any container in the cluster can be used since all mount the same munge volume.
func GetMungeKey(ctx context.Context, client *docker.Client, realm, clusterName string) ([]byte, error) {
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	if len(containers) == 0 {
		return nil, fmt.Errorf("no containers found in cluster %q", clusterName)
	}
	key, err := client.CopyFromContainer(ctx, containers[0].Name, slurm.MungeKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading munge key: %w", err)
	}
	return key, nil
}

// aggregateState determines the overall cluster status from node states.
// If all nodes share the same status, that status is returned.
// Mixed states return StateMixed; no nodes returns StateEmpty.
func aggregateState(states []docker.ContainerState) State {
	if len(states) == 0 {
		return StateEmpty
	}
	first := containerStateToState(states[0])
	for _, s := range states[1:] {
		if containerStateToState(s) != first {
			return StateMixed
		}
	}
	return first
}

// containerStateToState maps a docker container state to a cluster State.
func containerStateToState(state docker.ContainerState) State {
	switch state {
	case docker.StateRunning:
		return StateRunning
	case docker.StatePaused:
		return StatePaused
	case docker.StateExited, docker.StateDead, docker.StateCreated:
		return StateStopped
	default:
		return StateUnknown
	}
}
