// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestPgAgoAndClock(t *testing.T) {
	now := pgNow
	for ago, want := range map[time.Duration]string{
		0: "just now", 49 * time.Second: "just now", 50 * time.Second: "1 min ago", 5 * time.Minute: "5 min ago",
		59 * time.Minute: "59 min ago", time.Hour: "1 h ago", 5 * time.Hour: "5 h ago", 24 * time.Hour: "1 d ago", 80 * time.Hour: "3 d ago",
	} {
		assert.Equal(t, want, pgAgo(now.Add(-ago), now), ago.String())
	}
	assert.Equal(t, "0:00.0", pgClock(-time.Second))
	assert.Equal(t, "1:05.5", pgClock(65*time.Second+500*time.Millisecond))
	assert.Equal(t, "12:00.0", pgClock(12*time.Minute))
}

func TestPgPlural(t *testing.T) {
	assert.Equal(t, "0 clusters", pgPlural(0, "cluster", "clusters"))
	assert.Equal(t, "1 cluster", pgPlural(1, "cluster", "clusters"))
	assert.Equal(t, "2 clusters", pgPlural(2, "cluster", "clusters"))
}

func TestPgDialog(t *testing.T) {
	assert.Equal(t, "/dialogs/power", pgDialog("power"))
	assert.Equal(t, "/dialogs/power", pgDialog("power", "realm", ""))
	assert.Equal(t, "/dialogs/power?nodes=a%2Cb&realm=x+y", pgDialog("power", "realm", "x y", "nodes", "a,b", "op", "", "dangling"))
	assert.Equal(t, pgAction{Label: "L", URL: "/dialogs/a?k=v", Class: "c", Dialog: true}, pgDialogAction("L", "c", "a", "k", "v"))
}

func TestPgNames(t *testing.T) {
	assert.Equal(t, "/clusters/a/b", pgClusterURL("a", "b", ""))
	assert.Equal(t, "/clusters/a/b", pgClusterURL("a", "b", "summary"))
	assert.Equal(t, "/clusters/a/b?tab=logs", pgClusterURL("a", "b", "logs"))
	assert.Equal(t, "/nodes/a/b/worker-0", pgNodeURL("a", "b", "worker-0"))
	assert.Equal(t, "worker-0", pgShort("sind", "dev", "sind-dev-worker-0"))
	assert.Equal(t, "worker-0", pgArg("default", "worker-0"))
	assert.Equal(t, "worker-0.dev", pgArg("dev", "worker-0"))
	assert.Equal(t, "", pgClusterArg("default"))
	assert.Equal(t, " dev", pgClusterArg("dev"))
	assert.Equal(t, "sind", pgSind(""))
	assert.Equal(t, "sind", pgSind("sind"))
	assert.Equal(t, "sind --realm ci", pgSind("ci"))
	assert.True(t, pgValidTab("logs"))
	assert.False(t, pgValidTab("slurm"))
}

func TestPgStateClasses(t *testing.T) {
	for s, want := range map[cluster.State]string{
		cluster.StateRunning: "ok", cluster.StateStopped: "bad", cluster.StatePaused: "warn", cluster.StateUnknown: "warn", cluster.StateMixed: "", cluster.StateEmpty: "",
	} {
		assert.Equal(t, want, pgStateClass(s), string(s))
	}
	for s, want := range map[cluster.State]string{
		cluster.StateRunning: "ok", cluster.StatePaused: "warn", cluster.StateMixed: "warn", cluster.StateUnknown: "warn", cluster.StateStopped: "", cluster.StateEmpty: "",
	} {
		assert.Equal(t, want, pgClusterClass(s), string(s))
	}
}

func TestPgNode(t *testing.T) {
	ns := cluster.NodeSummary{Container: "sind-dev-controller-backup", Cluster: "dev", Role: config.RoleController, State: cluster.StateRunning, FQDN: "f"}
	st := &cluster.Status{Nodes: []*cluster.NodeStatus{
		nil,
		{Name: "worker-0.dev"},
		{Name: "controller-backup.dev", Managed: true, Health: &cluster.NodeHealth{
			IP: "10.0.0.2", Services: cluster.ServiceHealth{"slurmctld": true, "sshd": false, "munge": true, "odd": true},
			HA: &cluster.HAStatus{Position: "backup"},
		}},
	}}
	n := pgNodeOf("sind", ns, st)
	assert.Equal(t, "controller-backup", n.Short)
	assert.Equal(t, "controller-backup.dev", n.Arg)
	assert.Equal(t, "10.0.0.2", n.IP, "the status fills in a missing address")
	assert.True(t, n.Known)
	assert.True(t, n.Managed)
	assert.Equal(t, []pgSvc{{"munge", true}, {"sshd", false}, {"slurmctld", true}}, n.Services[1:], "in the order of the matrix")
	assert.Equal(t, 3, n.ServiceUp())
	assert.Equal(t, []string{"sshd"}, n.Down())
	assert.Equal(t, []pgPill{{"sshd down", "bad"}}, n.Issues())
	assert.True(t, n.Problem())
	assert.True(t, n.Running())
	assert.False(t, n.Worker())
	assert.Equal(t, "brand", n.RoleClass())
	assert.Equal(t, "pg-nm pg-nm-controller-backup pg-nm-running pg-nm-svcdown", n.Tile())
	up, ok := n.Service("munge")
	assert.True(t, up && ok)
	_, ok = n.Service("slurmd")
	assert.False(t, ok)
	assert.Equal(t, "/nodes/sind/dev/controller-backup", n.Link())
	assert.Equal(t, "ok", n.StateClass())

	// A node with no status, and one with a status that lacks it.
	for _, st := range []*cluster.Status{nil, {}, {Nodes: []*cluster.NodeStatus{{Name: "controller-backup.other"}}}} {
		n = pgNodeOf("sind", ns, st)
		assert.False(t, n.Known)
		assert.Empty(t, n.Services)
		assert.Empty(t, n.Issues())
		assert.False(t, n.Problem())
	}

	// A stopped node reports its state, not its services.
	stopped := pgNode{Short: "worker-0", Role: config.RoleWorker, State: cluster.StateStopped, Services: []pgSvc{{"slurmd", false}}}
	assert.Empty(t, stopped.Down())
	assert.Equal(t, []pgPill{{"stopped", "bad"}}, stopped.Issues())
	assert.Equal(t, "pg-nm pg-nm-worker pg-nm-stopped", stopped.Tile())
	assert.Equal(t, "", stopped.RoleClass())
	assert.True(t, stopped.Worker())
	paused := pgNode{Role: config.RoleWorker, State: cluster.StatePaused}
	assert.Equal(t, []pgPill{{"paused", "warn"}}, paused.Issues())
}

func TestPgCluster(t *testing.T) {
	c := pgCluster{Realm: "sind", Name: "dev", State: cluster.StateMixed, Nodes: []pgNode{
		{Short: "controller", Role: config.RoleController, State: cluster.StateRunning},
		{Short: "controller-backup", Role: config.RoleController, State: cluster.StateRunning},
		{Short: "worker-0", Role: config.RoleWorker, State: cluster.StateStopped},
		{Short: "db", Role: config.RoleDB, State: cluster.StateRunning},
	}}
	assert.Equal(t, "1 controller · 1 controller-backup · 1 db · 1 worker", c.Roles())
	c.Nodes = append(c.Nodes, pgNode{Short: "worker-1", Role: config.RoleWorker, State: cluster.StatePaused})
	assert.Equal(t, "1 controller · 1 controller-backup · 1 db · 2 workers", c.Roles())
	assert.Equal(t, "", pgCluster{}.Roles())
	assert.Equal(t, []pgPill{{"worker-0: stopped", "bad"}, {"worker-1: paused", "warn"}}, c.Problems())
	assert.Equal(t, "5 nodes", c.NodesText())
	assert.Equal(t, "warn", c.StateClass())
	assert.Equal(t, "/clusters/sind/dev", c.Link())
	assert.Equal(t, "/dialogs/delete-cluster?cluster=dev&realm=sind", c.DeleteURL())
}

func TestPgRunOf(t *testing.T) {
	now := pgNow
	end := now.Add(-time.Minute)
	for _, tc := range []struct {
		run   store.Run
		class string
		time  string
	}{
		{store.Run{ID: "a", Start: now.Add(-time.Minute)}, "run", "1:00.0"},
		{store.Run{ID: "a", End: &end, Status: store.StatusOK}, "ok", "1 min ago"},
		{store.Run{ID: "a", End: &end, Status: store.StatusCanceled}, "warn", "1 min ago"},
		{store.Run{ID: "a", End: &end, Status: store.StatusFailed}, "bad", "1 min ago"},
		{store.Run{ID: "a", End: &end}, "bad", "1 min ago"},
	} {
		r := pgRunOf(tc.run, now)
		assert.Equal(t, tc.class, r.Class)
		assert.Equal(t, tc.time, r.Time)
		assert.True(t, r.Terminal)
		assert.Equal(t, !r.Running, tc.run.Done())
		assert.Equal(t, "/runs/a", r.Link())
	}
	assert.False(t, pgRunOf(store.Run{Parent: "p"}, now).Terminal)
}

func TestPgRunsOn(t *testing.T) {
	sn := store.Snapshot{Runs: []store.Run{
		{ID: "1", Title: "create worker dev"},
		{ID: "2", Title: "create cluster dev2"},
		{ID: "3", Title: "dev"},
		{ID: "4", Title: "create cluster dev", End: &time.Time{}},
		{ID: "5", Title: "delete cluster other dev"},
	}}
	var ids []string
	for _, r := range pgRunsOn(sn, "dev", pgNow) {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"1", "5"}, ids)
}

func TestPgNodeNeed(t *testing.T) {
	sn := store.Snapshot{Clusters: map[string][]cluster.Summary{"r": {{Name: "c", State: cluster.StateMixed}}}}
	n := pgNode{Realm: "r", Cluster: "c", Short: "w", Arg: "w.c"}
	n.State = cluster.StateUnknown
	need := pgNodeNeed(sn, n)
	assert.Equal(t, "warn", need.Sev)
	assert.Equal(t, "w.c is unknown", need.Title)
	assert.Contains(t, need.Detail, "a state that sind does not know. The cluster c is mixed.")
	assert.Len(t, need.Actions, 1, "a node in an unknown state cannot be started or paused")
	// A cluster that the snapshot does not hold.
	n.Cluster = "gone"
	assert.Contains(t, pgNodeNeed(sn, n).Detail, "The cluster gone is unknown.")
}

func TestPgRealmNames(t *testing.T) {
	sn := store.Snapshot{
		Realms:   []cluster.RealmSummary{{Name: "b"}},
		Clusters: map[string][]cluster.Summary{"a": {{Name: "x"}}},
		Nodes:    map[string][]cluster.NodeSummary{"c10": {{}}, "c2": {{}}, "b": {{}}},
	}
	assert.Equal(t, []string{"a", "b", "c2", "c10"}, pgRealmNames(sn, ""))
	assert.Equal(t, []string{"zz"}, pgRealmNames(sn, "zz"))
	assert.Equal(t, "/clusters", pgRealmNext(&View{Page: "cluster"}))
	assert.Equal(t, "/clusters", pgRealmNext(&View{Page: "run"}))
	assert.Equal(t, "/nodes", pgRealmNext(&View{Page: "nodes"}))
	assert.Equal(t, "/nodes", pgRealmNext(&View{Page: "node"}))
	assert.Equal(t, "/mesh", pgRealmNext(&View{Page: "more-mesh"}))
	assert.Equal(t, "/", pgRealmNext(&View{Page: "overview"}))
}

func TestPgClusterTopics(t *testing.T) {
	p := Params{"realm": {"r"}, "cluster": {"c"}}
	has := func(tab string, topic store.Topic) bool {
		for _, x := range pgClusterTopics(p, tab) {
			if x == topic {
				return true
			}
		}
		return false
	}
	assert.True(t, has("", store.StatusTopic("r", "c")))
	assert.True(t, has("summary", store.MeshTopic("r")))
	assert.True(t, has("nodes", store.NodesTopic("r")))
	assert.False(t, has("nodes", store.NetworksTopic("r")))
	assert.True(t, has("network", store.NetworksTopic("r")))
	assert.True(t, has("network", store.VolumesTopic("r")))
	assert.True(t, has("connect", store.MonitoringTopic("r")))
	assert.False(t, has("connect", store.StatusTopic("r", "c")))
	assert.Empty(t, pgClusterTopics(p, "logs"), "a render of the logs tab would end its stream")
}

func TestPgFragmentsDependOnWhatTheyShow(t *testing.T) {
	r := pgRegistry()
	frag := func(name string) Fragment {
		f, ok := r.fragments[name]
		assert.True(t, ok, name)
		return f
	}
	p := Params{"realm": {"r"}, "cluster": {"c"}, "node": {"n"}}
	dep := func(name string, p Params, topics ...store.Topic) bool {
		return frag(name).depends(topics, p)
	}
	assert.True(t, dep("pg-overview-needs", nil, store.LockTopic("x")))
	assert.True(t, dep("pg-overview-needs", nil, store.TopicDocker))
	assert.True(t, dep("pg-overview-needs", nil, store.TopicDoctor))
	assert.True(t, dep("pg-overview-recent", nil, store.TopicRuns))
	assert.True(t, dep("pg-overview-recent", nil, store.RunTopic("a")))
	assert.False(t, dep("pg-overview-recent", nil, store.NodesTopic("x")))
	assert.True(t, dep("pg-clusters-table", nil, store.ClustersTopic("x")))
	assert.True(t, dep("pg-nodes-table", nil, store.NodesTopic("x")))
	assert.False(t, dep("pg-nodes-table", nil, store.TopicTasks))
	assert.True(t, dep("pg-cluster-head", p, store.TopicRuns))
	assert.True(t, dep("pg-cluster-head", p, store.StatusTopic("r", "c")))
	assert.False(t, dep("pg-cluster-head", p, store.StatusTopic("r", "other")))
	assert.True(t, dep("pg-cluster-body", Params{"realm": {"r"}, "cluster": {"c"}, "tab": {"nodes"}}, store.StatusTopic("r", "c")))
	assert.False(t, dep("pg-cluster-body", Params{"realm": {"r"}, "cluster": {"c"}, "tab": {"logs"}}, store.StatusTopic("r", "c")))
	assert.True(t, dep("pg-node-body", p, store.StatusTopic("r", "c")))
	assert.True(t, dep("pg-node-head", p, store.NodesTopic("r")))
	assert.False(t, dep("pg-node-head", p, store.NodesTopic("other")))
}
