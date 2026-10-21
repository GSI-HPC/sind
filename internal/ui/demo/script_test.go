// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

var t0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

// reduceLog returns the one run of an event log.
func reduceLog(t *testing.T, log []byte) store.Run {
	t.Helper()
	r := activity.NewReducer(10)
	for _, line := range splitLines(log) {
		r.Line(line)
	}
	runs := r.Runs()
	require.Len(t, runs, 1)
	return runs[0]
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return out
}

// spans returns the spans of the run, depth first.
func spans(r store.Run) []store.Span {
	var out []store.Span
	var walk func(s store.Span)
	walk = func(s store.Span) {
		out = append(out, s)
		for _, c := range s.Children {
			walk(c)
		}
	}
	walk(r.Root)
	return out
}

// stepsOf returns the names of the steps of the run.
func stepsOf(r store.Run) []string {
	var out []string
	for _, s := range r.Root.Children {
		out = append(out, s.Name)
	}
	return out
}

func spanNamed(t *testing.T, r store.Run, name string) store.Span {
	t.Helper()
	for _, s := range spans(r) {
		if s.Name == name {
			return s
		}
	}
	require.Failf(t, "no span", "%q", name)
	return store.Span{}
}

// effects records what a script does to the world.
type effects struct {
	mu  sync.Mutex
	ops []string
}

func (e *effects) emit(op string, n node) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ops = append(e.ops, op+" "+n.name)
}

// play records the script with the effects recorded.
func playRecorded(t *testing.T, cancelAfter time.Duration, work func(ctx context.Context, p play) error) (store.Run, []string) {
	t.Helper()
	fx := &effects{}
	log := record(recording{
		id: runID(1), trace: traceID(1), title: "cmd", start: t0, cancelAfter: cancelAfter,
		work: func(ctx context.Context, p play) error {
			p.emit = fx.emit
			return work(ctx, p)
		},
	})
	return reduceLog(t, log), fx.ops
}

func create(s createSpec) func(context.Context, play) error {
	return func(ctx context.Context, p play) error { return p.create(ctx, s) }
}

func TestCreate_OK(t *testing.T) {
	nodes := nodeSet("dev", 14*time.Second, 19*time.Second)
	r, ops := playRecorded(t, 0, create(createSpec{image: "img:1", mesh: true, nodes: nodes}))
	assert.Equal(t, store.StatusOK, r.Status)
	assert.Equal(t, "cmd", r.Title)
	assert.Equal(t, []string{"pull images", "preflight", "nodes", "mesh registration", "slurm"}, stepsOf(r))
	assert.Equal(t, []string{
		"create controller", "start controller", "create worker-0", "start worker-0",
		"ready controller", "ready worker-0",
	}, ops)
	pulled := spanNamed(t, r, "img:1")
	assert.Equal(t, int64(8), pulled.Amount, "the pull counted all layers")
	assert.Equal(t, "items", pulled.Unit)
	var units []string
	for _, sp := range spans(r) {
		if sp.Name == "ready" && sp.Message != "" {
			units = append(units, sp.Message)
		}
	}
	assert.Contains(t, units, "slurmctld")
	assert.Contains(t, units, "slurmd")
}

func TestCreate_RunsToTheRecordedTime(t *testing.T) {
	r, _ := playRecorded(t, 0, create(createSpec{nodes: nodeSet("dev", 14*time.Second)}))
	require.NotNil(t, r.End)
	assert.Equal(t, t0, r.Start)
	assert.Greater(t, r.End.Sub(r.Start), 14*time.Second)
}

func TestCreate_Worker(t *testing.T) {
	r, _ := playRecorded(t, 0, create(createSpec{nodes: nodeSet("dev", 0, 19*time.Second)[1:]}))
	assert.Equal(t, store.StatusOK, r.Status)
	assert.Equal(t, []string{"preflight", "nodes", "slurm"}, stepsOf(r), "no pull, no mesh")
}

func TestCreate_PullFails(t *testing.T) {
	r, ops := playRecorded(t, 0, create(createSpec{image: "img:broken", pullFail: errors.New("pull access denied"), nodes: nodeSet("dev", time.Second)}))
	assert.Equal(t, store.StatusFailed, r.Status)
	assert.Contains(t, r.Err, "pulling img:broken")
	assert.Equal(t, []string{"pull images", "preflight"}, stepsOf(r))
	assert.Empty(t, ops, "nothing was made")
}

func TestCreate_TimeoutRollsBack(t *testing.T) {
	r, ops := playRecorded(t, 0, create(createSpec{image: "i", mesh: true, nodes: nodeSet("bench", 14*time.Second, 21*time.Second, 0, 340*time.Second)}))
	assert.Equal(t, store.StatusFailed, r.Status)
	assert.Equal(t, []string{"pull images", "preflight", "nodes", "rollback"}, stepsOf(r))
	assert.Equal(t, store.StatusFailed, spanNamed(t, r, "worker-1").Status)
	assert.Equal(t, store.StatusCanceled, spanNamed(t, r, "worker-2").Status, "what still waited was stopped")
	assert.Equal(t, store.StatusOK, spanNamed(t, r, "worker-0").Status)
	assert.Equal(t, "remove worker-2", ops[len(ops)-1])
}

func TestCreate_Interrupts(t *testing.T) {
	// An interrupt at any second ends the run ok or canceled, never in
	// between; those in the waits and in the slurm step roll back.
	seen := map[string]bool{}
	for at := time.Second; at < 80*time.Second; at += 1500 * time.Millisecond {
		r, _ := playRecorded(t, at, create(createSpec{image: "i", mesh: true, nodes: nodeSet("dev", 14*time.Second, 19*time.Second)}))
		require.Contains(t, []string{store.StatusOK, store.StatusCanceled}, r.Status, "at %s", at)
		if r.Status != store.StatusCanceled {
			continue
		}
		last := r.Root.Children[len(r.Root.Children)-1]
		require.Equal(t, "rollback", last.Name, "at %s", at)
		for _, s := range r.Root.Children {
			if s.Status == store.StatusCanceled {
				seen[s.Name] = true
			}
		}
	}
	assert.True(t, seen["nodes"], "interrupted while the nodes wait")
	assert.True(t, seen["slurm"], "interrupted while slurm starts")
}

func TestRemove(t *testing.T) {
	nodes := nodeSet("old", 0, 0, 0)
	r, ops := playRecorded(t, 0, func(ctx context.Context, p play) error { return p.remove(ctx, nodes, true, true) })
	assert.Equal(t, store.StatusOK, r.Status)
	assert.Equal(t, []string{"mesh deregistration", "nodes", "network", "volumes", "mesh"}, stepsOf(r))
	assert.Equal(t, []string{"remove controller", "remove worker-0", "remove worker-1"}, ops)

	r, _ = playRecorded(t, 0, func(ctx context.Context, p play) error { return p.remove(ctx, nodes, false, false) })
	assert.Equal(t, []string{"mesh deregistration", "nodes", "network"}, stepsOf(r))
}

func TestRemove_Interrupted(t *testing.T) {
	nodes := nodeSet("old", 0, 0, 0)
	r, ops := playRecorded(t, time.Second, func(ctx context.Context, p play) error { return p.remove(ctx, nodes, true, true) })
	assert.Equal(t, store.StatusCanceled, r.Status)
	assert.Equal(t, []string{"mesh deregistration", "nodes"}, stepsOf(r), "no network after the interrupt")
	assert.Equal(t, store.StatusCanceled, spanNamed(t, r, "worker-1").Status, "never started")
	assert.NotContains(t, ops, "remove worker-1")
}

func TestDeleteWorkers(t *testing.T) {
	nodes := nodeSet("dev", 0, 0, 0)[1:]
	r, ops := playRecorded(t, 0, func(ctx context.Context, p play) error { return p.deleteWorkers(ctx, nodes) })
	assert.Equal(t, store.StatusOK, r.Status)
	assert.Equal(t, []string{"slurm", "nodes"}, stepsOf(r))
	assert.Equal(t, []string{"remove worker-0", "remove worker-1"}, ops)

	r, _ = playRecorded(t, time.Second, func(ctx context.Context, p play) error { return p.deleteWorkers(ctx, nodes) })
	assert.Equal(t, store.StatusCanceled, r.Status)
}

func TestPower(t *testing.T) {
	nodes := nodeSet("dev", 0, 0)
	want := map[string][]string{
		"shutdown": {"stop controller", "stop worker-0"},
		"on":       {"start controller", "ready controller", "start worker-0", "ready worker-0"},
		"reboot":   {"stop controller", "start controller", "ready controller", "stop worker-0", "start worker-0", "ready worker-0"},
		"cycle":    {"stop controller", "start controller", "ready controller", "stop worker-0", "start worker-0", "ready worker-0"},
		"cut":      {"stop controller", "stop worker-0"},
		"freeze":   {"pause controller", "pause worker-0"},
		"unfreeze": {"unpause controller", "unpause worker-0"},
	}
	require.Len(t, powerOps, len(want))
	for op, ops := range want {
		r, got := playRecorded(t, 0, func(ctx context.Context, p play) error { return p.power(ctx, op, nodes) })
		assert.Equal(t, store.StatusOK, r.Status, op)
		assert.Equal(t, ops, got, op)
	}
	r, _ := playRecorded(t, time.Second, func(ctx context.Context, p play) error { return p.power(ctx, "reboot", nodes) })
	assert.Equal(t, store.StatusCanceled, r.Status)
}

func TestAttach(t *testing.T) {
	r, ops := playRecorded(t, 0, func(ctx context.Context, p play) error { return p.attach(ctx, opConnect, node{name: "minio"}) })
	assert.Equal(t, store.StatusOK, r.Status)
	assert.Equal(t, []string{"connect minio"}, ops)
	assert.Equal(t, "minio", spanNamed(t, r, "minio").Node)
}

func TestGeneric(t *testing.T) {
	r, _ := playRecorded(t, 0, func(ctx context.Context, p play) error { return p.generic(ctx, "reload") })
	assert.Equal(t, store.StatusOK, r.Status)
	assert.Equal(t, []string{"reload"}, stepsOf(r))
}

func TestPlay_NoEffects(t *testing.T) {
	// A recorded run has nobody to tell.
	p := play{clk: &manualClock{t: t0}}
	assert.NotPanics(t, func() { p.do(opCreate, node{}) })
}

func TestFate(t *testing.T) {
	assert.NoError(t, fate(nil))
	boom := errors.New("boom")
	assert.Equal(t, boom, fate(boom))
	err := fate(fmt.Errorf("x: %w", context.Canceled))
	var c classed
	require.ErrorAs(t, err, &c)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	assert.NoError(t, interrupted(ctx))
	cancel()
	assert.ErrorIs(t, interrupted(ctx), context.Canceled)
}

func TestTimedOut(t *testing.T) {
	assert.ErrorIs(t, timedOut{}, context.DeadlineExceeded)
	assert.Equal(t, "timed out", timedOut{}.Error())
}
