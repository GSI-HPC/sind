// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"sort"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// ClusterSummary holds summary information about a sind cluster.
type ClusterSummary struct {
	Name         string
	SlurmVersion string
	Status       Status
	NodeCount    int
	Submitters   int
	Controllers  int
	Workers      int
}

// GetClusters lists all sind clusters by querying Docker for containers
// with the sind.cluster label. Containers are grouped by cluster name.
func GetClusters(ctx context.Context, client *docker.Client) ([]*ClusterSummary, error) {
	containers, err := client.ListContainers(ctx, "label="+LabelCluster)
	if err != nil {
		return nil, fmt.Errorf("listing sind containers: %w", err)
	}
	if len(containers) == 0 {
		return nil, nil
	}

	// Group containers by cluster name.
	type clusterData struct {
		summary *ClusterSummary
		states  []string
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
				summary: &ClusterSummary{
					Name:         name,
					SlurmVersion: c.Labels[LabelSlurmVersion],
				},
			}
			byCluster[name] = cd
		}
		cd.summary.NodeCount++
		cd.states = append(cd.states, c.State)
		switch c.Labels[LabelRole] {
		case "controller":
			cd.summary.Controllers++
		case "submitter":
			cd.summary.Submitters++
		case "compute":
			cd.summary.Workers++
		}
	}

	// Build sorted result.
	result := make([]*ClusterSummary, 0, len(byCluster))
	for _, cd := range byCluster {
		cd.summary.Status = aggregateStatus(cd.states)
		result = append(result, cd.summary)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// aggregateStatus determines the overall cluster status from node states.
// If all nodes share the same status, that status is returned. Otherwise,
// the cluster status is unknown.
func aggregateStatus(states []string) Status {
	if len(states) == 0 {
		return StatusUnknown
	}
	first := containerStateToStatus(states[0])
	for _, s := range states[1:] {
		if containerStateToStatus(s) != first {
			return StatusUnknown
		}
	}
	return first
}

// containerStateToStatus maps a docker container state string to a Status.
func containerStateToStatus(state string) Status {
	switch state {
	case "running":
		return StatusRunning
	case "paused":
		return StatusPaused
	case "exited", "dead", "created":
		return StatusStopped
	default:
		return StatusUnknown
	}
}
