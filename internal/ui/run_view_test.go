// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTimeline(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", -1)[0]
	tl := runTimelineOf(runCtxOf(run, runAt(60)))
	require.Len(t, tl.Rows, 4, "the visible steps; preflight is hidden")
	assert.Equal(t, []string{"pull images", "nodes", "mesh registration", "slurm"},
		[]string{tl.Rows[0].Label, tl.Rows[1].Label, tl.Rows[2].Label, tl.Rows[3].Label})

	// The run took 53.6 s: ticks every 10 s, the last at the end of the scale.
	labels := []string{}
	for _, tk := range tl.Ticks {
		labels = append(labels, tk.Label)
	}
	assert.Equal(t, []string{"0s", "10s", "30s"}[:1], labels[:1])
	assert.Equal(t, 0, tl.Ticks[0].Pos)
	assert.EqualValues(t, 10_000, tl.Ticks[1].At)
	assert.Zero(t, tl.Since, "a run that ended stands still")

	pull := tl.Rows[0]
	assert.True(t, pull.Bar)
	assert.Equal(t, 0, pull.Left)
	assert.Equal(t, runOK, pull.Class)
	assert.Equal(t, 1, pull.Total)
	assert.Equal(t, "6.6s · 1 ok", pull.Result)
	require.Len(t, pull.Children, 1)
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:latest", pull.Children[0].Name)
	assert.Equal(t, "6.6s", pull.Children[0].Result)

	nodes := tl.Rows[1]
	assert.Equal(t, 13, nodes.Left, "the step starts 6.73 s into 53.6 s")
	assert.True(t, nodes.Ended)
	assert.Greater(t, nodes.To, nodes.From)
	require.Len(t, nodes.Children, 3)
	assert.Equal(t, []string{"controller", "worker-0", "worker-1"}, []string{nodes.Children[0].Name, nodes.Children[1].Name, nodes.Children[2].Name})
	assert.Equal(t, "32s · 3 ok", nodes.Result)
	assert.Empty(t, tl.Rows[2].Children)
	assert.Equal(t, "1.2s", tl.Rows[2].Result, "a step without targets")

	// A failed run: the failed and canceled counts show.
	failed := runTimelineOf(runCtxOf(runFixture(t, "create-failed.jsonl", -1)[0], runAt(400)))
	assert.Equal(t, "5m07s · 2 ok · 2 failed · 1 canceled", failed.Rows[1].Result)
	assert.Equal(t, runFailed, failed.Rows[1].Class)
	assert.Equal(t, "✗", failed.Rows[1].Mark)
}

func TestRunTimelineMidRun(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", 59)[0]
	tl := runTimelineOf(runCtxOf(run, runAt(25)))
	nodes := tl.Rows[1]
	assert.Equal(t, runRunning, nodes.Class)
	assert.Equal(t, "0/3 · 18s", nodes.Result)
	assert.Equal(t, 100, nodes.Left+nodes.Width, "a bar that runs reaches the end of the scale")
	assert.False(t, nodes.Ended, "run.js ends the bar of a step that runs at the clock")
	assert.Positive(t, tl.Since, "the clock of a run that goes on")
	assert.Equal(t, "ready munge", nodes.Children[0].Result)
	assert.Equal(t, "ready sshd", nodes.Children[1].Result)
	// The scale grows with the clock.
	later := runTimelineOf(runCtxOf(run, runAt(125)))
	assert.Less(t, later.Rows[1].Left, nodes.Left)
	assert.Equal(t, "2m00s", later.Ticks[len(later.Ticks)-1].Label)
}

func TestRunTimelineEdges(t *testing.T) {
	// A run without any time: a scale of eight seconds, and no bars.
	empty := store.Run{Root: runSp("command", "x", runWith(func(s *store.Span) { s.Queued = time.Time{} }),
		runKids(runSp("step", "a", runStartedAt(1))))}
	empty.Root.Children[0].Started = time.Time{}
	tl := runTimelineOf(runCtxOf(empty, runAt(5)))
	require.Len(t, tl.Rows, 1)
	assert.False(t, tl.Rows[0].Bar)
	assert.Len(t, tl.Ticks, 9, "0 s to 8 s, a tick every second")
	var major []string
	for _, tk := range tl.Ticks {
		if !tk.Minor {
			major = append(major, tk.Label)
		}
	}
	assert.Equal(t, []string{"0s", "4s", "8s"}, major, "a narrow screen shows the first, the middle and the last label")
	later := runTimelineOf(runCtxOf(runOf(0, "", runSp("step", "a", runEndedAt(0, 100, "ok"))), runAt(100)))
	require.Len(t, later.Ticks, 4, "0 s to 1m30s")
	assert.Equal(t, []bool{false, true, true, false}, []bool{later.Ticks[0].Minor, later.Ticks[1].Minor, later.Ticks[2].Minor, later.Ticks[3].Minor}, "an even number has no middle label")

	// A queued span has no bar; a queued span in a finished run is skipped.
	run := runOf(9, "failed", runSp("step", "never"), runSp("wait", "realm lock", runEndedAt(0, 1, "ok")),
		runSp("step", "early", runEndedAt(-3, 2, "ok")))
	tl = runTimelineOf(runCtxOf(run, runAt(20)))
	assert.False(t, tl.Rows[0].Bar)
	assert.Equal(t, runSkipped, tl.Rows[0].Class)
	assert.Equal(t, "skipped", tl.Rows[0].Result)
	queued := runTimelineOf(runCtxOf(runOf(0, "", runSp("step", "s", runStartedAt(0), runKids(runSp("target", "q"), runSp("target", "r", runStartedAt(1))))), runAt(5)))
	assert.Equal(t, "queued", queued.Rows[0].Children[0].Result)
	assert.Equal(t, "4.0s", queued.Rows[0].Children[1].Result)
	assert.Equal(t, "1.0s", tl.Rows[1].Result, "a wait of the command")
	assert.Equal(t, 0, tl.Rows[2].Left, "a bar that starts before the run starts at the left edge")

	// Batches nest, and only down to three levels.
	deep := runOf(9, "ok", runSp("step", "s", runEndedAt(0, 9, "ok"), runKids(
		runSp("batch", "b", runEndedAt(0, 9, "ok"), runWith(func(s *store.Span) { s.Batch = "1/2" }), runKids(
			runSp("target", "t", runEndedAt(0, 9, "ok"), runKids(runSp("target", "too deep", runEndedAt(0, 9, "ok")))),
			runSp("call", "c", runEndedAt(0, 9, "ok")),
		)),
		runSp("batch", "plain", runStartedAt(1)),
		runSp("call", "x", runStartedAt(1)),
	)))
	tl = runTimelineOf(runCtxOf(deep, runAt(20)))
	step := tl.Rows[0]
	require.Len(t, step.Children, 2)
	assert.Equal(t, "b 1/2", step.Children[0].Name)
	assert.Equal(t, "plain", step.Children[1].Name)
	require.Len(t, step.Children[0].Children, 1)
	assert.Equal(t, "t", step.Children[0].Children[0].Name)
	assert.Empty(t, step.Children[0].Children[0].Children, "the third level has no rows below it")
	assert.Equal(t, "9.0s", step.Children[0].Result)
	assert.Equal(t, "19s", step.Children[1].Result, "a batch that goes on")

	// A run of days: a few ticks only.
	long := runOf(0, "")
	long.Start = runAt(-30 * 86400)
	tl = runTimelineOf(runCtxOf(long, runAt(0)))
	assert.LessOrEqual(t, len(tl.Ticks), runMaxTicks+1)
	// One of 20 s picks 5 s.
	assert.Equal(t, "5s", runTimelineOf(runCtxOf(runOf(20, "ok"), runAt(30))).Ticks[1].Label)
}

func TestRunTileClass(t *testing.T) {
	for name, tc := range map[string]struct {
		classes []string
		want    string
	}{
		"failed beats all": {[]string{runOK, runFailed, runRunning, runCanceled}, runFailed},
		"canceled":         {[]string{runOK, runCanceled, runRunning}, runCanceled},
		"running":          {[]string{runOK, runRunning, runQueued}, runRunning},
		"some to come":     {[]string{runOK, runQueued}, runRunning},
		"queued":           {[]string{runQueued, runQueued}, runQueued},
		"skipped":          {[]string{runSkipped}, runSkipped},
		"ok":               {[]string{runOK, runEnded, runSkipped}, runOK},
	} {
		assert.Equal(t, tc.want, runTileClass(tc.classes), name)
	}
}

func TestRunNodesOK(t *testing.T) {
	n := runNodesOf(runCtxOf(runFixture(t, "create-ok.jsonl", -1)[0], runAt(60)))
	require.NotNil(t, n)
	require.Len(t, n.Tiles, 3, "the image is a transfer, not a node; the nodes of two steps are one tile")
	c := n.Tiles[0]
	assert.Equal(t, "controller", c.Name)
	assert.Equal(t, "role-controller", c.RoleClass)
	assert.Equal(t, runOK, c.Class)
	assert.Equal(t, "done", c.Msg)
	assert.Nil(t, c.Ring)
	var labels []string
	for _, p := range c.Pipe {
		labels = append(labels, p.Label)
	}
	assert.Equal(t, []string{"ready", "setup", "ready"}, labels, "the nodes step, then the slurm step")
	assert.Equal(t, "nodes · ready munge: ok", c.Pipe[0].Title)
	assert.Equal(t, "worker", n.Tiles[1].Role)
}

func TestRunNodesFailed(t *testing.T) {
	n := runNodesOf(runCtxOf(runFixture(t, "create-failed.jsonl", -1)[0], runAt(400)))
	require.Len(t, n.Tiles, 5)
	w1 := n.Tiles[2]
	assert.Equal(t, "worker-1", w1.Name)
	assert.Equal(t, runFailed, w1.Class)
	assert.Equal(t, "node worker-1: timed out; last probe error: probe munge: munge not ready", w1.Msg)
	require.NotNil(t, w1.Ring, "a wait that timed out shows a full ring")
	assert.Equal(t, "69.1 69.1", w1.Ring.Dash)
	assert.Equal(t, "bad", w1.Ring.Class)
	assert.Equal(t, "5m02s of 5m00s", w1.Ring.Title)
	w3 := n.Tiles[4]
	assert.Equal(t, runCanceled, w3.Class)
	assert.Equal(t, "canceled", w3.Msg)
	assert.Nil(t, w3.Ring, "a wait that was canceled shows none")
}

func TestRunNodesMidRun(t *testing.T) {
	run := runFixture(t, "create-ok.jsonl", 59)[0]
	n := runNodesOf(runCtxOf(run, runAt(25)))
	require.Len(t, n.Tiles, 3)
	c := n.Tiles[0]
	assert.Equal(t, runRunning, c.Class)
	assert.Equal(t, "ready munge · 15s of 5m00s", c.Msg)
	require.NotNil(t, c.Ring)
	assert.Equal(t, "run", c.Ring.Class)
	assert.Equal(t, "3.5 69.1", c.Ring.Dash)

	// The ring turns amber at half of the limit and red at four fifths.
	for now, want := range map[float64]string{160: "warn", 280: "bad"} {
		got := runNodesOf(runCtxOf(run, runAt(now))).Tiles[0].Ring
		require.NotNil(t, got)
		assert.Equal(t, want, got.Class, "at %v s", now)
	}
	// Nothing runs once the run is over.
	over := run
	e := runAt(30)
	over.End = &e
	assert.Nil(t, runNodesOf(runCtxOf(over, runAt(300))).Tiles[0].Ring)
}

func TestRunNodesEdges(t *testing.T) {
	// No targets, or only transfers: no tiles.
	assert.Nil(t, runNodesOf(runCtxOf(runOf(0, ""), runAt(5))))
	img := runSp("target", "img", runStartedAt(0), runWith(func(s *store.Span) { s.Unit = "items" }))
	img2 := runSp("target", "img2", runStartedAt(0), runWith(func(s *store.Span) { s.Size = 5 }))
	assert.Nil(t, runNodesOf(runCtxOf(runOf(0, "", runSp("step", "pull", runStartedAt(0), runKids(img, img2))), runAt(5))))

	run := runOf(0, "", runSp("step", "nodes", runStartedAt(0), runKids(
		runSp("target", "queued", runWith(func(s *store.Span) { s.Role = "Weird Role!" })),
		runSp("target", "norole", runStartedAt(0)),
		runSp("target", "plain", runStartedAt(0), runWith(func(s *store.Span) { s.Node = "n-1" }), runKids(runSp("call", "x", runStartedAt(0)))),
		runSp("batch", "b", runStartedAt(0), runKids(runSp("target", "inbatch", runEndedAt(0, 1, "skipped")))),
		runSp("target", "limitless", runStartedAt(0), runKids(
			runSp("wait", "nolimit", runStartedAt(0)),
			runSp("wait", "ready", runStartedAt(0), runWith(func(s *store.Span) { s.Timeout = 10 * time.Second })),
			runSp("wait", "failed", runEndedAt(0, 1, "failed"), runWith(func(s *store.Span) { s.Timeout = 10 * time.Second; s.Class = "target" })),
			runSp("wait", "faster", runStartedAt(8), runWith(func(s *store.Span) { s.Timeout = 10 * time.Second })),
		)),
	)))
	n := runNodesOf(runCtxOf(run, runAt(9)))
	require.Len(t, n.Tiles, 5)
	q, nr, pl, ib, lim := n.Tiles[0], n.Tiles[1], n.Tiles[2], n.Tiles[3], n.Tiles[4]
	assert.Equal(t, "role-weirdrole", q.RoleClass)
	assert.Equal(t, "queued", q.Msg)
	assert.Equal(t, runQueued, q.Class)
	assert.Equal(t, []runPipe{{Class: runQueued, Label: "nodes", Title: "nodes: queued"}}, q.Pipe)
	assert.Empty(t, nr.RoleClass)
	assert.Equal(t, "running", nr.Msg, "a running node with nothing to say")
	assert.Equal(t, "n-1", pl.Name)
	assert.Equal(t, "x", pl.Msg)
	assert.Equal(t, runSkipped, ib.Class)
	assert.Equal(t, "did not run", ib.Msg)
	require.NotNil(t, lim.Ring)
	assert.Equal(t, "bad", lim.Ring.Class, "the wait that has used the most")
	assert.Equal(t, "9.0s of 10s", lim.Ring.Title)
	assert.Equal(t, "nolimit · 9.0s of 10s", lim.Msg)

	// A node that finished one step and is in the next one is running; the
	// target that goes on says what it does.
	next := runOf(0, "",
		runSp("step", "nodes", runEndedAt(0, 2, "ok"), runKids(runSp("target", "n", runEndedAt(0, 2, "ok")))),
		runSp("step", "slurm", runStartedAt(2), runKids(runSp("target", "n", runStartedAt(2), runKids(runSp("call", "docker exec", runStartedAt(2)))))))
	tile := runNodesOf(runCtxOf(next, runAt(3))).Tiles[0]
	assert.Equal(t, runRunning, tile.Class)
	assert.Equal(t, "docker exec", tile.Msg)
	assert.Len(t, tile.Pipe, 2)

	// A failed target without an error text.
	bad := runOf(0, "", runSp("step", "nodes", runStartedAt(0), runKids(runSp("target", "t", runEndedAt(0, 1, "failed")))))
	assert.Equal(t, "failed", runNodesOf(runCtxOf(bad, runAt(2))).Tiles[0].Msg)
	// A run that ended with a node that was running, and a long first line.
	long := runOf(5, "canceled", runSp("step", "nodes", runStartedAt(0), runKids(runSp("target", "t", runEndedAt(0, 1, "failed"),
		runWith(func(s *store.Span) {
			s.Err = "first line that is far too long " + string(make([]rune, 0)) + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\nsecond"
		})))))
	msg := runNodesOf(runCtxOf(long, runAt(6))).Tiles[0].Msg
	assert.Len(t, []rune(msg), 120)
	assert.True(t, len(msg) > 0 && msg[len(msg)-3:] == "…")
}

func TestRunRoleClass(t *testing.T) {
	assert.Equal(t, "role-controller-backup", runRoleClass("Controller-Backup"))
	assert.Equal(t, "", runRoleClass(""))
	assert.Equal(t, "", runRoleClass("!!"))
}

func TestRunShorten(t *testing.T) {
	assert.Equal(t, "abc", runShorten("abc\ndef", 10))
	assert.Equal(t, "ab…", runShorten("abcdef", 3))
	assert.Equal(t, "äö", runShorten("äö", 2))
}

func TestRunMoreTransfers(t *testing.T) {
	m := runMoreOf(runCtxOf(runFixture(t, "create-ok.jsonl", 15)[0], runAt(3)))
	require.NotNil(t, m)
	require.Len(t, m.Transfers, 1)
	tr := m.Transfers[0]
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:latest", tr.Name)
	assert.Equal(t, runRunning, tr.Class)
	assert.Equal(t, "38 MB / 101 MB · 14 MB/s", tr.Amount, "with the rate of the bytes over the last seconds")
	assert.True(t, tr.Bar)
	assert.Equal(t, 38, tr.Pct)
	assert.Empty(t, m.Steps, "the pull step has the transfer")

	done := runMoreOf(runCtxOf(runFixture(t, "create-ok.jsonl", -1)[0], runAt(60)))
	assert.Equal(t, 100, done.Transfers[0].Pct)
	assert.Equal(t, runOK, done.Transfers[0].Class)
	require.Len(t, done.Steps, 1)
	assert.Equal(t, runOtherStep{Name: "mesh registration", Class: runOK, PillClass: "ok", Text: "1.2s"}, done.Steps[0])

	// A transfer without a size has an amount and no bar; its message shows.
	odd := runOf(0, "", runSp("step", "x", runStartedAt(0), runKids(
		runSp("call", "dl", runStartedAt(0), runWith(func(s *store.Span) { s.Unit, s.Amount, s.Message = "bytes", 2000, "fetching" })),
		runSp("call", "nothing", runStartedAt(0), runWith(func(s *store.Span) { s.Unit = "bytes" })),
	)))
	m = runMoreOf(runCtxOf(odd, runAt(1)))
	require.Len(t, m.Transfers, 1)
	assert.False(t, m.Transfers[0].Bar)
	assert.Equal(t, "2.0 kB", m.Transfers[0].Amount)
	assert.Equal(t, "fetching", m.Transfers[0].Msg)

	// A target that works in a unit is a transfer before it knows its
	// size, and no node: it waits, or ended with nothing to tell.
	pulls := runOf(0, "", runSp("step", "pull images", runStartedAt(0), runKids(
		runSp("target", "img:1", runStartedAt(0), runWith(func(s *store.Span) { s.Unit = "bytes" })),
		runSp("target", "img:2", runEndedAt(0, 1, store.StatusOK), runWith(func(s *store.Span) { s.Unit = "bytes" })),
	)))
	m = runMoreOf(runCtxOf(pulls, runAt(2)))
	require.Len(t, m.Transfers, 2)
	assert.Equal(t, "waiting", m.Transfers[0].Amount)
	assert.False(t, m.Transfers[0].Bar)
	assert.Empty(t, m.Transfers[1].Amount)
	assert.Nil(t, runNodesOf(runCtxOf(pulls, runAt(2))))
}

func TestRunMoreSteps(t *testing.T) {
	assert.Nil(t, runMoreOf(runCtxOf(runOf(0, ""), runAt(1))))
	run := runOf(0, "",
		runSp("step", "failing", runEndedAt(0, 1, "failed"), runWith(func(s *store.Span) { s.Err = "boom\nmore" })),
		runSp("step", "going", runStartedAt(0), runKids(runSp("call", "docker exec", runStartedAt(0)))),
		runSp("step", "noted", runEndedAt(0, 1, "ok"), runWith(func(s *store.Span) { s.Message = "3 registered" })),
		runSp("step", "later"),
		runSp("step", "hidden", runWith(func(s *store.Span) { s.Hidden = true })),
		runSp("step", "withtargets", runStartedAt(0), runKids(runSp("target", "t", runStartedAt(0)))),
		runSp("wait", "realm lock", runStartedAt(0)),
	)
	m := runMoreOf(runCtxOf(run, runAt(5)))
	require.NotNil(t, m)
	assert.Equal(t, []runOtherStep{
		{"failing", runFailed, "bad", "boom"},
		{"going", runRunning, "run", "docker exec"},
		{"noted", runOK, "ok", "3 registered"},
		{"later", runQueued, "", ""},
	}, m.Steps)
	over := runOf(9, "canceled", runSp("step", "never"), runSp("step", "was", runStartedAt(0)))
	m = runMoreOf(runCtxOf(over, runAt(10)))
	assert.Equal(t, []runOtherStep{{"never", runSkipped, "", ""}, {"was", runCanceled, "warn", "10s"}}, m.Steps)
}

func TestRunFacts(t *testing.T) {
	run := runFixture(t, "create-failed.jsonl", -1)[0]
	f := runFactsOf(runCtxOf(run, runAt(60)))
	facts := map[string]string{}
	for _, x := range f.Facts {
		facts[x.Key] = x.Val
	}
	assert.Equal(t, "5d3c0a17b94e82f6", facts["Run"])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", facts["Trace"])
	assert.Equal(t, "00f067aa0ba902b7", facts["Caller span"])
	assert.Equal(t, "sind v0.11.0", facts["Program"])
	assert.Equal(t, "failed", facts["Status"])
	assert.Equal(t, "timeout", facts["Class"])
	assert.Contains(t, facts["Error"], "worker-1: timed out")
	assert.Contains(t, facts["Started"], "2026-10-14")
	assert.Contains(t, facts["Ended"], "2026-10-14")
	assert.Equal(t, "37 below the command, 21 of them hidden", facts["Spans"])
	assert.Equal(t, "0, 0 not sent to the sinks", facts["Line events"])
	assert.NotContains(t, facts, "Exit code")

	// A run that goes on has no end; the exit code and the line counts show.
	live := runOf(0, "", runSp("step", "s", runStartedAt(0), runWith(func(s *store.Span) { s.Lines, s.Dropped = 4, 1 }), runKids(
		runSp("call", "c", runStartedAt(0), runWith(func(s *store.Span) { s.Lines, s.Dropped = 2, 0 })))))
	live.Root.Exit = new(int)
	live.Root.Lines, live.Root.Dropped = 1, 3
	facts = map[string]string{}
	for _, x := range runFactsOf(runCtxOf(live, runAt(1))).Facts {
		facts[x.Key] = x.Val
	}
	assert.NotContains(t, facts, "Ended")
	assert.Equal(t, "0", facts["Exit code"])
	assert.Equal(t, "7, 4 not sent to the sinks", facts["Line events"])
}

func TestRunTitleNamesTheClusterOfTheTask(t *testing.T) {
	task := &store.Task{Cluster: "dev"}
	assert.Equal(t, "create cluster dev", runTitle(store.Run{Title: "create cluster"}, task))
	assert.Equal(t, "create cluster dev", runTitle(store.Run{Title: "create cluster dev"}, task))
	assert.Equal(t, "delete realm", runTitle(store.Run{Title: "delete realm"}, &store.Task{}))
	assert.Empty(t, runTitle(store.Run{}, task))
}
