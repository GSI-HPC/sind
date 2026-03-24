// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/nodeset"
)

// nodeTarget is a resolved node with its short name and cluster.
type nodeTarget struct {
	ShortName string
	Cluster   string
}

// parseNodeArgs expands nodeset patterns and resolves cluster names.
// Each expanded name is split as <shortName>.<cluster>, defaulting to "default".
func parseNodeArgs(args string) ([]nodeTarget, error) {
	expanded, err := nodeset.Expand(args)
	if err != nil {
		return nil, fmt.Errorf("expanding nodes: %w", err)
	}

	targets := make([]nodeTarget, 0, len(expanded))
	for _, name := range expanded {
		t := nodeTarget{ShortName: name, Cluster: "default"}
		if i := strings.LastIndex(name, "."); i >= 0 {
			t.ShortName = name[:i]
			t.Cluster = name[i+1:]
		}
		if t.ShortName == "" || t.Cluster == "" {
			return nil, fmt.Errorf("invalid node name %q", name)
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// groupByCluster groups node targets by cluster name, preserving order.
func groupByCluster(targets []nodeTarget) map[string][]string {
	result := make(map[string][]string)
	for _, t := range targets {
		result[t.Cluster] = append(result[t.Cluster], t.ShortName)
	}
	return result
}
