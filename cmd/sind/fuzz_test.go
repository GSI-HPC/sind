// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/pkg/config"
)

// FuzzParseNodeArgs checks the reader of the node arguments of the CLI: no
// expression makes it panic, and one it takes names at least one node,
// each with a short name and a cluster that sind takes as names, and none
// of them twice.
func FuzzParseNodeArgs(f *testing.F) {
	for _, seed := range []string{
		"",
		"controller",
		"worker-[0-3]",
		"worker-[0-3].dev,controller.dev",
		"worker-[0-3]!worker-2",
		"worker-[0-3]&worker-[2-5]^worker-9",
		"worker-0.dev1,worker-0.dev01",
		"worker-[0-3]!worker-2.default",
		"worker-0.default.sind.sind",
		"@group",
		"worker-[00-03]",
		"worker-[0-3",
		"a.b.c",
		".dev",
		"worker-0.",
		"worker-[0-1][0-1]",
		"WORKER-0",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		targets, err := parseNodeArgs(expr)
		if err != nil {
			return
		}
		require.NotEmpty(t, targets)
		seen := map[nodeTarget]bool{}
		for _, target := range targets {
			assert.NoError(t, config.CheckName("node", target.ShortName), "of %q", expr)
			assert.NoError(t, config.CheckName("cluster", target.Cluster), "of %q", expr)
			assert.False(t, seen[target], "%v twice of %q", target, expr)
			seen[target] = true
		}
		if len(targets) == 1 {
			one, err := parseOneNode("ssh", expr)
			require.NoError(t, err)
			assert.Equal(t, targets[0], one)
		}
	})
}
