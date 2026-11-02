// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// What the pages show of what Slurm says (store.Slurm): the state of each
// node, the partitions and the config files.

// pgAttachSlurm gives each node of the cluster name in nodes what sinfo
// on that cluster says about it, when it lists it.
func pgAttachSlurm(nodes []pgNode, name string, sl *store.Slurm) {
	if sl == nil || sl.Info == nil {
		return
	}
	for i := range nodes {
		if nodes[i].Cluster != name {
			continue
		}
		for j, sn := range sl.Info.Nodes {
			if sn.Name == nodes[i].Short {
				nodes[i].Slurm = &sl.Info.Nodes[j]
			}
		}
	}
}

// pgSlurmClass returns the class of the pill of a Slurm node state: ok for
// idle, run for a node with jobs, warn for a drained one, bad for one that
// is down or does not answer.
func pgSlurmClass(state string) string {
	switch {
	case !slurm.NodeStateTakesJobs(state):
		if strings.HasPrefix(state, "drain") && !strings.HasSuffix(state, "*") {
			return "warn"
		}
		return "bad"
	case strings.HasPrefix(state, "idle"):
		return "ok"
	}
	return "run"
}

// SlurmClass returns the class of the pill of the node's Slurm state.
func (n pgNode) SlurmClass() string { return pgSlurmClass(n.Slurm.State) }

// SlurmCPUs says how many of the node's CPUs jobs hold: "1 of 2 in use".
func (n pgNode) SlurmCPUs() string {
	return fmt.Sprintf("%d of %d in use", n.Slurm.AllocCPUs, n.Slurm.CPUs)
}

// SlurmMemory returns the node's memory as Slurm counts it.
func (n pgNode) SlurmMemory() string { return pgMemory(n.Slurm.MemoryMB) }

// SlurmPartitions returns the node's partitions, separated by commas.
func (n pgNode) SlurmPartitions() string { return strings.Join(n.Slurm.Partitions, ", ") }

// pgMemory writes megabytes as "1 GiB" when they are whole gibibytes,
// else as "512 MB".
func pgMemory(mb int) string {
	if mb >= 1024 && mb%1024 == 0 {
		return fmt.Sprintf("%d GiB", mb/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}

// pgSlurmView is what the Slurm tab and the Summary show of what Slurm
// says about a cluster.
type pgSlurmView struct {
	// Read is set once the collector has asked Slurm, Err when Slurm did
	// not answer.
	Read       bool
	Err        string
	Partitions []slurm.PartitionInfo
	// Nodes are the cluster's nodes that sinfo lists.
	Nodes []pgNode
	Files []pgSlurmFile
}

// pgSlurmFile is a config file of the Slurm tab.
type pgSlurmFile struct {
	// ID is the id of its element, which Copy reads.
	ID      string
	Name    string
	Path    string
	Content string
}

// pgSlurmOf builds what the pages show of what Slurm says, for the nodes
// of the cluster, which pgAttachSlurm filled.
func pgSlurmOf(sl *store.Slurm, nodes []pgNode) *pgSlurmView {
	v := &pgSlurmView{}
	if sl == nil {
		return v
	}
	v.Read, v.Err = true, sl.Err
	if sl.Info == nil {
		return v
	}
	v.Partitions = sl.Info.Partitions
	for _, n := range nodes {
		if n.Slurm != nil {
			v.Nodes = append(v.Nodes, n)
		}
	}
	for _, f := range sl.Info.Files {
		name := f.Path[strings.LastIndex(f.Path, "/")+1:]
		v.Files = append(v.Files, pgSlurmFile{ID: "slurm-file-" + strings.ReplaceAll(name, ".", "-"), Name: name, Path: f.Path, Content: f.Content})
	}
	return v
}

// HasConf reports whether the controller could read slurm.conf, which the
// Summary links to.
func (v *pgSlurmView) HasConf() bool {
	return slices.ContainsFunc(v.Files, func(f pgSlurmFile) bool { return f.Path == slurm.SlurmConfPath })
}

// PartitionsText names the partitions, the default one marked:
// "all (default), debug".
func (v *pgSlurmView) PartitionsText() string {
	var parts []string
	for _, p := range v.Partitions {
		name := p.Name
		if p.Default {
			name += " (default)"
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ", ")
}
