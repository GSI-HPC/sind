// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
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
	assert.Equal(t, pgPill{"mixed", "warn"}, pgCluster{State: cluster.StateMixed}.Pill())
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
		{ID: "6", Title: "power on worker-0", Root: store.Span{Node: "dev"}},
	}}
	var ids []string
	for _, r := range pgRunsOn(sn, "sind", "dev", pgNow) {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"6", "5", "1"}, ids, "oldest first; the target of the command names the cluster")
	sn.Runs = sn.Runs[:5]

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
	assert.Equal(t, 1, pgCount(pgSnapNodes(sn, "", pgNow), pgNode.Problem))
	for _, n := range pgSnapNodes(sn, "sind", pgNow) {
		assert.Equal(t, n.Cluster == "nightly", n.Busy && n.Creating, n.Arg)
	}
	c := pgClusterOf(sn, "sind", cluster.Summary{Name: "nightly"}, nil, pgNow)
	assert.True(t, c.Nodes[0].Creating)
	assert.False(t, c.Nodes[0].Problem())
	assert.False(t, c.Nodes[1].Problem())
	assert.Empty(t, c.Nodes[0].Issues(), "a node that is not up yet has no fault")
	assert.Equal(t, pgPill{"creating", "run"}, c.Pill())
	var titles []string
	d := &pgOverview{}
	pgNeedsOf(d, sn, pgNow)
	for _, need := range d.Needs {
		titles = append(titles, need.Subject+" "+need.Title)
	}
	assert.Equal(t, []string{"worker-0.dev has slurmd down"}, titles)

	// A command that adds workers may start services, but a stopped node
	// is a problem all the same.
	sn.Runs = []store.Run{{ID: "2", Title: "create worker nightly", Start: pgNow}}
	assert.Equal(t, 2, pgCount(pgSnapNodes(sn, "", pgNow), pgNode.Problem))
	c = pgClusterOf(sn, "sind", cluster.Summary{Name: "nightly"}, nil, pgNow)
	assert.False(t, c.Nodes[0].Problem())
	assert.True(t, c.Nodes[1].Problem())

	// A command on a node of the cluster works on the cluster.
	sn.Runs = []store.Run{{ID: "3", Title: "power reboot worker-0.dev", Start: pgNow}}
	w := pgWorkOf(sn, "sind", "dev", pgNow)
	assert.True(t, w.Busy)
	assert.Nil(t, w.Life)
	assert.False(t, pgWorkOf(sn, "sind", "nightly", pgNow).Busy)
}

func TestPgSlurmIssue(t *testing.T) {
	n := pgNode{Short: "worker-5", Role: config.RoleWorker, State: cluster.StateRunning, Services: []pgSvc{{"slurmd", true}}}
	_, ok := n.SlurmIssue()
	assert.False(t, ok, "sinfo did not list it")
	n.Slurm = &slurm.NodeInfo{Name: "worker-5", State: "mixed"}
	assert.Empty(t, n.Issues(), "it takes jobs")
	assert.False(t, n.Problem())

	// Slurm sends it no jobs: it needs a look, unless a command works on
	// its cluster or creates it.
	for state, class := range map[string]string{"drained": "warn", "draining": "warn", "down*": "bad", "idle*": "bad", "fail": "bad"} {
		n.Slurm.State = state
		assert.Equal(t, []pgPill{{state, class}}, n.Issues(), state)
		assert.True(t, n.Problem(), state)
	}
	n.Busy = true
	assert.False(t, n.Problem(), "a command may be resuming it")
	n.Busy, n.Creating = false, true
	assert.False(t, n.Problem())
	n.Creating = false

	// A service that is down is why: the node has that issue alone.
	n.Services = []pgSvc{{"slurmd", false}}
	assert.Equal(t, []pgPill{{"slurmd down", "bad"}}, n.Issues())
	assert.Equal(t, n.Issues(), n.ServiceIssues())
	n.State = cluster.StateStopped
	assert.Equal(t, []pgPill{{"stopped", "bad"}}, n.Issues())

	// The cluster says how many of its nodes need a look, in its pill,
	// whatever its state, amber for a worker that Slurm sends no jobs.
	n.State, n.Services, n.Slurm.State = cluster.StateRunning, []pgSvc{{"slurmd", true}}, "drained"
	assert.Equal(t, "warn", n.Sev())
	assert.Equal(t, pgPill{"running · drained", "warn"}, n.HeadPill(), "the node page says why it needs a look")
	c := pgCluster{State: cluster.StateRunning, Nodes: []pgNode{n, {State: cluster.StateRunning}}}
	assert.Equal(t, 1, c.Look())
	assert.Equal(t, pgPill{"running · 1 needs a look", "warn"}, c.Pill())
	c.State = cluster.StateMixed
	assert.Equal(t, pgPill{"mixed · 1 needs a look", "warn"}, c.Pill())
	// Red once a node is stopped or has a service down.
	down := pgNode{State: cluster.StateRunning, Known: true, Services: []pgSvc{{"slurmd", false}}}
	assert.Equal(t, "bad", down.Sev())
	assert.Equal(t, pgPill{"running · slurmd down", "bad"}, down.HeadPill())
	c.Nodes = append(c.Nodes, down)
	assert.Equal(t, pgPill{"mixed · 2 need a look", "bad"}, c.Pill())
	look, class := pgLook(c.Nodes)
	assert.Equal(t, 2, look)
	assert.Equal(t, "bad", class)
	c.Nodes = c.Nodes[:2]
	c.Nodes[0].Slurm.State = "idle"
	c.State = cluster.StateRunning
	assert.Equal(t, pgPill{"running", "ok"}, c.Pill())
	assert.Empty(t, c.Nodes[0].Sev())
	assert.Equal(t, pgPill{"running · takes jobs", "ok"}, c.Nodes[0].HeadPill(), "a worker says that it takes jobs")
	c.Nodes[0].Slurm = nil
	assert.Equal(t, pgPill{"running", "ok"}, c.Nodes[0].HeadPill(), "until sinfo lists it")
	w := pgNode{Role: config.RoleWorker, State: cluster.StateRunning, Known: true, Managed: true, Services: []pgSvc{{"munge", false}, {"slurmd", false}}}
	assert.Equal(t, pgPill{"running · takes no jobs", "bad"}, w.HeadPill(), "a worker whose slurmd is down")
	// While a command works on the cluster, a drained worker, or one with a
	// service down, needs no look yet, but it does not say that it takes
	// jobs either.
	w.Busy, w.Slurm = true, &slurm.NodeInfo{State: "drained"}
	assert.Equal(t, pgPill{"running", "ok"}, w.HeadPill(), "a busy worker with slurmd down")
	w.Services = []pgSvc{{"munge", true}, {"slurmd", true}}
	assert.Equal(t, pgPill{"running", "ok"}, w.HeadPill(), "a busy worker that Slurm drained")
	w.Slurm.State = "idle"
	assert.Equal(t, pgPill{"running · takes jobs", "ok"}, w.HeadPill(), "a busy worker that takes jobs")
	assert.Equal(t, "1 node needs a look", pgLookText(1))
	assert.Equal(t, "2 nodes need a look", pgLookText(2))
	assert.Equal(t, "1 needs a look", pgLookShort(1))
	assert.Equal(t, "3 need a look", pgLookShort(3))
	assert.Equal(t, "needs a look", pgLookUnit(1))
	assert.Equal(t, "need a look", pgLookUnit(3))
}

func TestPgOverviewSummary(t *testing.T) {
	d := &pgOverview{}
	assert.Empty(t, d.Summary())
	d.Needs = []pgNeed{{Sev: "ok", Kind: pgNeedFixed}}
	assert.Empty(t, d.Summary(), "a row that is fixed is no problem")
	assert.Equal(t, "warn", d.SummaryClass())
	d.Needs = append(d.Needs, pgNeed{Sev: "warn", Kind: pgNeedLock})
	assert.Equal(t, "1 realm stays locked", d.Summary())
	d.Needs = d.Needs[:1]
	d.Needs = append(d.Needs, pgNeed{Sev: "warn", Kind: pgNeedWarning}, pgNeed{Sev: "warn", Kind: pgNeedUnknown}, pgNeed{Sev: "warn", Kind: pgNeedWarning})
	assert.Equal(t, "1 node needs a look · 2 host warnings", d.Summary())
	assert.Equal(t, "warn", d.SummaryClass())
	d.Needs = append(d.Needs, pgNeed{Sev: "bad", Kind: pgNeedService}, pgNeed{Sev: "warn", Kind: pgNeedSlurm}, pgNeed{Sev: "bad", Kind: pgNeedRun}, pgNeed{Sev: "bad", Kind: pgNeedRun})
	assert.Equal(t, "3 nodes need a look · 2 runs failed · 2 host warnings", d.Summary(), "one count of the nodes, whatever is wrong with them")
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
	assert.Equal(t, "/clusters", (&View{Page: "cluster"}).PgRealmNext())
	assert.Equal(t, "/clusters", (&View{Page: "run"}).PgRealmNext())
	assert.Equal(t, "/nodes", (&View{Page: "nodes"}).PgRealmNext())
	assert.Equal(t, "/nodes", (&View{Page: "node"}).PgRealmNext())
	assert.Equal(t, "/mesh", (&View{Page: "more-mesh"}).PgRealmNext())
	assert.Equal(t, "/", (&View{Page: "overview"}).PgRealmNext())
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
	// A run that starts, goes on or ends changes what a command creates or
	// deletes: the pill, the phase and the actions of the node and cluster
	// pages, but for the logs tab, whose stream a render would end.
	assert.True(t, dep("pg-node-head", p, store.TopicRuns))
	assert.True(t, dep("pg-node-body", p, store.TopicRuns))
	for _, tab := range []string{"", "summary", "nodes", "slurm", "network"} {
		assert.True(t, dep("pg-cluster-body", Params{"realm": {"r"}, "cluster": {"c"}, "tab": {tab}}, store.TopicRuns), tab)
	}
	assert.False(t, dep("pg-cluster-body", Params{"realm": {"r"}, "cluster": {"c"}, "tab": {"logs"}}, store.TopicRuns))
}
