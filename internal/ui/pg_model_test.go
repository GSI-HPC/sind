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
	assert.Empty(t, pgJoin(nil))
	assert.Equal(t, "a", pgJoin([]string{"a"}))
	assert.Equal(t, "a and b", pgJoin([]string{"a", "b"}))
	assert.Equal(t, "a, b and c", pgJoin([]string{"a", "b", "c"}))
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
	assert.True(t, pgValidTab("slurm"))
	assert.False(t, pgValidTab("jobs"))
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
		r := pgRunOf(store.Snapshot{}, tc.run, now)
		assert.Equal(t, tc.class, r.Class)
		assert.Equal(t, tc.time, r.Time)
		assert.True(t, r.Terminal)
		assert.Equal(t, !r.Running, tc.run.Done())
		assert.Equal(t, "/runs/a", r.Link())
	}
	r := pgRunOf(store.Snapshot{}, store.Run{Parent: "p"}, now)
	assert.False(t, r.Terminal)
	assert.Equal(t, runFromProgram, r.Source)
	r = pgRunOf(store.Snapshot{}, store.Run{Parent: "p", FromUI: true}, now)
	assert.Equal(t, runFromUI, r.Source)
	task := store.Task{Trace: "tr", Cluster: "dev"}
	r = pgRunOf(store.Snapshot{Tasks: []store.Task{task}}, store.Run{Trace: "tr", Title: "create worker"}, now)
	assert.Equal(t, runFromUI, r.Source)
	assert.Equal(t, "create worker dev", r.Title, "the cluster of the task")
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
	for _, r := range pgRunsOn(sn, "sind", "dev", pgNow) {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"5", "1"}, ids, "oldest first")

	// A run of sind ui counts in the realm of its task only.
	sn.Tasks = []store.Task{{Trace: "x", Realm: "ci"}}
	sn.Runs[0].Trace = "x"
	ids = nil
	for _, r := range pgRunsOn(sn, "sind", "dev", pgNow) {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"5"}, ids)
}

func TestPgRunBanner(t *testing.T) {
	assert.Nil(t, pgCluster{Name: "dev"}.Banner())
	one := pgCluster{Name: "dev", Runs: []pgRun{{ID: "a", Title: "create worker dev", Source: runFromTerminal}}}
	assert.Equal(t, &pgRunBanner{Command: "create worker dev", Text: "runs in a terminal.", Href: "/runs/a", Action: "Show progress"}, one.Banner())
	one.Runs[0].Source = runFromUI
	assert.Equal(t, "runs from sind ui.", one.Banner().Text)
	one.Runs[0].Waiting = true
	assert.Equal(t, "waits for the lock of the realm. It runs from sind ui.", one.Banner().Text)

	several := pgCluster{Realm: "sind", Name: "dev", Runs: []pgRun{{ID: "a"}, {ID: "b"}}}
	assert.Equal(t, &pgRunBanner{Count: "2 commands", Text: "run on dev.", Href: "/activity?cluster=dev&realm=sind&status=running", Action: "Show them"}, several.Banner())
	several.Runs[1].Waiting = true
	assert.Equal(t, "run on dev: 1 works, 1 waits for the lock of the realm.", several.Banner().Text)
	several.Runs = append(several.Runs, pgRun{ID: "c", Waiting: true})
	assert.Equal(t, "run on dev: 1 works, 2 wait for the lock of the realm.", several.Banner().Text)
}

func TestPgNodesOfABusyClusterAreNoProblem(t *testing.T) {
	node := func(c, short string, st cluster.State) cluster.NodeSummary {
		return cluster.NodeSummary{Cluster: c, Container: "sind-" + c + "-" + short, Role: config.RoleWorker, State: st}
	}
	down := func(c string) *cluster.Status {
		return &cluster.Status{Nodes: []*cluster.NodeStatus{{Name: "worker-0." + c, Health: &cluster.NodeHealth{Services: cluster.ServiceHealth{"slurmd": false}}}}}
	}
	sn := store.Snapshot{
		Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev"}, {Name: "nightly"}}},
		Nodes: map[string][]cluster.NodeSummary{"sind": {
			node("dev", "worker-0", cluster.StateRunning), node("nightly", "worker-0", cluster.StateRunning), node("nightly", "worker-1", cluster.StateStopped),
		}},
		Statuses: map[store.StatusKey]*cluster.Status{{Realm: "sind", Cluster: "dev"}: down("dev"), {Realm: "sind", Cluster: "nightly"}: down("nightly")},
		Runs:     []store.Run{{ID: "1", Title: "create cluster nightly", Start: pgNow}},
	}
	// dev's worker-0 has slurmd down; the nodes of nightly are being made.
	assert.Equal(t, 1, pgProblemNodes(sn, ""))
	for _, n := range pgSnapNodes(sn, "sind") {
		assert.Equal(t, n.Cluster == "nightly", n.Busy && n.Creating, n.Arg)
	}
	c := pgClusterOf(sn, "sind", cluster.Summary{Name: "nightly"}, nil, pgNow)
	assert.True(t, c.Nodes[0].Creating)
	assert.False(t, c.Nodes[0].Problem())
	assert.False(t, c.Nodes[1].Problem())
	assert.Equal(t, []pgPill{{"slurmd down", "bad"}}, c.Nodes[0].Issues(), "the cluster page still lists what it found")
	var titles []string
	for _, need := range pgNeeds(sn, "", pgNow) {
		titles = append(titles, need.Title)
	}
	assert.Equal(t, []string{"worker-0.dev has slurmd down"}, titles)

	// A command that adds workers may start services, but a stopped node
	// is a problem all the same.
	sn.Runs = []store.Run{{ID: "2", Title: "create worker nightly", Start: pgNow}}
	assert.Equal(t, 2, pgProblemNodes(sn, ""))
	c = pgClusterOf(sn, "sind", cluster.Summary{Name: "nightly"}, nil, pgNow)
	assert.False(t, c.Nodes[0].Problem())
	assert.True(t, c.Nodes[1].Problem())

	// A command on a node of the cluster works on the cluster.
	sn.Runs = []store.Run{{ID: "3", Title: "power reboot worker-0.dev", Start: pgNow}}
	busy, creating := pgBusy(sn, "sind", "dev")
	assert.True(t, busy)
	assert.False(t, creating)
	busy, _ = pgBusy(sn, "sind", "nightly")
	assert.False(t, busy)
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
	// A node that runs with two services down.
	n.State = cluster.StateRunning
	n.Services = []pgSvc{{"munge", false}, {"sshd", true}, {"slurmd", false}}
	need = pgNodeNeed(sn, n)
	assert.Equal(t, pgNeed{Sev: "bad", Kind: pgNeedService, Title: "w.c has munge, slurmd down", Detail: "The container runs, but munge and slurmd are not active."},
		pgNeed{Sev: need.Sev, Kind: need.Kind, Title: need.Title, Detail: need.Detail})
	assert.Equal(t, "/clusters/r/gone?node=w&tab=logs", need.Actions[0].URL)
}

func TestPgOverviewSummary(t *testing.T) {
	d := &pgOverview{}
	assert.Empty(t, d.Summary())
	d.Needs = []pgNeed{{Sev: "run", Kind: pgNeedLock}}
	assert.Empty(t, d.Summary(), "a lock is work, not a problem")
	assert.Equal(t, "warn", d.SummaryClass())
	d.Needs = append(d.Needs, pgNeed{Sev: "warn", Kind: pgNeedWarning}, pgNeed{Sev: "warn", Kind: pgNeedUnknown}, pgNeed{Sev: "warn", Kind: pgNeedWarning})
	assert.Equal(t, "1 node is in an unknown state · 2 host warnings", d.Summary())
	assert.Equal(t, "warn", d.SummaryClass())
	d.Needs = append(d.Needs, pgNeed{Sev: "bad", Kind: pgNeedService}, pgNeed{Sev: "bad", Kind: pgNeedService}, pgNeed{Sev: "bad", Kind: pgNeedRun}, pgNeed{Sev: "bad", Kind: pgNeedRun})
	assert.Equal(t, "1 node is in an unknown state · 2 nodes have a service down · 2 runs failed · 2 host warnings", d.Summary())
	assert.Equal(t, "bad", d.SummaryClass())
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
