// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runAt returns the time sec seconds after the start of the recorded runs.
func runAt(sec float64) time.Time {
	return time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC).Add(time.Duration(sec * float64(time.Second)))
}

// span options, for building spans by hand.
type runSpanOpt func(*store.Span)

func runSp(kind, name string, opts ...runSpanOpt) store.Span {
	s := store.Span{ID: kind + "-" + name, Kind: kind, Name: name, State: activity.StateQueued, Queued: runAt(0)}
	for _, o := range opts {
		o(&s)
	}
	return s
}

// runStartedAt makes the span run from sec.
func runStartedAt(sec float64) runSpanOpt {
	return func(s *store.Span) { s.State, s.Started = activity.StateRunning, runAt(sec) }
}

// runEndedAt makes the span end with status at sec; it started at start unless it
// has started already.
func runEndedAt(start, end float64, status string) runSpanOpt {
	return func(s *store.Span) {
		e := runAt(end)
		s.State, s.Started, s.Ended, s.Status = activity.StateEnded, runAt(start), &e, status
	}
}

func runKids(c ...store.Span) runSpanOpt     { return func(s *store.Span) { s.Children = c } }
func runWith(f func(*store.Span)) runSpanOpt { return runSpanOpt(f) }

// runOf wraps spans in a run of the command "create cluster dev" that began
// at 0 and goes on, or ended at end > 0 with status.
func runOf(end float64, status string, children ...store.Span) store.Run {
	root := runSp(activity.KindCommand, "create cluster", runStartedAt(0), runKids(children...))
	run := store.Run{ID: "r1", Trace: "tr1", Title: "create cluster dev", Start: runAt(0), Last: runAt(max(end, 1)), Root: root}
	if end > 0 {
		e := runAt(end)
		run.End, run.Status = &e, status
		run.Root.State, run.Root.Ended, run.Root.Status = activity.StateEnded, &e, status
	}
	return run
}

func TestRunDur(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second:                          "0.0s",
		0:                                     "0.0s",
		340 * time.Millisecond:                "0.3s",
		9999 * time.Millisecond:               "9.9s",
		10 * time.Second:                      "10s",
		59*time.Second + 900*time.Millisecond: "59s",
		time.Minute:                           "1m00s",
		5*time.Minute + 7*time.Second:         "5m07s",
		time.Hour:                             "1h00m",
		2*time.Hour + 3*time.Minute + 59*time.Second: "2h03m",
	} {
		assert.Equal(t, want, runDur(d), d.String())
	}
}

func TestRunPct(t *testing.T) {
	assert.Equal(t, 0, runPct(5, 0))
	assert.Equal(t, 0, runPct(-1, 10))
	assert.Equal(t, 50, runPct(5, 10))
	assert.Equal(t, 33, runPct(1, 3))
	assert.Equal(t, 100, runPct(20, 10))
}

func TestRunBytes(t *testing.T) {
	for n, want := range map[int64]string{
		-5: "0 B", 999: "999 B", 1000: "1.0 kB", 12_345: "12 kB", 100_663_296: "101 MB",
		4_194_304: "4.2 MB", 3_000_000_000: "3.0 GB", 5_000_000_000_000: "5000 GB",
	} {
		assert.Equal(t, want, runBytes(n), want)
	}
}

func TestRunAmount(t *testing.T) {
	for name, tc := range map[string]struct {
		s    store.Span
		want string
	}{
		"no unit":         {store.Span{Amount: 3, Size: 7}, ""},
		"no work":         {store.Span{Unit: "bytes"}, ""},
		"bytes":           {store.Span{Unit: "bytes", Amount: 4_194_304, Size: 100_663_296}, "4.2 MB / 101 MB"},
		"bytes no size":   {store.Span{Unit: "bytes", Amount: 2000}, "2.0 kB"},
		"percent":         {store.Span{Unit: "percent", Amount: 42, Size: 100}, "42 %"},
		"percent of 200":  {store.Span{Unit: "percent", Amount: 50, Size: 200}, "25 %"},
		"percent no size": {store.Span{Unit: "percent", Amount: 17}, "17 %"},
		"items":           {store.Span{Unit: "items", Amount: 3, Size: 7}, "3 / 7 items"},
		"items no size":   {store.Span{Unit: "layers", Amount: 3}, "3 layers"},
	} {
		assert.Equal(t, tc.want, runAmount(tc.s), name)
	}
}

func TestRunSpanPct(t *testing.T) {
	_, ok := runSpanPct(store.Span{Amount: 5})
	assert.False(t, ok)
	pct, ok := runSpanPct(store.Span{Amount: 5, Size: 20})
	assert.True(t, ok)
	assert.Equal(t, 25, pct)
	pct, ok = runSpanPct(runSp("target", "x", runEndedAt(0, 1, store.StatusOK), runWith(func(s *store.Span) { s.Size = 20 })))
	assert.True(t, ok)
	assert.Equal(t, 100, pct)
	pct, _ = runSpanPct(runSp("target", "x", runEndedAt(0, 1, store.StatusFailed), runWith(func(s *store.Span) { s.Size, s.Amount = 20, 5 })))
	assert.Equal(t, 25, pct)
}

func TestRunSpanClass(t *testing.T) {
	for name, tc := range map[string]struct {
		s       store.Span
		runDone bool
		want    string
	}{
		"ok":                {runSp("step", "a", runEndedAt(0, 1, "ok")), false, runOK},
		"failed":            {runSp("step", "a", runEndedAt(0, 1, "failed")), false, runFailed},
		"canceled":          {runSp("step", "a", runEndedAt(0, 1, "canceled")), true, runCanceled},
		"skipped":           {runSp("step", "a", runEndedAt(0, 1, "skipped")), false, runSkipped},
		"unknown status":    {runSp("step", "a", runEndedAt(0, 1, "")), false, runEnded},
		"ended state only":  {store.Span{State: activity.StateEnded, Status: "ok"}, false, runOK},
		"queued":            {runSp("step", "a"), false, runQueued},
		"queued, run over":  {runSp("step", "a"), true, runSkipped},
		"running":           {runSp("step", "a", runStartedAt(1)), false, runRunning},
		"running, run over": {runSp("step", "a", runStartedAt(1)), true, runCanceled},
		"no state, no time": {store.Span{}, false, runQueued},
		"no state, started": {store.Span{Started: runAt(1)}, false, runRunning},
	} {
		assert.Equal(t, tc.want, runSpanClass(tc.s, tc.runDone), name)
	}
}

func TestRunMarks(t *testing.T) {
	for _, c := range []string{runOK, runFailed, runCanceled, runRunning, runQueued, runSkipped, runEnded} {
		assert.NotEmpty(t, runMark(c), c)
	}
	assert.Empty(t, runMark("nonsense"))
}

func TestRunStatusClassAndPill(t *testing.T) {
	over := func(status string) store.Run { return runOf(10, status) }
	for name, tc := range map[string]struct {
		run   store.Run
		task  *store.Task
		class string
		label string
		pill  string
	}{
		"running":      {runOf(0, ""), nil, runRunning, "running", "run"},
		"suspended":    {func() store.Run { r := runOf(0, ""); r.Suspended = true; return r }(), nil, runRunning, "suspended", "warn"},
		"stopping":     {runOf(0, ""), &store.Task{Canceling: true}, runRunning, "stopping", "warn"},
		"task running": {runOf(0, ""), &store.Task{}, runRunning, "running", "run"},
		"ok":           {over("ok"), nil, runOK, "ok", "ok"},
		"failed":       {over("failed"), nil, runFailed, "failed", "bad"},
		"canceled":     {over("canceled"), nil, runCanceled, "canceled", "warn"},
		"odd status":   {over("weird"), nil, runEnded, "weird", ""},
		"no status":    {over(""), nil, runEnded, "ended", ""},
	} {
		assert.Equal(t, tc.class, runStatusClass(tc.run), name)
		label, pill := runPill(tc.run, tc.task)
		assert.Equal(t, tc.label, label, name)
		assert.Equal(t, tc.pill, pill, name)
	}
}

func TestRunClockOfARun(t *testing.T) {
	now := runAt(30)
	run := runOf(0, "")
	assert.Equal(t, runAt(0), runOrigin(run))
	assert.Equal(t, 30*time.Second, runElapsed(run, now))
	assert.Equal(t, runAt(0).UnixMilli(), runSince(run))

	run.Suspended = true
	assert.Zero(t, runSince(run), "a suspended run's clock stands still")

	over := runOf(12, "ok")
	assert.Equal(t, 12*time.Second, runElapsed(over, now))
	assert.Zero(t, runSince(over))

	// A run without a start takes the start of its command, then of its
	// first span, and has no clock without any.
	noStart := runOf(0, "")
	noStart.Start = time.Time{}
	assert.Equal(t, runAt(0), runOrigin(noStart))
	noStart.Root.Started = time.Time{}
	assert.Equal(t, runAt(0), runOrigin(noStart), "the queue time of the command")
	noStart.Root.Queued = time.Time{}
	noStart.Root.Children = []store.Span{
		runSp("step", "late", runStartedAt(5)), runSp("step", "queued"),
		runSp("step", "early", runStartedAt(2), runKids(runSp("target", "t", runStartedAt(3)))),
	}
	assert.Equal(t, runAt(2), runOrigin(noStart))
	noStart.Root.Children = nil
	assert.True(t, runOrigin(noStart).IsZero())
	assert.Zero(t, runElapsed(noStart, now))
	assert.Zero(t, runSince(noStart))
	// A clock before the start is 0.
	assert.Zero(t, runElapsed(runOf(0, ""), runAt(-5)))
}

func TestRunWhen(t *testing.T) {
	now := runAt(30)
	assert.Equal(t, "–", runWhen(time.Time{}, now))
	assert.Equal(t, runAt(5).Local().Format("15:04:05"), runWhen(runAt(5), now))
	assert.Equal(t, runAt(-86400).Local().Format("Jan 2 15:04"), runWhen(runAt(-86400), now))
	assert.Equal(t, "", runStamp(time.Time{}))
	assert.Contains(t, runStamp(runAt(1)), "2026-10-14")
}

func TestRunTaskOf(t *testing.T) {
	snap := store.Snapshot{Tasks: []store.Task{{ID: "a", Trace: "x"}, {ID: "b", Trace: "y"}, {ID: "c", Trace: "x"}}}
	got, ok := runTaskOf(snap, "x")
	assert.True(t, ok)
	assert.Equal(t, "c", got.ID, "the newest")
	_, ok = runTaskOf(snap, "z")
	assert.False(t, ok)
	_, ok = runTaskOf(snap, "")
	assert.False(t, ok)
}

func TestRunSegs(t *testing.T) {
	// No steps yet: a running segment, and the status of the command after.
	segs := runSegs(runOf(0, ""))
	assert.Equal(t, []runSeg{{Class: runRunning, Title: "starting"}}, segs)
	assert.Equal(t, []runSeg{{Class: runFailed, Title: runFailed}}, runSegs(runOf(5, "failed")))

	run := runOf(0, "",
		runSp("step", "pull", runEndedAt(0, 4, "ok"), runWith(func(s *store.Span) { s.Total = 1 })),
		runSp("step", "hidden", runStartedAt(0), runWith(func(s *store.Span) { s.Hidden = true })),
		runSp("wait", "realm lock", runStartedAt(0)),
		runSp("step", "nodes", runStartedAt(4), runWith(func(s *store.Span) { s.Total = 4 }), runKids(
			runSp("target", "a", runEndedAt(4, 6, "ok")),
			runSp("target", "b", runStartedAt(4)),
		)),
		runSp("step", "mesh", runStartedAt(5), runWith(func(s *store.Span) { s.Size, s.Amount = 10, 3 })),
		runSp("step", "slurm"),
		runSp("step", "idle", runStartedAt(5)),
	)
	segs = runSegs(run)
	require.Len(t, segs, 5)
	assert.Equal(t, runSeg{Class: runOK, Title: "pull: ok (0/1)"}, segs[0])
	assert.Equal(t, runSeg{Class: runRunning, Title: "nodes: running (1/4)", Fill: 25}, segs[1])
	assert.Equal(t, runSeg{Class: runRunning, Title: "mesh: running", Fill: 30}, segs[2])
	assert.Equal(t, runSeg{Class: runQueued, Title: "slurm: queued"}, segs[3])
	assert.Equal(t, runSeg{Class: runRunning, Title: "idle: running"}, segs[4].withoutFill())

	// Targets that move along count for what they did.
	pull := runOf(0, "", runSp("step", "pull", runStartedAt(0), runWith(func(s *store.Span) { s.Total = 2 }), runKids(
		runSp("target", "img", runStartedAt(0), runWith(func(s *store.Span) { s.Size, s.Amount, s.Unit = 100, 50, "bytes" })),
		runSp("target", "img2", runStartedAt(0), runWith(func(s *store.Span) { s.Size, s.Amount, s.Unit = 100, 500, "bytes" })),
		runSp("target", "img3"),
	)))
	assert.Equal(t, 50, runSegs(pull)[0].Fill, "1.5 of 3 targets")

	// A run that is over shows what never ran as skipped.
	over := runOf(9, "canceled", runSp("step", "nodes", runStartedAt(1)), runSp("step", "later"))
	assert.Equal(t, []string{runCanceled, runSkipped}, []string{runSegs(over)[0].Class, runSegs(over)[1].Class})
}

// withoutFill is the segment without its fill, for comparing the rest.
func (s runSeg) withoutFill() runSeg { s.Fill = 0; return s }

func TestRunLine(t *testing.T) {
	now := runAt(30)
	for name, tc := range map[string]struct {
		run  store.Run
		task *store.Task
		want string
	}{
		"ok":        {runOf(12, "ok"), nil, "done in 12s"},
		"failed":    {runOf(75, "failed"), nil, "failed in 1m15s"},
		"canceled":  {runOf(3, "canceled"), nil, "canceled in 3.0s"},
		"no status": {runOf(3, ""), nil, "ended in 3.0s"},
		"suspended": {func() store.Run { r := runOf(0, ""); r.Suspended = true; return r }(), nil, "suspended"},
		"starting":  {runOf(0, ""), nil, "starting"},
		"working":   {runOf(0, "", runSp("step", "pull", runEndedAt(0, 1, "ok"))), nil, "working"},
		"message": {func() store.Run {
			r := runOf(0, "", runSp("step", "pull", runEndedAt(0, 1, "ok")))
			r.Root.Message = "waiting for docker"
			return r
		}(), nil, "waiting for docker"},
		"stopping": {runOf(0, "", runSp("step", "nodes", runStartedAt(1), runKids(
			runSp("target", "a", runStartedAt(1)), runSp("target", "b", runStartedAt(1)), runSp("target", "c"),
		))), &store.Task{Canceling: true}, "stopping · 2 running will stop · 1 queued will not start"},
		"rolling back": {runOf(0, "", runSp("step", "rollback", runStartedAt(1))), &store.Task{Canceling: true}, "rolling back"},
		"a rollback that ended": {runOf(0, "", runSp("step", "rollback", runEndedAt(1, 2, "ok"))),
			&store.Task{Canceling: true}, "stopping · 0 running will stop · 0 queued will not start"},
		"steps, wait and call": {runOf(0, "",
			runSp("wait", "realm lock", runStartedAt(0), runWith(func(s *store.Span) { s.Message = "create cluster (pid 4) on host" })),
			runSp("call", "docker info", runStartedAt(0)),
			runSp("step", "nodes", runStartedAt(1), runKids(runSp("target", "worker-2", runStartedAt(1), runKids(runSp("wait", "ready", runStartedAt(1), runWith(func(s *store.Span) { s.Message = "munge" })))))),
			runSp("step", "mesh", runStartedAt(1)),
			runSp("step", "done", runEndedAt(0, 1, "ok")),
			runSp("step", "later"),
		), nil, "waiting for realm lock create cluster (pid 4) on host  |  docker info  |  nodes 0/1 · worker-2 ready munge  |  mesh"},
	} {
		assert.Equal(t, tc.want, runLine(tc.run, tc.task, now), name)
	}
}

func TestRunStepNow(t *testing.T) {
	// The first target with something to say wins over one without.
	step := runSp("step", "nodes", runStartedAt(0), runWith(func(s *store.Span) { s.Total = 5 }), runKids(
		runSp("target", "w-0", runStartedAt(0)),
		runSp("batch", "2/5", runStartedAt(0), runKids(
			runSp("target", "w-1", runStartedAt(0), runWith(func(s *store.Span) { s.Message = "starting" })),
		)),
		runSp("target", "w-2", runEndedAt(0, 1, "ok")),
	))
	assert.Equal(t, "nodes 1/5 · w-1 starting", runStepNow(step))

	// Without a target that says anything, the first running one is named.
	step.Children = step.Children[:1]
	assert.Equal(t, "nodes 0/5 · w-0", runStepNow(step))
	step.Children = append(step.Children, runSp("target", "w-2", runEndedAt(0, 1, "ok")))

	// A batch without a running target is passed over; a node is named by its node.
	step.Children = []store.Span{
		runSp("batch", "1/2", runStartedAt(0), runKids(runSp("target", "x", runEndedAt(0, 1, "ok")))),
		runSp("target", "name", runStartedAt(0), runWith(func(s *store.Span) { s.Node = "node-7"; s.Unit, s.Size, s.Amount = "items", 4, 1 })),
	}
	assert.Equal(t, "nodes 1/5 · node-7 1 / 4 items", runStepNow(step))

	// No target: the step's message, or what its running child does.
	msg := runSp("step", "mesh", runStartedAt(0), runWith(func(s *store.Span) { s.Message = "registering" }))
	assert.Equal(t, "mesh · registering", runStepNow(msg))
	call := runSp("step", "mesh", runStartedAt(0), runKids(runSp("call", "docker exec", runStartedAt(0), runWith(func(s *store.Span) { s.Message = "x" }))))
	assert.Equal(t, "mesh · docker exec x", runStepNow(call))
	assert.Equal(t, "mesh", runStepNow(runSp("step", "mesh", runStartedAt(0))))
}

func TestRunDetail(t *testing.T) {
	assert.Equal(t, "own", runDetail(runSp("target", "t", runWith(func(s *store.Span) { s.Message = "own" }))))
	// Queued and ended children are passed over; batches are looked into.
	tgt := runSp("target", "t", runStartedAt(0), runKids(
		runSp("call", "q"),
		runSp("call", "e", runEndedAt(0, 1, "ok")),
		runSp("batch", "b", runStartedAt(0)),
		runSp("batch", "b2", runStartedAt(0), runKids(runSp("call", "inner", runStartedAt(0)))),
	))
	assert.Equal(t, "inner", runDetail(tgt))
	tgt.Children = tgt.Children[:3]
	assert.Equal(t, "", runDetail(tgt))
	assert.Equal(t, "1 / 2 items", runDetail(runSp("target", "t", runWith(func(s *store.Span) { s.Unit, s.Amount, s.Size = "items", 1, 2 }))))
}

func TestRunName(t *testing.T) {
	assert.Equal(t, "n", runName(store.Span{Name: "x", Node: "n"}))
	assert.Equal(t, "x", runName(store.Span{Name: "x"}))
}
