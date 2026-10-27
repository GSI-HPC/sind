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
		for _, n := range splitList(v) {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
	}
	return names
}

// pickNodes returns the nodes of the cluster that the form's field "nodes"
// names, which must all exist and, where workersFor says what the command
// does to them, such as "deleted", be workers.
func pickNodes(f Form, c Context, realm, clusterName, workersFor string) ([]cluster.NodeSummary, error) {
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
		if workersFor != "" && n.Role != config.RoleWorker {
			return nil, fmt.Errorf("nodes: %s is the %s, and only workers can be %s", name, n.Role, workersFor)
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
	if n, ok := highestWorker(nodes); ok {
		next = workerNumber(n) + 1
	}
	out := make([]string, count)
	for i := range out {
		out[i] = "worker-" + strconv.Itoa(next+i)
	}
	return out
}

// NewestWorker returns the worker of nodes that new workers take their
// image and size from, as sind create worker picks it: the one with the
// highest number among the workers that are managed like the controller,
// as the status st says, or among all workers where none is or st, which
// may be nil, does not say.
func NewestWorker(nodes []cluster.NodeSummary, st *cluster.Status) (cluster.NodeSummary, bool) {
	newest, ok := highestWorker(nodes)
	if !ok || st == nil {
		return newest, ok
	}
	managed := map[string]bool{}
	ctlSeen, want := false, false
	for _, n := range st.Nodes {
		short, _, _ := strings.Cut(n.Name, ".")
		managed[short] = n.Managed
		if n.Role == config.RoleController && !ctlSeen {
			ctlSeen, want = true, n.Managed
		}
	}
	if !ctlSeen {
		return newest, true
	}
	var alike []cluster.NodeSummary
	for _, n := range nodes {
		if m, known := managed[ShortName(n)]; known && m == want {
			alike = append(alike, n)
		}
	}
	if w, ok := highestWorker(alike); ok {
		return w, true
	}
	return newest, true
}

// highestWorker returns the worker of nodes with the highest number.
func highestWorker(nodes []cluster.NodeSummary) (cluster.NodeSummary, bool) {
	var newest cluster.NodeSummary
	found := false
	for _, n := range nodes {
		if n.Role == config.RoleWorker && workerNumber(n) >= 0 && (!found || workerNumber(n) > workerNumber(newest)) {
			newest, found = n, true
		}
	}
	return newest, found
}

// workerNumber returns the number at the end of the short name of a worker,
// -1 for none.
func workerNumber(n cluster.NodeSummary) int {
	short := ShortName(n)
	if i := strings.LastIndex(short, "-"); i >= 0 {
		if v, err := strconv.Atoi(short[i+1:]); err == nil && v >= 0 {
			return v
		}
	}
	return -1
}
