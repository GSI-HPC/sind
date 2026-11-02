// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunGantt(t *testing.T) {
	pull := runSp("target", "img", runStartedAt(1), runWith(func(s *store.Span) {
		s.Unit, s.Amount, s.Size, s.Message = "bytes", 50, 100, "extracting"
		s.Samples = []store.Sample{{At: runAt(1.5), Amount: 10, Size: 100}, {At: runAt(2), Amount: 50, Size: 100}}
	}), runKids(runSp("call", "docker pull", runStartedAt(1))))
	failed := runSp("target", "worker-0", runEndedAt(2, 4, "failed"), runWith(func(s *store.Span) {
		s.Node, s.Role, s.Err, s.Class, s.Hidden = "worker-0", "worker", "boom", "timeout", true
	}), runKids(runSp("wait", "ready", runEndedAt(2, 4, "failed"), runWith(func(s *store.Span) { s.Timeout = 3 * time.Second }))))
	run := runOf(0, "",
		runSp("step", "pull images", runStartedAt(1), runWith(func(s *store.Span) { s.Total = 1 }), runKids(pull)),
		runSp("step", "nodes", runStartedAt(2), runKids(failed, runSp("call", "docker ps", runStartedAt(2)))),
		runSp("step", "slurm"))
	task := &store.Task{ID: "t1", Canceling: true}
	g := runGanttOf(runCtx{Run: run, Found: true, Task: task, Now: runAt(5)})

	assert.Equal(t, runAt(0).UnixMilli(), g.Origin)
	assert.Nil(t, g.End)
	assert.Equal(t, int64(1000), g.Last)
	assert.Equal(t, runRunning, g.State)
	assert.True(t, g.Stopping)
	assert.Equal(t, "create cluster dev", g.Title)
	var names []string
	for _, s := range g.Spans {
		names = append(names, s.Kind+" "+s.Name)
	}
	assert.Equal(t, []string{
		"command create cluster", "step pull images", "target img", "call docker pull",
		"step nodes", "target worker-0", "wait ready", "step slurm",
	}, names, "parents first; the calls of a target only")

	ms := func(v int64) *int64 { return &v }
	assert.Equal(t, ganttSpan{
		ID: "target-img", Parent: "", Kind: "target", Name: "img", Q: 0, R: ms(1000), Class: runRunning,
		Unit: "bytes", Amount: 50, Size: 100, Msg: "extracting", W: [][3]int64{{1500, 10, 100}, {2000, 50, 100}},
	}, g.Spans[2])
	assert.Equal(t, ganttSpan{
		ID: "target-worker-0", Kind: "target", Name: "worker-0", Node: "worker-0", Role: "worker", Hidden: true,
		R: ms(2000), E: ms(4000), Class: runFailed, Err: "boom", ErrKind: "timeout",
	}, g.Spans[5])
	assert.Equal(t, int64(3000), g.Spans[6].Timeout)
	assert.Nil(t, g.Spans[7].R, "a step that has not begun")
	assert.Equal(t, 1, g.Spans[1].Total)

	// The browser reads the short keys.
	var back map[string]any
	require.NoError(t, json.Unmarshal([]byte(g.JSON()), &back))
	assert.Equal(t, "create cluster dev", back["title"])
	assert.NotContains(t, back, "end")
	assert.NotContains(t, back, "usual")

	// An ended run has an end, and is not stopping any more.
	over := runOf(9, store.StatusCanceled)
	over.Suspended, over.Unfinished = true, true
	g = runGanttOf(runCtx{Run: over, Found: true, Task: task, Now: runAt(20)})
	require.NotNil(t, g.End)
	assert.Equal(t, int64(9000), *g.End)
	assert.Equal(t, runCanceled, g.State)
	assert.Equal(t, store.StatusCanceled, g.Status)
	assert.False(t, g.Stopping)
	assert.True(t, g.Suspended)
	assert.True(t, g.Unfinished)

	// A run without any start: no origin.
	g = runGanttOf(runCtx{Run: store.Run{ID: "x"}, Found: true, Now: runAt(5)})
	assert.Zero(t, g.Origin)
	assert.Zero(t, g.Spans[0].Q)
}

func TestRunUsual(t *testing.T) {
	// earlier returns an ok run of the command that took d seconds, its
	// nodes step n of them, and pulls images when pull is set.
	earlier := func(id string, d, n float64, pull bool) store.Run {
		steps := []store.Span{runSp("step", "nodes", runEndedAt(0, n, "ok")), runSp("step", "never")}
		if pull {
			steps = append(steps, runSp("step", "pull images", runEndedAt(0, 1, "ok")))
		}
		r := runOf(d, store.StatusOK, steps...)
		r.ID = id
		return r
	}
	run := runOf(0, "", runSp("step", "nodes", runStartedAt(0)))
	failed := earlier("failed", 1, 1, false)
	failed.Status = store.StatusFailed
	other := earlier("other", 1, 1, false)
	other.Title = "delete cluster dev"
	runs := []store.Run{
		run, failed, other, runOf(0, ""), earlier("pulled", 1, 1, true),
		earlier("a", 30, 20, false), earlier("b", 10, 6, false), earlier("c", 20, 12, false),
		earlier("d", 40, 30, false), earlier("e", 50, 40, false), earlier("f", 1, 1, false),
	}
	runs[3].ID = "going"

	usual, total, n := runUsual(store.Snapshot{Runs: runs}, run)
	assert.Equal(t, 5, n, "the newest five that count")
	assert.Equal(t, int64(30000), total)
	assert.Equal(t, map[string]int64{"nodes": 20000}, usual, "a step that never ran has no usual duration")

	// A run that pulls counts only the runs that pulled.
	pulling := runOf(0, "", runSp("step", "pull images", runStartedAt(0)))
	usual, total, n = runUsual(store.Snapshot{Runs: runs}, pulling)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(1000), total)
	assert.Equal(t, map[string]int64{"nodes": 1000, "pull images": 1000}, usual)

	usual, total, n = runUsual(store.Snapshot{Runs: []store.Run{run, failed}}, run)
	assert.Nil(t, usual)
	assert.Zero(t, total)
	assert.Zero(t, n)

	// Runs of one command count however their titles name the nodes:
	// each create worker makes new ones.
	cur := runOf(0, "", runSp("step", "nodes", runStartedAt(0)))
	cur.Title, cur.Command = "create worker worker-3.legacy", "create worker legacy"
	prev := earlier("prev", 8, 8, false)
	prev.Title, prev.Command = "create worker worker-2.legacy", "create worker legacy"
	_, total, n = runUsual(store.Snapshot{Runs: []store.Run{cur, prev}}, cur)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(8000), total)

	g := runGanttOf(runCtx{Run: run, Found: true, Snap: store.Snapshot{Runs: runs}, Now: runAt(5)})
	assert.Equal(t, 5, g.UsualN)
	assert.Equal(t, int64(30000), g.UsualRun)
}

func TestRunMedian(t *testing.T) {
	assert.Equal(t, int64(7), runMedian([]int64{7}))
	assert.Equal(t, int64(2), runMedian([]int64{9, 2, 1, 4}), "the lower of the two in the middle")
	d := []int64{3, 1, 2}
	assert.Equal(t, int64(2), runMedian(d))
	assert.Equal(t, []int64{3, 1, 2}, d, "the list stays as it was")
}
