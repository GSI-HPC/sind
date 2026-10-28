// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// pgFiles is where the files of the jobs of a cluster are, as the Connect
// tab of a cluster and the page of a new one show it: the host side of its
// data mount and the path in the nodes, in a sentence, and how to put a job
// script there from this host.
type pgFiles struct {
	// Kind names the host side: "On this host" for a directory of the
	// host, "Docker volume" for a volume.
	Kind string
	// Host is the directory of the host or the name of the volume, Node
	// the path in every node.
	Host, Node string
	// Text says it in a sentence.
	Text string
	// Copy is the command that copies a job script in from this host: cp
	// into the directory, or docker cp into a node of the cluster, which
	// mounts the volume; "" while there is no place to copy to.
	Copy string
}

// pgFilesOf returns where the files are with the data mount m, whose
// cluster has the node container, which a copy into a volume goes
// through. A volume that is missing (m.OK false) has nothing to copy to.
func pgFilesOf(m cluster.MountPoint, container string) pgFiles {
	node := cmp.Or(m.Path, cluster.DefaultDataMountPath)
	f := pgFiles{Host: m.Source, Node: node}
	switch {
	case m.Type == config.StorageHostPath:
		f.Kind = "On this host"
		if m.Source == "" {
			f.Text = "Say which directory of this host the nodes see at " + node + "."
			return f
		}
		f.Text = "Put your batch script in " + m.Source + ". Every node sees it at " + node + ", and the output of your jobs lands there too."
		f.Copy = "cp job.sh " + pgShellPath(strings.TrimSuffix(m.Source, "/")+"/")
	case !m.OK:
		f.Kind = "Docker volume"
		f.Text = "Docker has no volume " + m.Source + ", so " + node + " is empty in the nodes."
	default:
		f.Kind = "Docker volume"
		f.Text = "The volume " + m.Source + " is " + node + " in every node. Jobs read scripts from it and write their output to it."
		f.Copy = "docker cp job.sh " + action.Quote([]string{container + ":" + strings.TrimSuffix(node, "/") + "/"})
	}
	return f
}

// pgShellPath quotes a path for the shell, but for a leading ~/, which the
// shell then still expands.
func pgShellPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return "~/" + action.Quote([]string{rest})
	}
	return action.Quote([]string{p})
}

// pgFilesNode is the node of the cluster that a copy into its volume goes
// through: the submitter, where jobs start, or else the controller.
func pgFilesNode(nodes []pgNode) string {
	out := ""
	for _, n := range nodes {
		switch n.Role {
		case config.RoleSubmitter:
			return n.Container
		case config.RoleController:
			if out == "" {
				out = n.Container
			}
		}
	}
	return out
}
