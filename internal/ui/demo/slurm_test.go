// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlurmOfTheSeededWorld(t *testing.T) {
	st := store.New()
	w := Seeded()
	w.Publish(st)
	sl := st.Snapshot().Slurm("sind", "dev")
	require.NotNil(t, sl)
	require.Empty(t, sl.Err)
	states := map[string]string{}
	for _, n := range sl.Info.Nodes {
		states[n.Name] = n.State + "/" + n.Reason
	}
	assert.Equal(t, map[string]string{
		"worker-0": "allocated/", "worker-1": "mixed/", "worker-2": "idle/", "worker-3": "idle/",
		"worker-4": "down*/Not responding", "worker-5": "drained/memory errors, admin 09:02",
	}, states)
	assert.Equal(t, []slurm.PartitionInfo{{Name: "all", Default: true, State: "up", TimeLimit: "infinite", Nodes: 6, NodeList: "worker-[0-5]"}}, sl.Info.Partitions)
	conf := sl.Info.Files[0].Content
	assert.Contains(t, conf, "ClusterName=dev\n")
	assert.Contains(t, conf, "SlurmctldHost=controller-backup\n")
	assert.Contains(t, conf, "AccountingStorageType=accounting_storage/slurmdbd")
	assert.Contains(t, sl.Info.Files[1].Content, "NodeName=worker-0 CPUs=2 RealMemory=1024")

	legacy := st.Snapshot().Slurm("sind", "legacy")
	assert.Equal(t, "down*", legacy.Info.Nodes[1].State, "a stopped worker does not answer")
	assert.NotContains(t, legacy.Info.Files[0].Content, "AccountingStorageType")

	// A cluster that goes takes what Slurm says with it.
	w.configure("ci", "pr-145", func(c *clusterState) { c.nodes = nil })
	w.Publish(st)
	assert.Nil(t, st.Snapshot().Slurm("ci", "pr-145"))
}

func TestSlurmOfAControllerThatDoesNotAnswer(t *testing.T) {
	c := &clusterState{name: "x"}
	assert.Contains(t, slurmOf(c).Err, "No such container")
	ctl := &nodeState{short: "controller", state: cluster.StateStopped, up: map[probe.Service]bool{}}
	c.nodes = []*nodeState{ctl}
	assert.Contains(t, slurmOf(c).Err, "is not running")
	ctl.state = cluster.StateRunning
	assert.Contains(t, slurmOf(c).Err, "Unable to contact slurm controller")
	assert.Len(t, slurmOf(c).Info.Files, 2, "cat reads the files without slurmctld")
	assert.Empty(t, slurmOf(c).Info.Nodes)
	ctl.up[probe.ServiceSlurmctld] = true
	sl := slurmOf(c)
	assert.Empty(t, sl.Info.Nodes)
	assert.Empty(t, sl.Info.Partitions, "no worker, no partition")
}

func TestHostList(t *testing.T) {
	assert.Empty(t, hostList(nil))
	assert.Equal(t, "worker-3", hostList([]string{"worker-3"}))
	assert.Equal(t, "worker-[2-4]", hostList([]string{"worker-2", "worker-3", "worker-4"}))
	assert.Equal(t, "worker-0,worker-2", hostList([]string{"worker-0", "worker-2"}))
	assert.Equal(t, "gpu,worker-1", hostList([]string{"gpu", "worker-1"}))
	assert.True(t, strings.HasPrefix(hostList([]string{"worker-1", "x"}), "worker-1,"))
}
