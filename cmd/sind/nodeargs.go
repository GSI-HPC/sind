// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/go-nodeset"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/spf13/cobra"
)

// noArgs is the argument check of a command that takes no argument. It
// says so in the words of cobra's other arity checks, where cobra.NoArgs
// reports the first argument as an unknown command.
func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("accepts no arguments, received %d", len(args))
	}
	return nil
}

// optionalCluster is the argument check of a command whose only, optional
// argument is a cluster name.
func optionalCluster(cmd *cobra.Command, args []string) error {
	if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
		return err
	}
	if len(args) == 1 {
		return config.CheckName("cluster", args[0])
	}
	return nil
}

// defaultClusterHelp is the line of the Long of a command that names the
// optional cluster argument arg.
func defaultClusterHelp(arg string) string {
	return arg + ` defaults to "` + config.DefaultClusterName + `".`
}

// nodesHelp describes the NODES argument of the commands that take node
// sets, power and delete worker.
const nodesHelp = `NODES is one or more node arguments: a node (worker-0), a node of a cluster
(worker-0.dev) or a node set (worker-[0-3], worker-[0-3].dev). The cluster
defaults to "default". Quote a node set from the shell, which would
otherwise expand worker-[0-3] to the files in the working directory that
match it.`

// nodeHelp describes the NODE argument of the commands that act on one
// node, ssh, logs and get node.
const nodeHelp = `NODE is a node (worker-0) or a node of a cluster (worker-0.dev). The
cluster defaults to "default".`

// nodesExample returns the Example of a command that takes node sets: a
// quoted node set, and node, a node of another cluster.
func nodesExample(command, node string) string {
	return "  " + command + " 'worker-[0-3]'\n  " + command + " " + node
}

// clusterArg returns the cluster named by the argument of a command that
// optionalCluster checks, the default cluster without one.
func clusterArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return config.DefaultClusterName
}

// nodeTarget is a resolved node with its short name and cluster.
type nodeTarget struct {
	ShortName string
	Cluster   string
}

// errNoGroups answers every @group reference in a node argument.
var errNoGroups = errors.New("sind has no node groups")

// noGroups is the group resolver of node arguments. sind has no node groups,
// so a reference to one is an error rather than a set of no nodes.
type noGroups struct{}

func (noGroups) Resolve(string, string) (string, error) { return "", errNoGroups }
func (noGroups) All(string) (string, error)             { return "", errNoGroups }

// parseNodeArgs evaluates a node set expression, such as
// "worker-[0-3].dev!worker-2.dev", and resolves the cluster of each node it
// names. Whitespace is a union, as a comma is, so the arguments of a command
// may be joined with a space. Each name is split at its last "." into
// <shortName>.<cluster>, the cluster defaulting to "default". The targets
// come in go-nodeset's order: by the name with its numbers left out, then
// by number. An expression that names no node is an error, and so is one
// that names nodes of two clusters whose names differ only in zero padding
// (see paddingClash), and one that is not NODE[.CLUSTER], such as an FQDN
// (see parseNodeName).
func parseNodeArgs(expr string) ([]nodeTarget, error) {
	set, err := nodeset.ParseWith(expr, noGroups{})
	if err != nil {
		return nil, argsErrorf("expanding nodes: %w", err)
	}
	if first, second, found := paddingClash(expr); found {
		return nil, argsErrorf("expanding nodes: %s and %s differ only in zero padding, so a node set takes them for one node, "+
			"but they are in two clusters, %s and %s; name the nodes of each cluster in a command of its own",
			first, second, clusterOf(first), clusterOf(second))
	}
	names := set.Expand()
	if len(names) == 0 {
		return nil, argsErrorf("expanding nodes: %q names no node", expr)
	}

	targets := make([]nodeTarget, 0, len(names))
	for _, name := range names {
		t, err := parseNodeName(name, typedTerm(expr, name))
		if err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// parseOneNode parses the node argument of a command that acts on one node,
// such as ssh, logs and get node, named command in the error: a node set
// that names any other number of nodes is a usage error.
func parseOneNode(command, arg string) (nodeTarget, error) {
	targets, err := parseNodeArgs(arg)
	if err != nil {
		return nodeTarget{}, err
	}
	if len(targets) != 1 {
		return nodeTarget{}, argsErrorf("%s requires exactly one node, got %d", command, len(targets))
	}
	return targets[0], nil
}

// typedTerm returns the term of the node set expression expr that names
// name, as the user typed it, such as worker-[0-1].dev for worker-0.dev,
// for the errors about name. Without one, it returns name.
func typedTerm(expr, name string) string {
	for _, term := range nodeTerms(expr) {
		if set, err := nodeset.Parse(term); err == nil && slices.Contains(set.Expand(), name) {
			return term
		}
	}
	return name
}

// parseNodeName splits one node name into <shortName>.<cluster>, the cluster
// defaulting to "default", and checks both parts. A name with more dots is
// not NODE[.CLUSTER]: the FQDN that get nodes prints is NODE.CLUSTER.REALM.sind,
// which the node commands do not take, as they act in the realm of --realm.
// The errors quote typed, the term of the node argument that named name.
func parseNodeName(name, typed string) (nodeTarget, error) {
	t := nodeTarget{ShortName: name, Cluster: clusterOf(name)}
	if i := strings.LastIndex(name, "."); i >= 0 {
		t.ShortName = name[:i]
	}
	if t.ShortName == "" || t.Cluster == "" {
		return nodeTarget{}, argsErrorf("invalid node name %q", typed)
	}
	if strings.Contains(t.ShortName, ".") {
		if strings.HasSuffix(name, "."+cluster.DNSSuffix) {
			return nodeTarget{}, argsErrorf("use NODE[.CLUSTER], not the FQDN %q", typed)
		}
		return nodeTarget{}, argsErrorf("invalid node name %q: want NODE[.CLUSTER]", typed)
	}
	if err := config.CheckName("node", t.ShortName); err != nil {
		if t.ShortName == typed {
			return nodeTarget{}, argsError(err)
		}
		// The short name of a name with a cluster, or of a node set.
		return nodeTarget{}, argsErrorf("invalid node name %q: %w", typed, config.CheckName("short", t.ShortName))
	}
	if err := config.CheckName("cluster", t.Cluster); err != nil {
		return nodeTarget{}, argsErrorf("invalid node name %q: %w", typed, err)
	}
	return t, nil
}

// clusterOf returns the cluster of a node name: what follows its last ".",
// "default" for a name without one.
func clusterOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return config.DefaultClusterName
}

// paddingClash finds two names in expr that go-nodeset takes for one node,
// as they differ only in zero padding, though they name nodes of two
// clusters: worker-0.dev1 and worker-0.dev01. The padding of the cluster
// suffix's numbers is no more part of a name's identity than the short
// name's, so the expression would act on one of the two nodes only, while
// sind's clusters dev1 and dev01 are two. It returns the two names as the
// terms of expr write them, and found false when there are none.
//
// Each term is expanded on its own, so that every spelling is seen, and
// only when the cluster suffix of one of them holds a number, the only way
// two clusters' names can differ only in padding.
func paddingClash(expr string) (first, second string, found bool) {
	terms := nodeTerms(expr)
	if !slices.ContainsFunc(terms, func(term string) bool {
		return strings.ContainsAny(clusterOf(term), "0123456789")
	}) {
		return "", "", false
	}
	seen := nodeset.New()
	for _, term := range terms {
		set, err := nodeset.Parse(term)
		if err != nil {
			// expr parsed, so its terms do too; a term that does not
			// names nothing to compare.
			continue
		}
		for _, name := range set.Expand() {
			prev, ok := seen.Canonical(name)
			switch {
			case !ok:
				_ = seen.Add(name) // a name Expand returned parses
			case clusterOf(prev) != clusterOf(name):
				return prev, name, true
			}
		}
	}
	return "", "", false
}

// nodeTerms splits a node set expression into its terms, the operands of
// its operators, as go-nodeset reads them: a comma, whitespace, "!", "&"
// and "^" outside brackets end a term, and empty terms are left out.
func nodeTerms(expr string) []string {
	var terms []string
	start, depth := 0, 0
	end := func(i int) {
		if term := strings.TrimSpace(expr[start:i]); term != "" {
			terms = append(terms, term)
		}
		start = i + 1
	}
	for i := 0; i < len(expr); i++ {
		switch c := expr[i]; {
		case c == '[':
			depth++
		case c == ']':
			depth--
		case depth > 0:
		case strings.IndexByte(", \t\n\r!&^", c) >= 0:
			end(i)
		}
	}
	end(len(expr))
	return terms
}

// clusterNodes is the short names of the targets in one cluster.
type clusterNodes struct {
	Cluster    string
	ShortNames []string
}

// groupByCluster groups node targets by cluster, keeping their order: the
// clusters come in the order of their first node.
func groupByCluster(targets []nodeTarget) []clusterNodes {
	var groups []clusterNodes
	index := make(map[string]int)
	for _, t := range targets {
		i, ok := index[t.Cluster]
		if !ok {
			i = len(groups)
			index[t.Cluster] = i
			groups = append(groups, clusterNodes{Cluster: t.Cluster})
		}
		groups[i].ShortNames = append(groups[i].ShortNames, t.ShortName)
	}
	return groups
}
