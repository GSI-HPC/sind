// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/go-nodeset"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// ShortName returns the short name of a node, such as worker-1: the first
// label of its DNS name.
func ShortName(n cluster.NodeSummary) string {
	short, _, _ := strings.Cut(n.FQDN, ".")
	return short
}

// nodeNames returns the node names of the form's field "nodes", which may
// repeat and may hold several names separated by commas or spaces, without
// duplicates, in the order given.
func nodeNames(f Form) []string {
	var names []string
	for _, v := range f["nodes"] {
		for _, n := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	return names
}

// pickNodes returns the nodes of the cluster that the form's field "nodes"
// names, which must all exist and, with workersOnly, be workers.
func pickNodes(f Form, c Context, realm, clusterName string, workersOnly bool) ([]cluster.NodeSummary, error) {
	names := nodeNames(f)
	if len(names) == 0 {
		return nil, &Missing{Field: "nodes", Text: "pick at least one node of " + clusterName}
	}
	have := c.Snap.ClusterNodes(realm, clusterName)
	var picked []cluster.NodeSummary
	for _, name := range names {
		i := slices.IndexFunc(have, func(n cluster.NodeSummary) bool { return ShortName(n) == name })
		if i < 0 {
			return nil, fmt.Errorf("nodes: cluster %s has no node %s", clusterName, name)
		}
		n := have[i]
		if workersOnly && n.Role != config.RoleWorker {
			return nil, fmt.Errorf("nodes: %s is the %s, and only workers can be deleted", name, n.Role)
		}
		picked = append(picked, n)
	}
	return picked, nil
}

// nodeWords returns the node arguments for the short names of nodes of the
// cluster, as sind takes them: one folded set such as worker-[0-3].dev, or,
// should folding not give back exactly these nodes, the plain names.
func nodeWords(clusterName string, shorts []string) []string {
	names := make([]string, len(shorts))
	for i, s := range shorts {
		names[i] = s
		if clusterName != config.DefaultClusterName {
			names[i] = s + "." + clusterName
		}
	}
	if set, err := nodeset.Parse(strings.Join(names, ",")); err == nil {
		folded := set.String()
		back, err := nodeset.Parse(folded)
		want := slices.Sorted(slices.Values(names))
		if err == nil && slices.Equal(slices.Sorted(slices.Values(back.Expand())), want) {
			return []string{folded}
		}
	}
	return names
}

// nextWorkers returns the short names that new workers of a cluster with the
// nodes get: worker-N upward from the highest number there is.
func nextWorkers(nodes []cluster.NodeSummary, count int) []string {
	next := 0
	for _, n := range nodes {
		if n.Role != config.RoleWorker {
			continue
		}
		if i := strings.LastIndex(ShortName(n), "-"); i >= 0 {
			if v, err := strconv.Atoi(ShortName(n)[i+1:]); err == nil && v >= next {
				next = v + 1
			}
		}
	}
	out := make([]string, count)
	for i := range out {
		out[i] = "worker-" + strconv.Itoa(next+i)
	}
	return out
}
