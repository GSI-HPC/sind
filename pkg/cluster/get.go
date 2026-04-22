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
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// Summary holds summary information about a sind cluster.
type Summary struct {
	Name         string `json:"name"`
	SlurmVersion string `json:"slurm_version"`
	State        State  `json:"status"`
	NodeCount    int    `json:"nodes"`
	Submitters   int    `json:"submitters"`
	Controllers  int    `json:"controllers"`
	Workers      int    `json:"workers"`
}

// GetClusters lists all sind clusters by querying Docker for containers
// with the sind.cluster label. Containers are grouped by cluster name.
func GetClusters(ctx context.Context, client *docker.Client, realm string) ([]*Summary, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelRealm+"="+realm)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	if len(containers) == 0 {
		return []*Summary{}, nil
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
	Container string      `json:"container"` // Docker container name
	Cluster   string      `json:"cluster"`   // cluster name
	Role      config.Role `json:"role"`
	FQDN      string      `json:"fqdn"` // DNS name
	IP        string      `json:"ip"`   // container IP on cluster network
	State     State       `json:"status"`
}

// NodeDetail holds the full identity and health information for a single node
// as reported by 'sind get node'. It extends NodeSummary with a per-service
// health map. All readiness-checked services (munge, sshd, and the role's
// Slurm services) are reported under Services.
type NodeDetail struct {
	Container string                `json:"container"`
	Cluster   string                `json:"cluster"`
	Role      config.Role           `json:"role"`
	FQDN      string                `json:"fqdn"`
	IP        string                `json:"ip"`
	Status    docker.ContainerState `json:"status"`
	Services  ServiceHealth         `json:"services"`
}

// GetAllNodes lists all nodes across all clusters in the realm.
func GetAllNodes(ctx context.Context, client *docker.Client, realm string) ([]*NodeSummary, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelRealm+"="+realm)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	return buildNodeSummaries(ctx, client, realm, containers)
}

// GetNodes lists all nodes in the named cluster.
func GetNodes(ctx context.Context, client *docker.Client, realm, clusterName string) ([]*NodeSummary, error) {
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	return buildNodeSummaries(ctx, client, realm, containers)
}

// buildNodeSummaries converts container list entries into node summaries.
func buildNodeSummaries(ctx context.Context, client *docker.Client, realm string, containers []docker.ContainerListEntry) ([]*NodeSummary, error) {
	if len(containers) == 0 {
		return []*NodeSummary{}, nil
	}

	// Filter containers that belong to a cluster and collect names for a
	// single batched docker inspect call.
	filtered := make([]docker.ContainerListEntry, 0, len(containers))
	names := make([]docker.ContainerName, 0, len(containers))
	for _, c := range containers {
		if c.Labels[LabelCluster] == "" {
			continue
		}
		filtered = append(filtered, c)
		names = append(names, c.Name)
	}
	if len(filtered) == 0 {
		return []*NodeSummary{}, nil
	}

	// IPs are best-effort: GetNodes/GetAllNodes drives user-facing listings
	// and shell completion, so an inspect failure should degrade to empty
	// IPs rather than fail the whole call. Log the cause so it is still
	// visible under -v.
	ipByName := make(map[docker.ContainerName]map[docker.NetworkName]string, len(names))
	if infos, err := client.InspectContainers(ctx, names...); err != nil {
		sindlog.From(ctx).WarnContext(ctx, "inspecting cluster containers for IPs", "err", err)
	} else {
		for _, info := range infos {
			ipByName[info.Name] = info.IPs
		}
	}

	result := make([]*NodeSummary, 0, len(filtered))
	for _, c := range filtered {
		clusterName := c.Labels[LabelCluster]
		prefix := ContainerPrefix(realm, clusterName)
		shortName := strings.TrimPrefix(string(c.Name), prefix)

		result = append(result, &NodeSummary{
			Container: string(c.Name),
			Cluster:   clusterName,
			Role:      config.Role(c.Labels[LabelRole]),
			FQDN:      DNSName(shortName, clusterName, realm),
			IP:        ipByName[c.Name][NetworkName(realm, clusterName)],
			State:     containerStateToState(c.State),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Cluster != b.Cluster {
			return naturalSortKey(a.Cluster) < naturalSortKey(b.Cluster)
		}
		if ra, rb := rolePrefix(a.Role), rolePrefix(b.Role); ra != rb {
			return ra < rb
		}
		return naturalSortKey(a.Container) < naturalSortKey(b.Container)
	})
	return result, nil
}

// rolePrefix returns a single-character sort prefix that orders nodes by role
// (controller < submitter < worker < other).
func rolePrefix(role config.Role) string {
	switch role {
	case config.RoleController:
		return "0"
	case config.RoleSubmitter:
		return "1"
	case config.RoleWorker:
		return "2"
	default:
		return "9"
	}
}

// naturalSortKey returns a comparison key where runs of ASCII digits are
// zero-padded to a fixed width so that lexical comparison yields natural
// order (e.g. "worker-2" < "worker-10").
func naturalSortKey(s string) string {
	const padWidth = 20
	var b strings.Builder
	b.Grow(len(s) + padWidth)
	i := 0
	for i < len(s) {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			run := s[i:j]
			for k := len(run); k < padWidth; k++ {
				b.WriteByte('0')
			}
			b.WriteString(run)
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// NetworkSummary holds summary information about a sind network.
type NetworkSummary struct {
	Name    string `json:"name"`
	Driver  string `json:"driver"`
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
}

// GetNetworks lists all sind-related Docker networks with IPAM details.
// This includes per-cluster networks (sind-<cluster>-net) and the mesh network (sind-mesh).
func GetNetworks(ctx context.Context, client *docker.Client, realm string) ([]*NetworkSummary, error) {
	entries, err := client.ListNetworks(ctx, "label="+LabelRealm+"="+realm)
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	if len(entries) == 0 {
		return []*NetworkSummary{}, nil
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
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

// GetVolumes lists all sind-related Docker volumes.
func GetVolumes(ctx context.Context, client *docker.Client, realm string) ([]*VolumeSummary, error) {
	entries, err := client.ListVolumes(ctx, "label="+LabelRealm+"="+realm)
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}
	if len(entries) == 0 {
		return []*VolumeSummary{}, nil
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

// RealmSummary holds summary information about a sind realm.
type RealmSummary struct {
	Name     string `json:"name"`
	Clusters int    `json:"clusters"`
}

// GetRealms lists all sind realms by querying Docker for containers with the
// sind.realm label. Containers are grouped by realm, and clusters are counted
// per realm.
func GetRealms(ctx context.Context, client *docker.Client) ([]*RealmSummary, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelRealm)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	if len(containers) == 0 {
		return []*RealmSummary{}, nil
	}

	// Group by realm, tracking unique cluster names per realm.
	type realmData struct {
		clusters map[string]struct{}
	}
	byRealm := make(map[string]*realmData)
	for _, c := range containers {
		realm := c.Labels[LabelRealm]
		if realm == "" {
			continue
		}
		rd, ok := byRealm[realm]
		if !ok {
			rd = &realmData{clusters: make(map[string]struct{})}
			byRealm[realm] = rd
		}
		if cluster := c.Labels[LabelCluster]; cluster != "" {
			rd.clusters[cluster] = struct{}{}
		}
	}

	result := make([]*RealmSummary, 0, len(byRealm))
	for name, rd := range byRealm {
		result = append(result, &RealmSummary{
			Name:     name,
			Clusters: len(rd.clusters),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
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
