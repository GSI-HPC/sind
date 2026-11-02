// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"slices"
	"strings"

	"github.com/GSI-HPC/go-nodeset"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/config"
)

// nodeCommand reports whether the command of the name works on nodes, so
// that its title names them rather than their cluster: a power command,
// create worker and delete worker.
func nodeCommand(name string) bool {
	return strings.HasPrefix(name, "power ") || name == "create worker" || name == "delete worker"
}

// NodeNames returns the short names of the nodes that a run works on, in
// the order the log names them, each once: the targets with a role, as
// sind gives a node's target, but for those of the step rollback, which
// undoes the work.
func NodeNames(root store.Span) []string {
	var names []string
	for _, c := range root.Children {
		if c.Kind == KindStep && c.Name == "rollback" {
			continue
		}
		Walk(store.Span{Children: []store.Span{c}}, func(s store.Span) {
			if s.Kind == KindTarget && s.Role != "" && s.Node != "" && !slices.Contains(names, s.Node) {
				names = append(names, s.Node)
			}
		})
	}
	return names
}

// title is how a user reads the command of a run: the name of its command
// span and what it works on, as "create cluster dev". A command that works
// on nodes of one cluster names its nodes, once the log has them, as sind
// takes them: "power reboot worker-[0-2].dev", "create worker
// worker-2.legacy".
func title(root store.Span) string {
	words := root.Name
	if root.Node == "" {
		return words
	}
	names := NodeNames(root)
	if !nodeCommand(root.Name) || strings.Contains(root.Node, ",") || len(names) == 0 {
		return words + " " + root.Node
	}
	return words + " " + FoldNodes(root.Node, names)
}

// FoldNodes writes the short names of nodes of the cluster as sind takes
// them: one node set, such as worker-[0-2].dev, with the cluster but for
// the default one, or the names one by one, joined by commas, where they
// do not fold back to the same nodes.
func FoldNodes(cluster string, shorts []string) string {
	args := make([]string, len(shorts))
	for i, s := range shorts {
		args[i] = s
		if cluster != config.DefaultClusterName {
			args[i] += "." + cluster
		}
	}
	joined := strings.Join(args, ",")
	set, err := nodeset.Parse(joined)
	if err != nil || !slices.Equal(slices.Sorted(slices.Values(set.Expand())), slices.Sorted(slices.Values(args))) {
		return joined
	}
	return set.String()
}
