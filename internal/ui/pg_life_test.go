// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lifeAt returns pgNow moved by d, as a pointer for an end.
func lifeAt(d time.Duration) *time.Time { t := pgNow.Add(d); return &t }

// lifeSpan returns a span that ran from start for took, or runs still when
// took is 0, or waits when it is negative.
func lifeSpan(kind, name string, start, took time.Duration, children ...store.Span) store.Span {
	s := store.Span{Kind: kind, Name: name, Queued: pgNow.Add(start), Started: pgNow.Add(start), State: activity.StateRunning, Children: children}
	switch {
	case took > 0:
		s.Ended, s.State, s.Status = lifeAt(start+took), activity.StateEnded, store.StatusOK
	case took < 0:
		s.State, s.Started = activity.StateQueued, time.Time{}
	}
	return s
}

// lifeTarget returns the target of a node, as lifeSpan.
func lifeTarget(node string, role config.Role, start, took time.Duration, children ...store.Span) store.Span {
	s := lifeSpan(activity.KindTarget, node, start, took, children...)
	s.Node, s.Role = node, string(role)
	return s
}

// lifeCreate is a run of sind ui that creates the cluster ci/nightly: the
// images are pulled, the controller is ready, worker-0 waits for munge and
// worker-1 waits for its turn.
func lifeCreate() store.Run {
	wait := lifeSpan(activity.KindWait, "ready", -19*time.Second, 0)
	wait.Message, wait.Timeout = "munge", 5*time.Minute
	return store.Run{ID: "c1", Trace: "tr-c1", Title: "create cluster nightly", Start: pgNow.Add(-time.Minute), Root: store.Span{
		Kind: "command", Name: "create cluster", Started: pgNow.Add(-time.Minute), State: activity.StateRunning, Children: []store.Span{
			lifeSpan(activity.KindStep, "pull images", -60*time.Second, 10*time.Second,
				lifeTarget("sind-node:latest", "", -60*time.Second, 10*time.Second)),
			lifeSpan(activity.KindStep, "nodes", -50*time.Second, 0,
				lifeTarget("controller", config.RoleController, -50*time.Second, 18*time.Second),
				lifeTarget("worker-0", config.RoleWorker, -40*time.Second, 0, wait),
				lifeTarget("worker-1", config.RoleWorker, 0, -1)),
			lifeSpan(activity.KindStep, "slurm", 0, -1, lifeTarget("controller", config.RoleController, 0, -1)),
		},
	}}
}

// lifeSnap returns a snapshot of the cluster ci/nightly with the runs.
func lifeSnap(runs ...store.Run) store.Snapshot {
	nodes := pgNodes("ci", "nightly", map[config.Role]int{config.RoleController: 1, config.RoleWorker: 3}, map[string]cluster.State{"worker-2": cluster.StateStopped})
	return store.Snapshot{
		Clusters: map[string][]cluster.Summary{"ci": {{Name: "nightly", State: cluster.StateRunning}}},
		Nodes:    map[string][]cluster.NodeSummary{"ci": nodes},
		Tasks:    []store.Task{{ID: "t1", Action: "create-cluster", Realm: "ci", Cluster: "nightly", Trace: "tr-c1", Start: pgNow.Add(-time.Minute), Status: store.TaskRunning}},
		Runs:     runs,
	}
}

func TestPgWorkOfACreate(t *testing.T) {
	sn := lifeSnap(lifeCreate())
	w := pgWorkOf(sn, "ci", "nightly", pgNow)
	require.NotNil(t, w.Life)
	l := w.Life
	assert.True(t, w.Busy)
	assert.Equal(t, "creating · nodes 1/3", l.Text())
	assert.Equal(t, "creates", l.Verb())
	assert.Equal(t, "create cluster nightly", l.Title())
	assert.Equal(t, "from sind ui", l.Source())
	assert.Equal(t, "nightly", l.Name)
	assert.Equal(t, "today 11:59", l.Started)
	assert.Equal(t, []pgPill{{"pull images", runOK}, {"nodes", runRunning}, {"slurm", runQueued}}, l.Steps)
	assert.Equal(t, l.Segs.Pct(), l.Pct())
	require.NotNil(t, l.Stop)
	assert.Equal(t, "Stop the create…", l.Stop.Label)
	assert.Equal(t, []pgPhase{
		{Node: "controller", Role: "controller", State: runOK, Text: "ready in 18s"},
		{Node: "worker-0", Role: "worker", State: runRunning, Text: "waits for munge · 19s of 5m"},
		{Node: "worker-1", Role: "worker", State: runQueued, Text: "queued"},
	}, l.Nodes, "the image is no node, and the queued target of a later step does not hide the phase of the node")

	// Every node of the cluster is being created, with its phase once the
	// command names it.
	c := pgClusterOf(sn, "ci", sn.Clusters["ci"][0], nil, pgNow)
	assert.Equal(t, pgPill{"creating · nodes 1/3", "run"}, c.Pill())
	assert.True(t, c.AnyLife())
	assert.False(t, c.NeedsLook())
	ctl, w0, w1, w2 := c.Nodes[0], c.Nodes[1], c.Nodes[2], c.Nodes[3]
	for _, n := range c.Nodes {
		assert.True(t, n.Creating && n.Life() && n.Busy, n.Short)
		assert.False(t, n.Problem(), n.Short)
	}
	assert.Equal(t, pgPill{"ready", "ok"}, ctl.Pill())
	assert.Equal(t, pgPill{"starting", "run"}, w0.Pill())
	assert.Equal(t, pgPill{"queued", "run nodot"}, w1.Pill())
	assert.Equal(t, pgPill{"creating", "run"}, w2.Pill())
	assert.Equal(t, "waits for its turn", w2.LifeText())
	assert.Equal(t, "pg-nm pg-nm-controller pg-nm-running", ctl.Tile(), "a node that is ready looks as it is")
	assert.Equal(t, "pg-nm pg-nm-worker pg-nm-running pg-nm-creating pg-nm-now", w0.Tile())
	assert.Equal(t, "worker-0 · worker · being created · waits for munge · 19s of 5m", w0.TileTitle())
	assert.Equal(t, "worker-0.nightly, being created", w0.TileLabel())
	run, ok := w.runOf("worker-0")
	assert.True(t, ok)
	assert.Equal(t, "c1", run.ID)
	run, ok = w.runOf("worker-2")
	assert.True(t, ok, "the run that creates the cluster creates each node")
	assert.Equal(t, "c1", run.ID)

	// The realm says what goes on in it.
	rows := pgRealmRows(sn, pgNow)
	require.Len(t, rows, 1)
	assert.Equal(t, []pgPill{{"creating nightly", "run"}}, rows[0].Lives)

	// Later, the controller is a target of the step slurm: that phase
	// counts.
	r := lifeCreate()
	r.Root.Children[2].Children[0] = lifeTarget("controller", config.RoleController, -2*time.Second, 2*time.Second)
	l = pgWorkOf(lifeSnap(r), "ci", "nightly", pgNow).Life
	assert.Equal(t, "slurm done in 2.0s", l.Nodes[0].Text)
}

func TestPgWorkOfARunThatWaits(t *testing.T) {
	waiting := func(id string, start time.Duration) store.Run {
		r := store.Run{ID: id, Title: "create cluster nightly", Start: pgNow.Add(start)}
		r.Root.Children = []store.Span{lifeSpan(activity.KindWait, runLockWait, start, 0)}
		return r
	}
	// The oldest of two that wait shows; one that works wins over both.
	w := pgWorkOf(lifeSnap(waiting("new", -time.Second), waiting("old", -time.Minute)), "ci", "nightly", pgNow)
	assert.Equal(t, "old", w.Life.Run.ID)
	assert.Equal(t, "create queued", w.Life.Text())
	assert.Nil(t, w.Life.Stop, "a terminal run has no Stop")
	works := store.Run{ID: "works", Title: "create cluster nightly", Start: pgNow}
	w = pgWorkOf(lifeSnap(works, waiting("old", -time.Minute)), "ci", "nightly", pgNow)
	assert.Equal(t, "works", w.Life.Run.ID)
	assert.Equal(t, "creating", w.Life.Text(), "between steps")
	w = pgWorkOf(lifeSnap(waiting("new", -time.Second), works), "ci", "nightly", pgNow)
	assert.Equal(t, "works", w.Life.Run.ID, "an older run that works stays")
}

func TestPgWorkOfWorkers(t *testing.T) {
	add := store.Run{ID: "a", Title: "create worker nightly", Start: pgNow, Root: store.Span{Children: []store.Span{
		lifeSpan(activity.KindStep, "nodes", -5*time.Second, 0, lifeTarget("worker-2", config.RoleWorker, -5*time.Second, 0)),
		lifeSpan(activity.KindStep, "slurm", 0, 0, lifeTarget("worker-3", config.RoleWorker, 0, 0)),
	}}}
	del := store.Run{ID: "d", Title: "delete worker worker-0.nightly worker-1.nightly", Start: pgNow, Root: store.Span{Children: []store.Span{
		lifeSpan(activity.KindStep, "nodes", -5*time.Second, 0,
			lifeTarget("worker-0", config.RoleWorker, -5*time.Second, time.Second),
			lifeTarget("worker-1", config.RoleWorker, -4*time.Second, 0)),
	}}}
	sn := lifeSnap(del, add)
	w := pgWorkOf(sn, "ci", "nightly", pgNow)
	assert.True(t, w.Busy)
	assert.Nil(t, w.Life, "the cluster stays")
	c := pgClusterOf(sn, "ci", sn.Clusters["ci"][0], nil, pgNow)
	assert.Equal(t, pgPill{"running", "ok"}, c.Pill())
	ctl, w0, w1, w2, w3 := c.Nodes[0], c.Nodes[1], c.Nodes[2], c.Nodes[3], pgNode{Short: "worker-3"}
	assert.False(t, ctl.Life())
	assert.Empty(t, ctl.LifeText())
	assert.True(t, w0.Deleting)
	assert.Equal(t, pgPill{"deleting", "run"}, w0.Pill())
	assert.Equal(t, "removed in 1.0s", w0.LifeText())
	assert.Equal(t, "pg-nm pg-nm-worker pg-nm-running pg-nm-deleting", w0.Tile(), "a node that sind removes stays dashed")
	assert.Equal(t, "worker-0.nightly, being deleted", w0.TileLabel())
	assert.Equal(t, "removing", w1.LifeText())
	assert.Equal(t, "starting the container", w2.LifeText())
	assert.True(t, w2.Creating)
	run, ok := w.runOf("worker-2")
	assert.True(t, ok)
	assert.Equal(t, "a", run.ID)
	_, ok = w.runOf("controller")
	assert.False(t, ok)
	w.apply(&w3)
	assert.Equal(t, "starting slurm", w3.LifeText())
}

func TestPgPhaseText(t *testing.T) {
	s := lifeTarget("worker-0", config.RoleWorker, 0, 0)
	s.Err = "node {}: timed out\nlast probe: munge"
	assert.Equal(t, "node worker-0: timed out", pgPhaseText(s, runFailed, "nodes", pgCreating, pgNow))
	assert.Equal(t, "stopped", pgPhaseText(s, runCanceled, "nodes", pgCreating, pgNow))
	assert.Equal(t, "ready", pgPhaseText(store.Span{}, runOK, "nodes", pgCreating, pgNow), "a span without times")
	assert.Equal(t, "removed", pgPhaseText(store.Span{}, runOK, "nodes", pgDeleting, pgNow))
	s.Children = []store.Span{
		lifeSpan(activity.KindCall, "docker exec", -time.Second, 0),
		lifeSpan(activity.KindWait, "done", -time.Minute, time.Second),
		lifeSpan(activity.KindWait, "ready", -3*time.Second, 0),
	}
	assert.Equal(t, "waits for ready · 3.0s", pgPhaseText(s, runRunning, "slurm", pgCreating, pgNow), "a wait without a message or bound")

	for state, mark := range map[string]string{runOK: "✓", runFailed: "✗", runQueued: "○", runRunning: "○", runCanceled: "–"} {
		assert.Equal(t, mark, pgPhase{State: state}.Mark(), state)
	}
	n := pgNode{Creating: true, Phase: &pgPhase{State: runFailed}}
	assert.Equal(t, pgPill{"failed", "bad"}, n.Pill())
	n.Phase.State = runCanceled
	assert.Equal(t, pgPill{"stopped", "warn"}, n.Pill())
	assert.Equal(t, "5m", pgBound(5*time.Minute))
	assert.Equal(t, "1h", pgBound(time.Hour))
	assert.Equal(t, "1m30s", pgBound(90*time.Second))
	assert.Equal(t, "a", pgOr("a", "b"))
	assert.Equal(t, "b", pgOr("", "b"))
}

func TestPgLifeOfADelete(t *testing.T) {
	del := store.Run{ID: "d", Title: "delete cluster nightly", Start: pgNow, Root: store.Span{Children: []store.Span{
		lifeSpan(activity.KindStep, "nodes", 0, 0, lifeTarget("worker-0", config.RoleWorker, 0, 0), lifeTarget("worker-9", config.RoleWorker, -time.Second, time.Second)),
	}}}
	sn := lifeSnap(del)
	c := pgClusterOf(sn, "ci", sn.Clusters["ci"][0], nil, pgNow)
	require.NotNil(t, c.Life)
	assert.Equal(t, "deleting · 1 of 2 left", c.Life.Text(), "the nodes that are left, which do not read as the progress of a create")
	assert.Equal(t, "deletes", c.Life.Verb())
	assert.Nil(t, c.Life.Stop)
	for _, n := range c.Nodes {
		assert.True(t, n.Deleting, n.Short)
		assert.Equal(t, pgPill{"deleting", "run"}, n.Pill(), n.Short)
	}
	assert.Equal(t, "removing", c.Nodes[1].LifeText())
	assert.Equal(t, "waits for its turn", c.Nodes[0].LifeText())
	assert.Equal(t, "pg-nm pg-nm-controller pg-nm-running pg-nm-deleting", c.Nodes[0].Tile())
	assert.Equal(t, "controller · controller · being deleted · waits for its turn", c.Nodes[0].TileTitle())
	waits := store.Run{ID: "q", Title: "delete cluster nightly", Start: pgNow}
	waits.Root.Children = []store.Span{lifeSpan(activity.KindWait, runLockWait, 0, 0)}
	assert.Equal(t, "delete queued", pgWorkOf(lifeSnap(waits), "ci", "nightly", pgNow).Life.Text())
	assert.Equal(t, "deleting", (&pgLife{Kind: pgDeleting, Step: "nodes 0/2"}).Text(), "before it names a node")
	assert.Empty(t, pgNode{Short: "x"}.LifeText())
}

func TestPgClusterActionsWhileACommandOwnsIt(t *testing.T) {
	labels := func(acts []pgAction) (out []string) {
		for _, a := range acts {
			out = append(out, a.Label+"|"+a.Off)
		}
		return out
	}
	d := &pgClusterView{Realm: "ci", Name: "nightly"}
	assert.Equal(t, []string{"Add workers…|", "Delete cluster…|"}, labels(pgClusterActions(d, "x")))
	d.Cluster.Life = &pgLife{Kind: pgCreating, Stop: &runStop{}}
	assert.Equal(t, []string{"Add workers…|sind creates the cluster. Add workers once it is ready."}, labels(pgClusterActions(d, "csrf")),
		"Stop the create takes the place of Delete")
	assert.Equal(t, "csrf", d.Cluster.Life.Stop.CSRF)
	d.Cluster.Life.Stop = nil
	assert.Equal(t, []string{"Add workers…|sind creates the cluster. Add workers once it is ready.",
		"Delete cluster…|sind creates the cluster in a terminal. Stop it there, or delete the cluster once the create ends."}, labels(pgClusterActions(d, "")))
	d.Cluster.Life.Kind = pgDeleting
	assert.Equal(t, []string{"Add workers…|sind deletes the cluster.", "Delete cluster…|sind deletes the cluster already."}, labels(pgClusterActions(d, "")))
}

func TestPgSummaryWhileACommandOwnsTheNodes(t *testing.T) {
	ctl := readyNode("controller", config.RoleController, "munge", "slurmctld")
	ctl.Services[1].OK = false
	ctl.Creating = true
	w := readyNode("worker-0", config.RoleWorker, "munge", "slurmd")
	w.Services[1].OK = false
	w.Deleting = true
	d := readyView(ctl, w)
	pgSummaryTab(d)
	assert.Equal(t, []pgCell{{"y", "✓", "munge: active"}, {"wait", "○", "slurmctld: starting"}, {"na", "·", "slurmd: not on this node"}}, d.Matrix[0].Cells,
		"a service that runs is active, one that does not yet is starting")
	assert.Equal(t, pgCell{"wait", "○", "slurmd: stopping"}, d.Matrix[1].Cells[2])
}

func TestPgPagesWhileACommandCreatesACluster(t *testing.T) {
	ts := pgServer(t)
	sn := lifeSnap(lifeCreate())
	ts.st.SetDocker(store.DockerState{Checked: true})
	ts.st.SetRealms([]cluster.RealmSummary{{Name: "ci", Clusters: 1}})
	ts.st.SetClusters("ci", sn.Clusters["ci"])
	ts.st.SetNodes("ci", sn.Nodes["ci"])
	ts.st.AddTask(sn.Tasks[0])
	ts.st.SetRuns(sn.Runs)

	body := pgBody(t, ts, "/clusters/ci/nightly", 200)
	assert.Contains(t, body, `<span class="pill run" data-say="nightly is creating · nodes 1/3.">creating · nodes 1/3</span>`)
	assert.Contains(t, body, `<span>started today 11:59 from sind ui</span>`)
	assert.Contains(t, body, `<button type="button" class="btn" aria-disabled="true" title="sind creates the cluster. Add workers once it is ready.">Add workers…<span class="vh"> (sind creates the cluster. Add workers once it is ready.)</span></button>`)
	assert.Contains(t, body, `aria-label="Stop create cluster nightly…">Stop the create…</summary>`)
	assert.NotContains(t, body, "Delete cluster…")
	assert.Contains(t, body, `<section class="panel pg-life" aria-label="Progress of create cluster nightly">`)
	assert.Contains(t, body, `<b>sind creates nightly</b><span class="pill run nodot">nodes 1/3</span>`)
	assert.Contains(t, body, `<li class="running">nodes</li>`)
	assert.Contains(t, body, `<li class="pg-ph running"><span class="pg-ck running" aria-hidden="true">○</span><span class="pg-ph-t"><b class="mono">worker-0</b> <span class="faint">worker</span><span class="pg-ph-d">waits for munge · 19s of 5m</span></span></li>`)
	assert.Contains(t, body, `<span class="pill run">not yet: sind creates the cluster</span>`)
	assert.Contains(t, body, `<p class="pg-wait">The commands to open a shell and submit a test job show here when the create ends ok.</p>`)
	assert.NotContains(t, body, "sbatch --chdir")
	assert.Contains(t, body, "being created or deleted</li>")
	assert.NotContains(t, body, "callout run pg-run", "the progress takes the place of the banner")

	// Slurm reports the new node down until its slurmd registers: the
	// Slurm column waits for the create, as the Services column does.
	ts.st.SetSlurm("ci", "nightly", &store.Slurm{Info: &cluster.SlurmInfo{Nodes: []slurm.NodeInfo{{Name: "worker-0", State: "down", Reason: "Not responding"}}}})
	body = pgBody(t, ts, "/clusters/ci/nightly?tab=nodes", 200)
	assert.Contains(t, body, `<td data-label="Slurm"><span class="faint">–</span></td>`)
	assert.NotContains(t, body, "Not responding")
	assert.Contains(t, body, `aria-label="Select worker-0" disabled title="sind creates this node."`)
	assert.Contains(t, body, `<span class="pill run" data-say="worker-0 is starting." data-say-key="worker-0">starting</span>`)
	assert.Contains(t, body, `<td data-label="Services"><span class="faint">waits for munge · 19s of 5m</span></td>`)
	assert.NotContains(t, body, `aria-label="Start worker-2…"`, "no power button while sind creates the node")

	body = pgBody(t, ts, "/clusters?realm=ci", 200)
	assert.Contains(t, body, `aria-label="Select nightly" disabled title="sind creates this cluster."`)
	assert.Contains(t, body, `<div class="pg-lifebar"><div class="segbar" role="progressbar" aria-label="Progress of create cluster nightly"`)
	assert.Contains(t, body, `<summary class="btn sm" aria-label="Stop create cluster nightly…">Stop…</summary>`, "Stop takes the place of Delete")
	assert.NotContains(t, body, `aria-label="Delete nightly…"`)
	assert.Contains(t, body, `>Creating <span class="n">1</span></a>`)
	assert.Contains(t, body, `>Running <span class="n">0</span></a>`, "a cluster that sind creates does not run yet")
	assert.Contains(t, body, "being created or deleted</li>", "the legend explains the dashed tiles")

	// The Slurm tab waits for the create too.
	body = pgBody(t, ts, "/clusters/ci/nightly?tab=slurm", 200)
	assert.Contains(t, body, `<b>worker-0</b></a></td><td class="c-state"><span class="pill run">starting</span></td><td data-label="Reason"><span class="faint">waits for munge · 19s of 5m</span></td>`)
	assert.NotContains(t, body, "Not responding")
	// And the Slurm column of the health matrix.
	body = pgBody(t, ts, "/clusters/ci/nightly", 200)
	assert.Contains(t, body, `<th class="pg-mx-sl">Slurm</th>`)
	assert.Contains(t, body, `<td class="pg-mx-sl"><span class="faint">–</span></td>`)
	assert.NotContains(t, body, "down*")

	body = pgBody(t, ts, "/nodes?realm=ci", 200)
	assert.Contains(t, body, `data-say="worker-1.nightly is queued."`)
	assert.Contains(t, body, `<td data-label="Issues"><span class="faint">queued</span></td>`)

	body = pgBody(t, ts, "/nodes/ci/nightly/worker-0", 200)
	assert.Contains(t, body, `<b class="mono">create cluster nightly</b> creates this node: waits for munge · 19s of 5m.</span><a class="btn sm" href="/runs/c1">Show progress</a>`)
	assert.NotContains(t, body, "Shut down…")
	assert.Contains(t, body, "sind creates this node. Its Slurm state shows once it is ready.")
	assert.Contains(t, body, `<a href="/clusters/ci/nightly">nightly</a> <span class="pill run">creating · nodes 1/3</span>`, "the cluster's pill, as its page says it")
	assert.NotContains(t, body, "Not responding")
	ts.st.SetStatus("ci", "nightly", &cluster.Status{Name: "nightly", Nodes: []*cluster.NodeStatus{{
		Name: "worker-0.nightly", Role: config.RoleWorker, Managed: true,
		Health: &cluster.NodeHealth{State: cluster.StateRunning, Services: cluster.ServiceHealth{pgProbe("munge"): true, pgProbe("slurmd"): false}},
	}}})
	body = pgBody(t, ts, "/nodes/ci/nightly/worker-0", 200)
	assert.Contains(t, body, `<span data-word="munge">munge</span></span><span class="pill ok">active</span>`)
	assert.Contains(t, body, `<span data-word="slurmd">slurmd</span></span><span class="pill run">starting</span><a class="btn sm ghost"`, "a service of a node sind creates is not down")

	ts.st.SetLock("ci", &state.LockHolder{Command: "sind create cluster nightly", PID: 9})
	body = pgBody(t, ts, "/?realm=", 200)
	assert.Contains(t, body, `<span class="pg-frm">1 cluster · 4 nodes</span><span class="pill run">creating nightly</span><a class="pg-frl" href="/mesh?realm=ci">`)
	assert.NotContains(t, body, "a command runs", "the health says it already")
	// The row of the cluster says how far the create got, not the faults of
	// what is not up yet, and offers its progress, as the dock has Stop.
	assert.Contains(t, body, `<td class="c-ready"><span class="pill run">being created</span><span class="pg-fsub">nodes 1 of 3 ready</span></td>`)
	assert.Contains(t, body, `<td class="c-issues"><div class="pg-fnow"><div class="pg-lifebar"><div class="segbar" role="progressbar" aria-label="Progress of create cluster nightly"`)
	assert.Contains(t, body, `<span class="pg-fis"><b class="mono">worker-0</b> waits for munge · 19s of 5m</span></div></td>`)
	assert.Contains(t, body, `<td class="act c-act"><a class="btn sm" href="/runs/c1" aria-label="Show progress: create cluster nightly">Show progress</a></td>`)
	assert.Contains(t, body, ">Creating <span class=\"n\">1</span></a>")
	assert.Contains(t, body, "being created or deleted</li>", "the legend explains the dashed tiles")
}

func TestPgPagesWhileACommandDeletesANode(t *testing.T) {
	ts := pgServer(t)
	sn := lifeSnap(store.Run{ID: "d", Title: "delete worker worker-0.nightly", Start: pgNow, Root: store.Span{Children: []store.Span{
		lifeSpan(activity.KindStep, "nodes", 0, 0, lifeTarget("worker-0", config.RoleWorker, 0, 0)),
	}}})
	ts.st.SetClusters("ci", sn.Clusters["ci"])
	ts.st.SetNodes("ci", sn.Nodes["ci"])
	ts.st.SetRuns(sn.Runs)
	body := pgBody(t, ts, "/nodes/ci/nightly/worker-0", 200)
	assert.Contains(t, body, `<b class="mono">delete worker worker-0.nightly</b> deletes this node: removing.</span>`)
	assert.Contains(t, body, `<span class="pill run" data-say="worker-0.nightly is deleting.">deleting</span>`)
	assert.Contains(t, body, `<span class="muted">sind deletes this node.</span>`, "the Slurm panel")
	body = pgBody(t, ts, "/clusters/ci/nightly?tab=nodes", 200)
	assert.Contains(t, body, `aria-label="Select worker-0" disabled title="sind deletes this node."`)
}
