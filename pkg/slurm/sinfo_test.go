// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSinfoNodes(t *testing.T) {
	out := "worker-0|mixed|1/1/0/2|1024|all*|none\n" +
		"worker-1|drained|0/0/2/2|2048|all*|disk check | by admin\n" +
		"\n" +
		"worker-0|mixed|1/1/0/2|1024|debug|none\n"
	nodes, err := ParseSinfoNodes(out)
	require.NoError(t, err)
	assert.Equal(t, []NodeInfo{
		{Name: "worker-0", State: "mixed", CPUs: 2, AllocCPUs: 1, MemoryMB: 1024, Partitions: []string{"all", "debug"}},
		{Name: "worker-1", State: "drained", Reason: "disk check | by admin", CPUs: 2, MemoryMB: 2048, Partitions: []string{"all"}},
	}, nodes)

	nodes, err = ParseSinfoNodes("")
	require.NoError(t, err)
	assert.Empty(t, nodes)

	for _, bad := range []string{
		"worker-0|idle|0/2/0/2|1024|all",
		"worker-0|idle|0/2/0|1024|all|none",
		"worker-0|idle|x/2/0/2|1024|all|none",
		"worker-0|idle|0/2/0/x|1024|all|none",
		"worker-0|idle|0/2/0/2|lots|all|none",
	} {
		_, err := ParseSinfoNodes(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseSinfoPartitions(t *testing.T) {
	parts, err := ParseSinfoPartitions("all*|up|infinite|4|worker-[0-3]\n\ndebug|down|30:00|1|worker-0\n")
	require.NoError(t, err)
	assert.Equal(t, []PartitionInfo{
		{Name: "all", Default: true, State: "up", TimeLimit: "infinite", Nodes: 4, NodeList: "worker-[0-3]"},
		{Name: "debug", State: "down", TimeLimit: "30:00", Nodes: 1, NodeList: "worker-0"},
	}, parts)
	_, err = ParseSinfoPartitions("all*|up|infinite|4")
	assert.Error(t, err)
	_, err = ParseSinfoPartitions("all*|up|infinite|four|worker-0")
	assert.Error(t, err)
}

func TestNodeStateTakesJobs(t *testing.T) {
	for _, s := range []string{"idle", "allocated", "mixed", "completing", "planned", "reserved"} {
		assert.True(t, NodeStateTakesJobs(s), s)
	}
	for _, s := range []string{"down", "down*", "idle*", "drained", "draining", "fail", "failing", "maint", "powered_down", "reboot_issued", "unknown", "future", "inval"} {
		assert.False(t, NodeStateTakesJobs(s), s)
	}
}
