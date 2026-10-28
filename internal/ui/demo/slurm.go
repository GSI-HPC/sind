// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// The size of every worker of the demo, as slurmd reports it.
const (
	workerCPUs     = 2
	workerMemoryMB = 1024
)

// slurmOf returns what Slurm says about a cluster, as sinfo on its
// controller and its config files would: an error when the controller
// does not run, and the files alone when its slurmctld is down.
func slurmOf(c *clusterState) *store.Slurm {
	ctl := c.find(slurm.ControllerHost)
	switch {
	case ctl == nil:
		return &store.Slurm{Err: "Error response from daemon: No such container: " + slurm.ControllerHost}
	case ctl.state != cluster.StateRunning:
		return &store.Slurm{Err: "Error response from daemon: container " + ctl.short + " is not running"}
	}
	info := slurmInfoOf(c)
	if !ctl.up[probe.ServiceSlurmctld] {
		return &store.Slurm{Info: &cluster.SlurmInfo{Files: info.Files}, Err: "slurm_load_node: Unable to contact slurm controller (connect failure)"}
	}
	return &store.Slurm{Info: info}
}

// slurmInfoOf returns what sinfo and the config files say about a cluster
// whose slurmctld answers.
func slurmInfoOf(c *clusterState) *cluster.SlurmInfo {
	info := &cluster.SlurmInfo{}
	var workers []slurm.NodeEntry
	var names []string
	opts := slurm.ConfOptions{BackupController: c.find(cluster.ControllerBackupShortName) != nil, Metrics: c.monitoring}
	for _, n := range c.nodes {
		switch n.role {
		case config.RoleDB:
			opts.Accounting = true
		case config.RoleAPI:
			opts.JWT = true
		case config.RoleWorker:
			workers = append(workers, slurm.NodeEntry{Name: n.short, CPUs: workerCPUs, MemoryMB: workerMemoryMB})
			names = append(names, n.short)
			info.Nodes = append(info.Nodes, slurmNode(n))
		}
	}
	if len(names) > 0 {
		info.Partitions = []slurm.PartitionInfo{{Name: "all", Default: true, State: "up", TimeLimit: "infinite", Nodes: len(names), NodeList: hostList(names)}}
	}
	opts.DefMemPerCPU = slurm.DefMemPerCPU(workers)
	info.Files = []cluster.SlurmFile{
		{Path: slurm.SlurmConfPath, Content: slurm.GenerateSlurmConf(c.name, config.Section{}, opts)},
		{Path: slurm.NodesConfPath, Content: slurm.GenerateNodesConf(workers, config.Section{})},
	}
	return info
}

// slurmNode returns a worker as sinfo reports it: down when slurmd does not
// answer, draining when the demo drained it while its jobs hold CPUs,
// drained when they do not, else idle, mixed or allocated by the CPUs its
// jobs hold.
func slurmNode(n *nodeState) slurm.NodeInfo {
	sn := slurm.NodeInfo{Name: n.short, State: "idle", CPUs: workerCPUs, MemoryMB: workerMemoryMB, Partitions: []string{"all"}}
	switch {
	case n.state != cluster.StateRunning || !n.up[probe.ServiceSlurmd]:
		sn.State, sn.Reason = "down*", "Not responding"
	case n.drain != "" && n.busy > 0:
		sn.State, sn.Reason, sn.AllocCPUs = "draining", n.drain, min(n.busy, workerCPUs)
	case n.drain != "":
		sn.State, sn.Reason = "drained", n.drain
	case n.busy >= workerCPUs:
		sn.State, sn.AllocCPUs = "allocated", workerCPUs
	case n.busy > 0:
		sn.State, sn.AllocCPUs = "mixed", n.busy
	}
	return sn
}

// hostList writes node names as a Slurm host list: worker-0 to worker-3
// as worker-[0-3] when they are one run, else separated by commas.
func hostList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, ",")
	}
	first, err := strconv.Atoi(strings.TrimPrefix(names[0], "worker-"))
	if err != nil {
		return strings.Join(names, ",")
	}
	for i, n := range names {
		if n != fmt.Sprintf("worker-%d", first+i) {
			return strings.Join(names, ",")
		}
	}
	return fmt.Sprintf("worker-[%d-%d]", first, first+len(names)-1)
}
