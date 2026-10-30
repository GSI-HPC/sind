// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// fleetNode returns a managed node of ci/c that runs, with its services up
// but for the ones that down names.
func fleetNode(short string, role config.Role, svc string, down ...string) pgNode {
	n := pgNode{Realm: "ci", Cluster: "c", Short: short, Arg: short + ".c", Role: role, State: cluster.StateRunning, Managed: true, Known: true}
	n.Services = []pgSvc{{"munge", true}, {svc, true}}
	for _, d := range down {
		n.Services[1] = pgSvc{d, false}
	}
	return n
}

func TestPgFleetReadyOf(t *testing.T) {
	key := store.StatusKey{Realm: "ci", Cluster: "c"}
	probed := store.Snapshot{Statuses: map[store.StatusKey]*cluster.Status{key: {Name: "c"}}}
	noData := store.Snapshot{Statuses: map[store.StatusKey]*cluster.Status{key: {Name: "c", Mounts: []cluster.MountPoint{{Path: "/data", Source: "sind-c-data", Data: true}}}}}
	noSinfo := store.Snapshot{Statuses: probed.Statuses, Slurms: map[store.StatusKey]*store.Slurm{key: {Err: "slurm_load_partitions: Unable to contact slurm controller"}}}
	ctl := fleetNode("controller", config.RoleController, "slurmctld")
	w0, w1 := fleetNode("worker-0", config.RoleWorker, "slurmd"), fleetNode("worker-1", config.RoleWorker, "slurmd")
	down := fleetNode("worker-1", config.RoleWorker, "slurmd", "slurmd")
	c := func(nodes ...pgNode) pgCluster {
		return pgCluster{Realm: "ci", Name: "c", State: cluster.StateRunning, Nodes: nodes}
	}
	// busy is a cluster that a command works on: what is down may be what
	// it starts.
	busy := func(nodes ...pgNode) pgCluster {
		b := c(nodes...)
		for i := range b.Nodes {
			b.Nodes[i].Busy = true
		}
		b.Runs = []pgRun{{ID: "r", Title: "power reboot worker-1.c", Running: true}}
		return b
	}
	for _, tc := range []struct {
		name        string
		sn          store.Snapshot
		c           pgCluster
		pill        pgPill
		words, note string
	}{
		{"every worker takes jobs", probed, c(ctl, w0, w1), pgPill{"ready", "ok"}, "2 of 2 workers take jobs", ""},
		{"one worker", probed, c(ctl, w0), pgPill{"ready", "ok"}, "1 of 1 worker takes jobs", ""},
		{"a worker takes none", probed, c(ctl, w0, down), pgPill{"needs a look", "bad"}, "1 of 2 workers takes jobs", ""},
		{"no worker takes jobs", probed, c(ctl, down), pgPill{"not ready", "bad"}, "no worker takes jobs", ""},
		{"no worker at all", probed, c(ctl), pgPill{"not ready", "bad"}, "no worker", "Slurm runs jobs on workers. Add one."},
		{"the controller is down", probed, c(fleetNode("controller", config.RoleController, "slurmctld", "slurmctld"), w0), pgPill{"not ready", "bad"}, "The controller does not answer", "controller: slurmctld is down."},
		{"sinfo fails", noSinfo, c(ctl, w0), pgPill{"not ready", "bad"}, "The controller does not answer", "sinfo fails: slurm_load_partitions: Unable to contact slurm controller"},
		{"a node that is no worker needs a look", probed, c(ctl, w0, fleetNode("db", config.RoleDB, "slurmdbd", "slurmdbd")), pgPill{"needs a look", "bad"}, "1 of 1 worker takes jobs", ""},
		{"its data volume is missing", noData, c(ctl, w0), pgPill{"needs a look", "warn"}, "The data volume is missing", "Docker has no volume sind-c-data, so /data is empty."},
		{"before the first probe", store.Snapshot{}, c(ctl, w0), pgPill{"not checked yet", ""}, "sind ui has not probed it yet", ""},
		{"a stopped node before the first probe", store.Snapshot{}, c(ctl, pgNode{Short: "worker-0", Role: config.RoleWorker, State: cluster.StateStopped}), pgPill{"needs a look", "bad"}, "sind ui has not probed it yet", ""},
		{"a command works on a worker", probed, busy(ctl, w0, down), pgPill{"a command runs", "run"}, "1 of 2 workers takes jobs", ""},
		{"a command works on the only worker", probed, busy(ctl, down), pgPill{"a command runs", "run"}, "no worker takes jobs", ""},
		{"a command runs while a node is stopped", probed, func() pgCluster {
			b := busy(ctl, w0)
			b.Nodes[1].State, b.State = cluster.StateStopped, cluster.StateMixed
			return b
		}(), pgPill{"not ready", "bad"}, "no worker takes jobs", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pill, words, note := pgFleetReadyOf(tc.sn, tc.c)
			assert.Equal(t, tc.pill, pill)
			assert.Equal(t, tc.words, words)
			assert.Equal(t, tc.note, note)
			// The chip Needs a look keeps the clusters whose pill is
			// amber or red, and those of every page that need a look.
			row := pgFleetRowOf(tc.sn, tc.c, false)
			assert.Equal(t, pill.Class == "warn" || pill.Class == "bad", row.In("problem"))
			if tc.c.NeedsLook() {
				assert.True(t, row.In("problem"))
			}
		})
	}
}

func TestPgFleetLifeReady(t *testing.T) {
	phases := []pgPhase{{Node: "controller", State: runOK}, {Node: "worker-0", State: runRunning, Text: "waits for munge · 19s of 5m"}, {Node: "worker-1", State: runQueued}}
	for _, tc := range []struct {
		name        string
		l           pgLife
		pill, words string
		node, now   string
	}{
		{"a create", pgLife{Kind: pgCreating, Step: "nodes 1/3", Nodes: phases}, "being created", "nodes 1 of 3 ready", "worker-0", "waits for munge · 19s of 5m"},
		{"a create before its nodes", pgLife{Kind: pgCreating, Step: "pull images"}, "being created", "", "", "pull images"},
		{"a create that waits for the lock", pgLife{Kind: pgCreating, Run: pgRun{Waiting: true}}, "being created", "the create is queued", "", "waits for the lock of the realm"},
		{"a delete", pgLife{Kind: pgDeleting, Nodes: []pgPhase{{Node: "worker-0", State: runOK}, {Node: "worker-1", State: runQueued}}}, "being deleted", "1 of 2 nodes left", "", ""},
		{"a delete before its nodes", pgLife{Kind: pgDeleting}, "being deleted", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pill, words, note := pgFleetReadyOf(store.Snapshot{}, pgCluster{Life: &tc.l})
			assert.Equal(t, pgPill{tc.pill, "run"}, pill)
			assert.Equal(t, tc.words, words)
			assert.Empty(t, note)
			node, now := pgLifeNow(&tc.l)
			assert.Equal(t, tc.node, node)
			assert.Equal(t, tc.now, now)
		})
	}
}

func TestPgFleetRowOf(t *testing.T) {
	// Of the nodes that need a look, the row names three, and counts the
	// others; a node that is fine is none.
	c := pgCluster{Realm: "ci", Name: "c", State: cluster.StateMixed, Nodes: []pgNode{fleetNode("controller", config.RoleController, "slurmctld")}}
	for _, w := range []string{"worker-0", "worker-1", "worker-2", "worker-3", "worker-4"} {
		c.Nodes = append(c.Nodes, pgNode{Realm: "ci", Cluster: "c", Short: w, Role: config.RoleWorker, State: cluster.StateStopped})
	}
	r := pgFleetRowOf(store.Snapshot{}, c, false)
	require.Len(t, r.Issues, 3)
	assert.Equal(t, pgFleetIssue{Node: c.Nodes[1], Text: "stopped"}, r.Issues[0])
	assert.Equal(t, "and 2 more", r.More)
	assert.Equal(t, "c", r.Label)
	assert.Empty(t, r.Note)

	// When no node is to blame, the row says what is wrong; when one is,
	// the node says it.
	probed := store.Snapshot{Statuses: map[store.StatusKey]*cluster.Status{{Realm: "ci", Cluster: "c"}: {Name: "c"}}}
	ctl := fleetNode("controller", config.RoleController, "slurmctld")
	c = pgCluster{Realm: "ci", Name: "c", State: cluster.StateRunning, Nodes: []pgNode{ctl}}
	r = pgFleetRowOf(probed, c, false)
	assert.Empty(t, r.Issues)
	assert.Equal(t, "Slurm runs jobs on workers. Add one.", r.Note)
	c.Nodes[0] = fleetNode("controller", config.RoleController, "slurmctld", "slurmctld")
	c.Nodes = append(c.Nodes, fleetNode("worker-0", config.RoleWorker, "slurmd"))
	r = pgFleetRowOf(probed, c, false)
	assert.Len(t, r.Issues, 1)
	assert.Empty(t, r.Note)

	// A create of sind ui shows its progress, not its Stop, which the
	// task dock has.
	c = pgCluster{Realm: "ci", Name: "c", Life: &pgLife{Kind: pgCreating, Stop: &runStop{}, Nodes: []pgPhase{{Node: "worker-0", State: runRunning, Text: "starts"}}}}
	r = pgFleetRowOf(store.Snapshot{}, c, true)
	assert.Nil(t, r.Stop)
	assert.Equal(t, "c (ci)", r.Label)
	assert.Equal(t, "worker-0", r.NowNode)
	assert.Equal(t, "starts", r.Now)
	assert.Empty(t, r.Issues)
}
