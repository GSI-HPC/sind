// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/internal/slurm"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// SlurmNodeState is what slurmctld says about a managed worker, as sinfo
// reports it.
type SlurmNodeState struct {
	// State is sinfo's long state, such as idle, allocated, drained or
	// down; a "*" at its end means that the node does not answer.
	State string `json:"state"`
	// Reason is why the node is down or drained, empty for none.
	Reason string `json:"reason,omitempty"`
	// Registered tells whether slurmctld has the registration of the
	// node's slurmd and gets answers from it (slurm.NodeStateRegistered).
	Registered bool `json:"registered"`
}

// newSlurmNodeState returns the SlurmNodeState of a node sinfo listed.
func newSlurmNodeState(n slurm.NodeInfo) *SlurmNodeState {
	return &SlurmNodeState{State: n.State, Reason: n.Reason, Registered: slurm.NodeStateRegistered(n.State)}
}

// AddSlurmStates sets the Slurm state of the managed workers of status
// (NodeHealth.Slurm), from one sinfo on the controller in control: the
// primary controller while it runs, else the backup (see findController).
// GetStatus leaves it out, so that a slurmctld that is slow to answer does
// not hold up the status. A cluster without a running controller, or a
// sinfo that fails or does not answer within slurmInfoTimeout, leaves the
// states unset, with a debug log line that says why.
func AddSlurmStates(ctx context.Context, client *docker.Client, status *Status) {
	var workers []string
	var controller docker.ContainerName
	prefix := ContainerPrefix(status.Realm, status.Name)
	for _, n := range status.Nodes {
		shortName, _, _ := strings.Cut(n.Name, ".")
		switch {
		case n.Role == config.RoleWorker && n.Managed:
			workers = append(workers, shortName)
		case n.Role == config.RoleController && n.Health.State == StateRunning &&
			(controller == "" || shortName == string(config.RoleController)):
			controller = docker.ContainerName(prefix + shortName)
		}
	}
	if len(workers) == 0 || controller == "" {
		return
	}
	states, err := slurmNodeStates(ctx, client, controller, workers)
	if err != nil {
		sindlog.From(ctx).DebugContext(ctx, "reading the Slurm state of the workers", "cluster", status.Name, "err", err)
		return
	}
	for _, n := range status.Nodes {
		shortName, _, _ := strings.Cut(n.Name, ".")
		if n.Role == config.RoleWorker && n.Managed {
			n.Health.Slurm = states[shortName]
		}
	}
}

// nodeSlurmState returns the Slurm state of one managed worker, from a
// sinfo on the controller in control (see findController); nil when the
// cluster has no running controller, or the sinfo fails, with a debug log
// line that says why.
func nodeSlurmState(ctx context.Context, client *docker.Client, realm, clusterName, shortName string) *SlurmNodeState {
	log := sindlog.From(ctx)
	containers, err := clusterContainers(ctx, client, realm, clusterName)
	if err != nil {
		log.DebugContext(ctx, "reading the Slurm state of the node", "node", shortName, "err", err)
		return nil
	}
	controller, ok := findController(containers, realm, clusterName)
	if !ok || controller.State != docker.StateRunning {
		return nil
	}
	states, err := slurmNodeStates(ctx, client, controller.Name, []string{shortName})
	if err != nil {
		log.DebugContext(ctx, "reading the Slurm state of the node", "node", shortName, "err", err)
		return nil
	}
	return states[shortName]
}

// slurmNodeStates runs sinfo for nodes, by their Slurm node names, on
// controller, within slurmInfoTimeout, and returns their states by name. A
// node sinfo does not list has none.
func slurmNodeStates(ctx context.Context, client *docker.Client, controller docker.ContainerName, nodes []string) (map[string]*SlurmNodeState, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, slurmInfoTimeout, errSlurmInfoTimeout)
	defer cancel()
	out, err := client.Exec(ctx, controller, slurm.SinfoNodesCommandFor(nodes)...)
	if err != nil {
		if errors.Is(context.Cause(ctx), errSlurmInfoTimeout) {
			return nil, fmt.Errorf("sinfo on the controller did not answer within %s", slurmInfoTimeout)
		}
		return nil, fmt.Errorf("sinfo on the controller: %w", err)
	}
	infos, err := slurm.ParseSinfoNodes(out)
	if err != nil {
		return nil, err
	}
	states := make(map[string]*SlurmNodeState, len(infos))
	for _, n := range infos {
		states[n.Name] = newSlurmNodeState(n)
	}
	return states, nil
}
