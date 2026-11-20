// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slurmNodeSets are node sets in the syntax that sind and Slurm share:
// names, ranges and lists of numbers, with and without zero padding. Both
// must read the same nodes from each.
var slurmNodeSets = []string{
	"worker-0",
	"controller,worker-0",
	"worker-[0-3]",
	"worker-[0-1,3]",
	"worker-[3,0-1]",
	"controller,worker-[4-5]",
	"worker-[1-3],controller",
	"worker-[08-11]",
	"worker-[007]",
	"worker-[01-03,5]",
	"worker-[5,01-03]",
	"worker-[1-3,2-5]",
	"worker-[1,1]",
	"worker-1,worker-1",
	"gpu[1-2],n5",
	"login-[0-1],worker-[00-02],worker-[10-11]",
	"worker-[1-3]-ib",
	"worker-[1-3]ib",
	"rack[1-2]-node-[1-2]",
}

// nodeSetsApart are the node sets that sind and Slurm are known to read
// differently, with the nodes of each. A change of either reader that ends
// a difference fails the test: move the node set to slurmNodeSets then.
var nodeSetsApart = []struct {
	set         string
	slurm, sind []string
}{
	// go-nodeset takes two names that differ only in zero padding for one.
	{"worker-01,worker-1", []string{"worker-01", "worker-1"}, []string{"worker-01"}},
}

// randomNodeSets returns n node sets in the shared syntax, the same ones
// in each run.
func randomNodeSets(n int) []string {
	rng := rand.New(rand.NewPCG(1, 2))
	prefixes := []string{"worker-", "login-", "gpu", "n"}
	sets := make([]string, n)
	for i := range sets {
		// One width for the numbers of a prefix, 0 for no padding: the
		// names of a set then never differ only in their padding.
		widths := make([]int, len(prefixes))
		for j := range widths {
			widths[j] = []int{0, 0, 2, 3}[rng.IntN(4)]
		}
		var terms []string
		for range 1 + rng.IntN(3) {
			p := rng.IntN(len(prefixes))
			prefix := prefixes[p]
			num := func(v int) string { return fmt.Sprintf("%0*d", widths[p], v) }
			if rng.IntN(4) == 0 {
				terms = append(terms, prefix+num(rng.IntN(120)))
				continue
			}
			var items []string
			for range 1 + rng.IntN(3) {
				from := rng.IntN(120)
				if rng.IntN(3) == 0 {
					items = append(items, num(from))
					continue
				}
				items = append(items, num(from)+"-"+num(from+rng.IntN(12)))
			}
			terms = append(terms, prefix+"["+strings.Join(items, ",")+"]")
		}
		sets[i] = strings.Join(terms, ",")
	}
	return sets
}

// slurmHostnames returns the names that scontrol show hostnames of the
// node image reads from each node set, sorted and each one time, in one
// container. scontrol needs a slurm.conf and no controller for this.
func slurmHostnames(ctx context.Context, t *testing.T, c *docker.Client, sets []string) [][]string {
	t.Helper()
	const script = `set -f
printf 'ClusterName=x\nSlurmctldHost=localhost\n' > /tmp/slurm.conf
export SLURM_CONF=/tmp/slurm.conf
for s in "$@"; do
  if out=$(scontrol show hostnames "$s" 2>&1); then
    echo "ok $(echo $out)"
  else
    echo "error $(echo $out)"
  fi
done`
	args := append([]string{"-c", script, "sh"}, sets...)
	stdout, err := c.RunEphemeralWith(ctx, []string{"--entrypoint", "sh"}, testutil.Image(), args...)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.Len(t, lines, len(sets), "one line for each node set: %q", stdout)
	names := make([][]string, len(sets))
	for i, line := range lines {
		status, rest, _ := strings.Cut(line, " ")
		require.Equal(t, "ok", status, "scontrol show hostnames %q: %s", sets[i], rest)
		names[i] = strings.Fields(rest)
		slices.Sort(names[i])
		names[i] = slices.Compact(names[i])
	}
	return names
}

// sindNodes returns the short names of the nodes that sind reads from a
// node argument, sorted.
func sindNodes(t *testing.T, set string) []string {
	t.Helper()
	targets, err := parseNodeArgs(set)
	require.NoError(t, err, set)
	names := make([]string, len(targets))
	for i, target := range targets {
		names[i] = target.ShortName
	}
	slices.Sort(names)
	return names
}

// TestNodeSetsAsSlurmReadsThem compares the nodes that sind reads from a
// node set (parseNodeArgs, with go-nodeset) with the names that Slurm reads
// from it (scontrol show hostnames in the node image): a user writes a node
// argument as a Slurm host list. sind also takes what Slurm does not, such
// as steps and the operators of a node set; the test has none of these.
func TestNodeSetsAsSlurmReadsThem(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	sets := slices.Concat(slurmNodeSets, randomNodeSets(300))
	for _, apart := range nodeSetsApart {
		sets = append(sets, apart.set)
	}
	slurm := slurmHostnames(ctx, t, c, sets)
	alike := len(sets) - len(nodeSetsApart)
	for i, set := range sets[:alike] {
		assert.Equal(t, slurm[i], sindNodes(t, set), "node set %q: Slurm's names, then sind's", set)
	}
	for i, apart := range nodeSetsApart {
		assert.Equal(t, apart.slurm, slurm[alike+i], "Slurm reads %q", apart.set)
		assert.Equal(t, apart.sind, sindNodes(t, apart.set), "sind reads %q", apart.set)
	}
}
