// SPDX-License-Identifier: LGPL-3.0-or-later

// Package nodefold folds the names of nodes into node sets as sind's node
// arguments take them. It is the one fold of the Go code of sind ui: the
// commands that its dialogs build, the titles of the runs and what the run
// pages say of their nodes. The page scripts fold by nodeset.js; the cases
// of testdata/nodesets.json state what nodeset.js and go-nodeset read alike.
package nodefold

import (
	"regexp"
	"slices"
	"strings"

	"github.com/GSI-HPC/go-nodeset"

	"github.com/GSI-HPC/sind/pkg/cluster"
)

// nodeRE matches the name of a node: a short name, and maybe its cluster
// after a dot.
var nodeRE = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)?$`)

// Words folds names of nodes into node sets, one word for each cluster:
// worker-[0-2] for worker-0, worker-1 and worker-2. A name may name its
// cluster after a dot, as worker-0.dev: the names of each cluster fold
// apart, so the cluster follows the set, as in controller.dev,worker-[0-1].dev,
// and two clusters never fold into one range. The names of a cluster that
// do not fold back to exactly these nodes stay one word each, and so does
// a name that is no node's, such as an image's. A name counts once. The
// words come in the order of the first name of each.
func Words(names []string) []string {
	var keys []string
	groups := map[string][]string{}
	for _, name := range names {
		key := "\x00" + name
		if nodeRE.MatchString(name) {
			_, key, _ = strings.Cut(name, ".")
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		if !slices.Contains(groups[key], name) {
			groups[key] = append(groups[key], name)
		}
	}
	var out []string
	for _, key := range keys {
		out = append(out, fold(groups[key])...)
	}
	return out
}

// fold returns the names as one node set, or the names themselves where
// the set would not give back exactly these nodes.
func fold(names []string) []string {
	if set, err := nodeset.Parse(strings.Join(names, ",")); err == nil {
		folded := set.String()
		back, err := nodeset.Parse(folded)
		if err == nil && slices.Equal(slices.Sorted(slices.Values(back.Expand())), slices.Sorted(slices.Values(names))) {
			return []string{folded}
		}
	}
	return names
}

// Set is Words of the short names of nodes of one cluster, named as sind
// takes them (cluster.QualifiedNodeName), joined by commas: worker-[0-2].dev,
// or worker-[0-2] in the default cluster.
func Set(clusterName string, shorts []string) string {
	return strings.Join(Of(clusterName, shorts), ",")
}

// Of is Words of the short names of nodes of one cluster, named as sind
// takes them (cluster.QualifiedNodeName).
func Of(clusterName string, shorts []string) []string {
	names := make([]string, len(shorts))
	for i, s := range shorts {
		names[i] = cluster.QualifiedNodeName(clusterName, s)
	}
	return Words(names)
}
