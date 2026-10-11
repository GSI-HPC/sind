// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// targetNames renders targets as <shortName>.<cluster>, in order.
func targetNames(targets []nodeTarget) []string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.ShortName + "." + t.Cluster
	}
	return names
}

func TestParseNodeArgs(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want []string
	}{
		{"short name", "worker-0", []string{"worker-0.default"}},
		{"no number", "controller", []string{"controller.default"}},
		{"cluster suffix", "worker-0.dev", []string{"worker-0.dev"}},
		{"range", "worker-[0-2].dev", []string{"worker-0.dev", "worker-1.dev", "worker-2.dev"}},
		{"list", "worker-[0,2,5]", []string{"worker-0.default", "worker-2.default", "worker-5.default"}},
		{"padded range keeps its spelling", "worker-[00-02]", []string{"worker-00.default", "worker-01.default", "worker-02.default"}},
		{"step", "worker-[0-6/2]", []string{"worker-0.default", "worker-2.default", "worker-4.default", "worker-6.default"}},
		{"step with a list", "worker-[1-7/3,10]", []string{"worker-1.default", "worker-4.default", "worker-7.default", "worker-10.default"}},
		// Every number in a name is a dimension, the cluster's too; the
		// last number varies fastest.
		{"two dimensions", "worker-[0-1].dev[1-2]", []string{"worker-0.dev1", "worker-0.dev2", "worker-1.dev1", "worker-1.dev2"}},
		{"comma union", "controller.dev,worker-[0-1].prod", []string{"controller.dev", "worker-0.prod", "worker-1.prod"}},
		{"whitespace union", "worker-0 worker-1.dev\tcontroller", []string{"controller.default", "worker-0.default", "worker-1.dev"}},
		{"empty union operand", "worker-0,", []string{"worker-0.default"}},
		{"difference", "worker-[0-5]!worker-[2-3]", []string{"worker-0.default", "worker-1.default", "worker-4.default", "worker-5.default"}},
		{"intersection", "worker-[0-5]&worker-[4-9]", []string{"worker-4.default", "worker-5.default"}},
		{"symmetric difference", "worker-[0-3]^worker-[2-5]", []string{"worker-0.default", "worker-1.default", "worker-4.default", "worker-5.default"}},
		{"operator after whitespace", "worker-[0-3] !worker-1", []string{"worker-0.default", "worker-2.default", "worker-3.default"}},
		// Operators have no precedence: ((0-9 minus 0-4) and 3-6) is 5-6.
		{"left to right", "worker-[0-9]!worker-[0-4]&worker-[3-6]", []string{"worker-5.default", "worker-6.default"}},
		// Padding is not part of a node's identity; the first spelling wins.
		{"padding identity", "worker-1,worker-01", []string{"worker-1.default"}},
		{"padding identity first spelling", "worker-01,worker-1", []string{"worker-01.default"}},
		{"padding identity in a difference", "worker-[1-3]!worker-02", []string{"worker-1.default", "worker-3.default"}},
		{"duplicate", "worker-0,worker-0", []string{"worker-0.default"}},
		// Clusters whose names differ in more than padding stay apart, and
		// so do nodes of padded clusters that are not one node.
		{"clusters apart", "worker-0.dev1,worker-0.dev2", []string{"worker-0.dev1", "worker-0.dev2"}},
		{"padded clusters, other nodes", "worker-0.dev1,worker-1.dev01", []string{"worker-0.dev1", "worker-1.dev01"}},
		{"padding identity in one cluster", "worker-1.dev01,worker-01.dev01", []string{"worker-1.dev01"}},
		// Sorted by the name with its numbers left out, then by number.
		{"order", "worker-10,worker-2.dev,controller,worker-1", []string{"controller.default", "worker-1.default", "worker-10.default", "worker-2.dev"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets, err := parseNodeArgs(tt.expr)
			require.NoError(t, err)
			assert.Equal(t, tt.want, targetNames(targets))
		})
	}
}

func TestParseNodeArgs_SplitsAtLastDot(t *testing.T) {
	targets, err := parseNodeArgs("worker-0.lab.dev")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, nodeTarget{ShortName: "worker-0.lab", Cluster: "dev"}, targets[0])
}

func TestParseNodeArgs_Errors(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr string
	}{
		{"unbalanced bracket", "worker-[", "expanding nodes: unbalanced ["},
		{"descending range", "worker-[3-1]", `expanding nodes: in "worker-[3-1]": descending range "3-1"`},
		{"bad range bound", "worker-[a-2]", `invalid range bound "a"`},
		{"empty range", "worker-[]", "empty range"},
		{"padding mismatch", "worker-[1-010]", "padded to different widths"},
		{"adjacent numbers", "worker-0[0,10]", "two numeric parts are adjacent"},
		{"range too large", "worker-[0-99999999]", "expands to more than 1048576"},
		{"group", "@compute", "expanding nodes: group @compute: sind has no node groups"},
		{"group of a source", "worker-0,@slurm:idle", "expanding nodes: group @slurm:idle: sind has no node groups"},
		{"all of a source", "@*", "expanding nodes: group @*: sind has no node groups"},
		{"leading dash", "-oProxyCommand=x", `expanding nodes: "-oProxyCommand=x" is not a host name: it begins with -`},
		{"leading dash in a range", "-w[0-1]", "it begins with -"},
		{"dangling operator", "worker-[0-3]&", "the & operator has no right operand"},
		{"operator without left operand", "!worker-0", "the ! operator has no left operand"},
		{"comma before operator", "worker-[0-3],!worker-0", "the ! operator has no left operand"},
		{"empty", "", `expanding nodes: "" names no node`},
		{"whitespace only", " \t", "names no node"},
		{"empty result", "worker-[0-1]!worker-[0-1]", `expanding nodes: "worker-[0-1]!worker-[0-1]" names no node`},
		{"empty short name", ".dev", `invalid node name ".dev"`},
		{"trailing dot", "worker-0.", `invalid node name "worker-0."`},
		{"invalid cluster", "worker-[0-1].my_cluster", `invalid node name "worker-0.my_cluster": invalid cluster name "my_cluster"`},
		// go-nodeset would take each pair for one node, of the cluster
		// written first, and act on the other cluster's node not at all.
		{"clusters that differ only in padding", "worker-0.c01 worker-0.c1",
			"expanding nodes: worker-0.c01 and worker-0.c1 differ only in zero padding, so a node set takes them for one node, " +
				"but they are in two clusters, c01 and c1; name the nodes of each cluster in a command of its own"},
		{"padded clusters in a range", "worker-0.dev1,worker-[0-1].dev01", "worker-0.dev1 and worker-0.dev01 differ only in zero padding"},
		{"padded clusters in a difference", "worker-[0-3].dev1!worker-0.dev01", "worker-0.dev1 and worker-0.dev01 differ only in zero padding"},
		{"padded clusters in brackets", "worker-0.dev[01-02]&worker-0.dev2", "worker-0.dev02 and worker-0.dev2 differ only in zero padding"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets, err := parseNodeArgs(tt.expr)
			require.Error(t, err)
			assert.Nil(t, targets)
			assert.Contains(t, err.Error(), tt.wantErr)
			var ue *usageError
			assert.ErrorAs(t, err, &ue, "a node argument error is a usage error")
		})
	}
}

// The terms of an expression are what go-nodeset reads as the operands of
// its operators, brackets kept whole.
func TestNodeTerms(t *testing.T) {
	assert.Equal(t, []string{"worker-[0,2].dev1", "controller", "worker-1", "db", "w-[1-3]"},
		nodeTerms(" worker-[0,2].dev1,,controller\t!worker-1&db^w-[1-3] "))
	assert.Empty(t, nodeTerms(" , "))
}

// A term that does not parse names no node to compare: parseNodeArgs
// parsed the whole expression before.
func TestPaddingClash_SkipsATermThatDoesNotParse(t *testing.T) {
	_, _, found := paddingClash("worker-[.dev1,worker-0.dev01")
	assert.False(t, found)
}

func TestWithoutPadding(t *testing.T) {
	for in, want := range map[string]string{
		"worker-01.dev002": "worker-1.dev2",
		"worker-0.dev00":   "worker-0.dev0",
		"rack10n0010":      "rack10n10",
		"controller":       "controller",
	} {
		assert.Equal(t, want, withoutPadding(in), in)
	}
}

func TestNoGroups(t *testing.T) {
	_, err := noGroups{}.Resolve("", "compute")
	require.ErrorIs(t, err, errNoGroups)
	_, err = noGroups{}.All("slurm")
	require.ErrorIs(t, err, errNoGroups)
}

func TestGroupByCluster(t *testing.T) {
	targets := []nodeTarget{
		{ShortName: "worker-0", Cluster: "prod"},
		{ShortName: "worker-0", Cluster: "dev"},
		{ShortName: "worker-1", Cluster: "prod"},
		{ShortName: "controller", Cluster: "dev"},
	}

	assert.Equal(t, []clusterNodes{
		{Cluster: "prod", ShortNames: []string{"worker-0", "worker-1"}},
		{Cluster: "dev", ShortNames: []string{"worker-0", "controller"}},
	}, groupByCluster(targets))
}

func TestGroupByCluster_Empty(t *testing.T) {
	assert.Empty(t, groupByCluster(nil))
}
