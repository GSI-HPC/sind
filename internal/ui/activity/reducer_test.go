// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

func TestGoldenLog(t *testing.T) {
	run := only(t, "golden-v1.jsonl")

	assert.Equal(t, "0123456789abcdef", run.ID)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", run.Trace)
	assert.Equal(t, "00f067aa0ba902b7", run.Parent)
	assert.Equal(t, "v1.2.3", run.Version)
	assert.Empty(t, run.Program, "the golden trace line names no program")
	assert.Equal(t, "exec", run.Title)
	assert.Equal(t, at("2026-09-25T12:00:00Z"), run.Start)
	require.NotNil(t, run.End)
	assert.Equal(t, at("2026-09-25T12:00:02Z"), *run.End, "the end of the first command")
	assert.Equal(t, at("2026-09-25T12:00:03.5Z"), run.Last)
	assert.Equal(t, store.StatusOK, run.Status)
	assert.False(t, run.Suspended)
	assert.True(t, run.Done())

	root := run.Root
	assert.Equal(t, spanOf(1), root.ID)
	assert.Equal(t, KindCommand, root.Kind)
	assert.True(t, root.DryRun)
	// The other commands of the run hang below the root, in order, after
	// the steps of the first.
	assert.Equal(t, []string{"credential bmc", "run", "power on", "confirm", "status", "fetch"}, names(root.Children))
	assert.Equal(t, []string{"run", "power on", "confirm", "status", "fetch"}, names(VisibleChildren(root)))

	cred := root.Children[0]
	assert.True(t, cred.Hidden)
	assert.Equal(t, KindCall, cred.Kind)

	step := find(t, root, "run")
	assert.True(t, step.Fold)
	assert.True(t, step.ShowLines)
	assert.Equal(t, 2, step.Total)
	assert.Equal(t, 1, step.Limit)
	assert.Equal(t, "1 of 2 failed", step.Message)
	assert.Equal(t, store.StatusFailed, step.Status)
	assert.Equal(t, "target", step.Class)
	assert.Equal(t, "1 of 2 hosts failed: exe0002", step.Err)
	assert.Equal(t, StateEnded, step.State)

	ok := find(t, root, "exe0001")
	assert.Equal(t, "exe0001.hpc.example.org", ok.Host)
	assert.Equal(t, "compute", ok.Role)
	assert.Equal(t, "exe0001", ok.Node)
	assert.Equal(t, at("2026-09-25T12:00:00Z"), ok.Queued)
	assert.Equal(t, at("2026-09-25T12:00:01Z"), ok.Started, "from its run event")
	assert.Equal(t, store.StatusOK, ok.Status)

	ssh := find(t, root, "ssh")
	assert.Equal(t, 1500*time.Millisecond, ssh.Timeout)
	assert.Equal(t, 2, ssh.Lines)
	require.NotNil(t, ssh.Exit)
	assert.Zero(t, *ssh.Exit)
	assert.Nil(t, ok.Exit)
	assert.Equal(t, ssh.Queued, ssh.Started, "started running")

	redfish := find(t, root, "redfish")
	assert.Equal(t, "exe0002.mgmt", redfish.Host)
	assert.Equal(t, "transport", redfish.Class)

	batch := find(t, root, "batch 1/2")
	assert.Equal(t, "1/2", batch.Batch)
	assert.Equal(t, "exe[0003-0007]", batch.Node)
	assert.Equal(t, 5, batch.Total)
	assert.Equal(t, []string{"exe0003", "exe0004", "exe0005", "exe0006", "exe0007"}, names(batch.Children))
	skipped := find(t, root, "batch 2/2")
	assert.Equal(t, store.StatusSkipped, skipped.Status)
	assert.Equal(t, "not tried: an earlier batch failed", skipped.Err)
	assert.True(t, skipped.Started.IsZero(), "a queued span that never ran")

	stagger := find(t, root, "stagger")
	assert.Equal(t, KindWait, stagger.Kind)
	assert.Equal(t, 30*time.Second, stagger.Timeout)
	assert.Equal(t, store.StatusCanceled, stagger.Status)

	confirm := find(t, root, "confirm")
	assert.Equal(t, "reset 2 hosts", confirm.Message)
	assert.Equal(t, store.StatusSkipped, confirm.Status)

	sol := find(t, root, "sol")
	assert.Equal(t, 21, sol.Lines)
	assert.Equal(t, 1, sol.Dropped)

	download := find(t, root, "download")
	assert.EqualValues(t, 109051904, download.Amount, "the end carries the final amount")
	assert.EqualValues(t, 2147483648, download.Size)
	assert.Equal(t, "bytes", download.Unit)
	index := find(t, root, "indexing the archive")
	assert.EqualValues(t, 1200, index.Amount)
	assert.Zero(t, index.Size, "a size not known")
	assert.Equal(t, "items", index.Unit)
	firmware := find(t, root, "firmware update")
	assert.EqualValues(t, 100, firmware.Amount)
	assert.EqualValues(t, 100, firmware.Size)
	assert.Equal(t, "percent", firmware.Unit)

	// Every state, status and class the golden log lists comes through.
	classes := map[string]bool{}
	walk(root, func(s store.Span) {
		if s.Class != "" {
			classes[s.Class] = true
		}
	})
	for _, c := range []string{"target", "transport", "timeout", "auth", "pin", "usage", "canceled"} {
		assert.True(t, classes[c], c)
	}
}

func TestCreateOK(t *testing.T) {
	run := only(t, "create-ok.jsonl")

	assert.Equal(t, "5d3c0a17b94e82f6", run.ID)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", run.Trace)
	assert.Equal(t, "00f067aa0ba902b7", run.Parent)
	assert.Equal(t, "sind", run.Program)
	assert.Equal(t, "v0.11.0", run.Version)
	assert.Equal(t, "create cluster", run.Title)
	assert.Equal(t, store.StatusOK, run.Status)
	assert.Empty(t, run.Err)
	assert.Empty(t, run.Class)
	require.NotNil(t, run.End)
	assert.Equal(t, *run.End, run.Last)
	assert.Equal(t, at("2026-10-14T09:30:00Z"), run.Start)

	root := run.Root
	assert.Equal(t, []string{"pull images", "nodes", "mesh registration", "slurm"}, names(VisibleChildren(root)))
	assert.Equal(t, []string{"pull images", "preflight", "nodes", "mesh registration", "slurm"}, names(root.Children))
	assert.True(t, root.Children[1].Hidden)

	pull := find(t, root, "pull images")
	assert.True(t, pull.Fold)
	assert.True(t, pull.ShowLines)
	assert.Equal(t, 1, pull.Total)
	image := pull.Children[0]
	assert.Equal(t, KindTarget, image.Kind)
	assert.Equal(t, "bytes", image.Unit)
	assert.EqualValues(t, 96<<20, image.Size)
	assert.Equal(t, image.Size, image.Amount, "the end carries the whole")
	assert.Empty(t, image.Role)

	nodes := find(t, root, "nodes")
	assert.True(t, nodes.Fold)
	assert.Equal(t, 3, nodes.Total)
	assert.Equal(t, Counts{Total: 3, OK: 3}, CountTargets(nodes))
	assert.Equal(t, Counts{Total: 3, OK: 3}, CountTargets(find(t, root, "slurm")))
	controller := nodes.Children[0]
	assert.Equal(t, "controller", controller.Role)
	assert.Equal(t, "controller", controller.Node)
	assert.Equal(t, KindWait, find(t, controller, "ready").Kind)
	assert.Equal(t, 300*time.Second, find(t, controller, "ready").Timeout)
	assert.Equal(t, "munge", find(t, controller, "ready").Message)
	assert.Equal(t, []string{"docker create", "docker start", "ready", "setup"}, names(controller.Children))
	assert.Equal(t, 14*time.Second, Elapsed(find(t, controller, "ready"), run.Last))

	// The step bar and the counts a run page draws.
	_, used, ok := SlowestWait(root, run.Last)
	require.True(t, ok)
	assert.Less(t, used, 0.5)
	assert.Empty(t, FailedTargets(root))
}

func TestCreateFailed(t *testing.T) {
	run := only(t, "create-failed.jsonl")

	assert.Equal(t, store.StatusFailed, run.Status)
	assert.Equal(t, "timeout", run.Class)
	assert.Equal(t, "setting up nodes: node worker-1: timed out; last probe error: probe munge: munge not ready", run.Err)
	require.True(t, run.Done())

	root := run.Root
	assert.Equal(t, []string{"pull images", "nodes", "rollback"}, names(VisibleChildren(root)))
	nodes := find(t, root, "nodes")
	assert.Equal(t, store.StatusFailed, nodes.Status)
	assert.Equal(t, Counts{Total: 5, OK: 2, Failed: 2, Canceled: 1}, CountTargets(nodes))
	assert.Zero(t, CountTargets(nodes).Running)

	assert.Equal(t, []Failure{{
		Err:   "node {}: timed out; last probe error: probe munge: munge not ready",
		Class: "timeout",
		Nodes: []string{"worker-1", "worker-2"},
	}}, FailedTargets(root))

	rollback := find(t, root, "rollback")
	assert.Equal(t, store.StatusOK, rollback.Status)
	assert.Len(t, rollback.Children, 6)

	// A hidden call failed with docker's exit code.
	network := find(t, root, "docker network inspect")
	assert.True(t, network.Hidden)
	assert.Equal(t, store.StatusFailed, network.Status)
	require.NotNil(t, network.Exit)
	assert.Equal(t, 1, *network.Exit)

	// The wait of worker-1 used all of its limit; the canceled one 0.99.
	w1 := find(t, find(t, nodes, "worker-1"), "ready")
	used, ok := WaitUsed(w1, run.Last)
	require.True(t, ok)
	assert.Equal(t, 1.0, used)
	assert.Equal(t, WaitCritical, WaitLevel(used))
	slowest, _, ok := SlowestWait(root, run.Last)
	require.True(t, ok)
	assert.Equal(t, "ready", slowest.Name)

	worker3 := find(t, nodes, "worker-3")
	assert.Equal(t, store.StatusCanceled, worker3.Status)
	assert.Equal(t, "canceled", worker3.Class)
	assert.Equal(t, "node worker-3: context canceled", worker3.Err)
}

func TestCreateCanceled(t *testing.T) {
	run := only(t, "create-canceled.jsonl")

	assert.Equal(t, store.StatusCanceled, run.Status)
	assert.Equal(t, "canceled", run.Class)
	assert.Equal(t, "setting up nodes: context canceled", run.Err)
	require.True(t, run.Done())

	nodes := find(t, run.Root, "nodes")
	assert.Equal(t, store.StatusCanceled, nodes.Status)
	assert.Equal(t, Counts{Total: 4, OK: 1, Canceled: 3}, CountTargets(nodes))
	assert.Empty(t, FailedTargets(run.Root), "a cancellation is no failure")

	queued := find(t, nodes, "worker-2")
	assert.Equal(t, store.StatusCanceled, queued.Status)
	assert.True(t, queued.Started.IsZero(), "it never started")
	assert.Zero(t, Elapsed(queued, run.Last))
	assert.Empty(t, queued.Children)

	assert.Equal(t, store.StatusOK, find(t, run.Root, "rollback").Status)
	assert.Equal(t, []string{"pull images", "nodes", "rollback"}, names(VisibleChildren(run.Root)))
}

func TestInterleaved(t *testing.T) {
	runs := reduce(t, "interleaved.jsonl")
	require.Len(t, runs, 2)
	// Newest first: the run whose first line came last.
	assert.Equal(t, "c0ffee00c0ffee02", runs[0].ID)
	assert.Equal(t, "c0ffee00c0ffee01", runs[1].ID)
	assert.Equal(t, store.StatusFailed, runs[0].Status)
	assert.Equal(t, store.StatusOK, runs[1].Status)
	assert.Empty(t, runs[1].Parent, "a terminal run continues no trace")
	assert.Equal(t, "7a1c9e4203040506070809"+"0a0b0c0d0e", runs[1].Trace)
	assert.Equal(t, "51d03b7703040506070809"+"0a0b0c0d0e", runs[0].Trace)

	// Each run is what it is when read alone: filter the file by run.
	for _, run := range runs {
		r := NewReducer(5)
		for _, l := range splitLines(readFixture(t, "interleaved.jsonl")) {
			if bytesContains(l, `"run":"`+run.ID+`"`) {
				r.Line(l)
			}
		}
		require.Len(t, r.Runs(), 1)
		assert.Equal(t, run, r.Runs()[0], run.ID)
	}
}

// TestGoldenIsCurrent holds the copy of the golden log to the one in the
// module, when the module cache has it, so that a new go-clikit that adds
// to the log gets its lines read.
func TestGoldenIsCurrent(t *testing.T) {
	want := goldenOfModule(t)
	assert.Equal(t, string(want), string(readFixture(t, "golden-v1.jsonl")))
}

func TestChangedAndImmutableRuns(t *testing.T) {
	r := NewReducer(5)
	r.Line(line("trace", "aaaaaaaaaaaaaaaa", `"trace":"`+traceT+`","program":"sind"`))
	r.Line(ev("start", "aaaaaaaaaaaaaaaa", "2026-01-01T00:00:00Z", `"span":"r1","kind":"command","name":"create cluster"`))
	r.Line(ev("start", "bbbbbbbbbbbbbbbb", "2026-01-01T00:00:01Z", `"span":"r1","kind":"command","name":"delete cluster"`))
	assert.Equal(t, []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"}, r.Changed())
	assert.Empty(t, r.Changed(), "forgotten once told")

	first := r.Runs()
	require.Len(t, first, 2)
	assert.Equal(t, "bbbbbbbbbbbbbbbb", first[0].ID, "newest first")

	r.Line(ev("start", "aaaaaaaaaaaaaaaa", "2026-01-01T00:00:02Z", `"span":"s1","parent":"r1","kind":"step","name":"nodes"`))
	assert.Equal(t, []string{"aaaaaaaaaaaaaaaa"}, r.Changed())
	second := r.Runs()
	assert.Empty(t, first[1].Root.Children, "a run handed out does not change")
	require.Len(t, second[1].Root.Children, 1)
	assert.Equal(t, "nodes", second[1].Root.Children[0].Name)

	// What was handed out is the caller's: the reducer builds on.
	second[1].Root.Children[0].Name = "changed"
	second[1].Title = "changed"
	r.Line(ev("update", "aaaaaaaaaaaaaaaa", "2026-01-01T00:00:03Z", `"span":"s1","parent":"r1","kind":"step","name":"nodes","message":"hi"`))
	third := r.Runs()
	assert.Equal(t, "nodes", third[1].Root.Children[0].Name)
	assert.Equal(t, "hi", third[1].Root.Children[0].Message)
	assert.Equal(t, "create cluster", third[1].Title)
	assert.Equal(t, traceT, third[1].Trace)

	// A run that did not change is the one it was.
	assert.Equal(t, third[0], r.Runs()[0])
}

func TestBadLinesAreLeftOut(t *testing.T) {
	const run = "aaaaaaaaaaaaaaaa"
	good := ev("start", run, "2026-01-01T00:00:00Z", `"span":"r1","kind":"command","name":"x"`)
	for name, bad := range map[string]string{
		"empty":            ``,
		"text":             `hello`,
		"cut":              string(good[:len(good)-12]),
		"array":            `[1,2]`,
		"number":           `5`,
		"version 2":        `{"v":2,"type":"start","run":"aaaaaaaaaaaaaaaa","time":"2026-01-01T00:00:00Z","span":"s","kind":"command"}`,
		"version missing":  `{"type":"start","run":"aaaaaaaaaaaaaaaa","time":"2026-01-01T00:00:00Z","span":"s","kind":"command"}`,
		"version text":     `{"v":"1","type":"start","run":"aaaaaaaaaaaaaaaa","time":"2026-01-01T00:00:00Z","span":"s","kind":"command"}`,
		"no run":           `{"v":1,"type":"start","time":"2026-01-01T00:00:00Z","span":"s","kind":"command"}`,
		"run is a number":  `{"v":1,"type":"start","run":5,"time":"2026-01-01T00:00:00Z","span":"s","kind":"command"}`,
		"unknown type":     `{"v":1,"type":"mystery","run":"aaaaaaaaaaaaaaaa","time":"2026-01-01T00:00:00Z","span":"s","kind":"command"}`,
		"no type":          `{"v":1,"run":"aaaaaaaaaaaaaaaa","time":"2026-01-01T00:00:00Z","span":"s","kind":"command"}`,
		"start, no span":   `{"v":1,"type":"start","run":"aaaaaaaaaaaaaaaa","time":"2026-01-01T00:00:00Z","kind":"command"}`,
		"start, no time":   `{"v":1,"type":"start","run":"aaaaaaaaaaaaaaaa","span":"s","kind":"command"}`,
		"start, bad time":  `{"v":1,"type":"start","run":"aaaaaaaaaaaaaaaa","time":"yesterday","span":"s","kind":"command"}`,
		"end, no span":     `{"v":1,"type":"end","run":"aaaaaaaaaaaaaaaa","time":"2026-01-01T00:00:00Z"}`,
		"unterminated obj": `{"v":1,"type":"start"`,
	} {
		t.Run(name, func(t *testing.T) {
			r := NewReducer(5)
			r.Line([]byte(bad))
			assert.Empty(t, r.Runs())
			assert.Empty(t, r.Changed(), "a line left out makes no run")
		})
	}

	// None of them costs a later line.
	r := NewReducer(5)
	r.Line([]byte(`{"v":1,"type":"start"`))
	r.Line(good)
	r.Line([]byte("}}garbage{{"))
	r.Line(ev("end", run, "2026-01-01T00:00:05Z", `"span":"r1","status":"ok","state":"ended"`))
	runs := r.Runs()
	require.Len(t, runs, 1)
	assert.Equal(t, store.StatusOK, runs[0].Status)
}

func TestUnknownKeysAndValuesAreLeftOut(t *testing.T) {
	r := NewReducer(5)
	const run = "aaaaaaaaaaaaaaaa"
	r.Line(line("trace", run, `"trace":"`+traceT+`","program":"sind","version":"v1","future":{"a":[1]},"traceFlags":"01","traceState":"x=y"`))
	r.Line(ev("start", run, "2026-01-01T00:00:00Z",
		`"span":"r1","kind":"command","name":"cmd","flags":["hidden","sparkly","dry-run"],"state":"running","seq":"one","timeout":"long","exit":"x","newkey":[1,2],"httpStatus":503,"cache":"hit"`))
	r.Line(ev("start", run, "2026-01-01T00:00:01Z",
		`"span":"s1","parent":"r1","kind":"hologram","name":"odd","state":"strange","unit":"furlongs","amount":3,"size":10`))
	r.Line(ev("end", run, "2026-01-01T00:00:02Z",
		`"span":"s1","parent":"r1","kind":"hologram","status":"exploded","class":"cosmic","err":"bang","amount":3,"size":10`))
	runs := r.Runs()
	require.Len(t, runs, 1)
	root := runs[0].Root
	assert.True(t, root.Hidden)
	assert.True(t, root.DryRun)
	assert.False(t, root.Fold)
	assert.Zero(t, root.Timeout, "a value of the wrong type is left out")
	assert.Equal(t, "sind", runs[0].Program)
	odd := root.Children[0]
	assert.Empty(t, odd.Kind)
	assert.Equal(t, StateEnded, odd.State)
	assert.Empty(t, odd.Status, "an unknown status is none")
	assert.Empty(t, odd.Class)
	assert.Equal(t, "bang", odd.Err)
	assert.Empty(t, odd.Unit)
	assert.EqualValues(t, 10, odd.Size)
	assert.EqualValues(t, 3, odd.Amount)
	assert.Equal(t, at("2026-01-01T00:00:01Z"), odd.Started, "an unknown state is running")
}
