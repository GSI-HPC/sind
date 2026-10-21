// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/stretchr/testify/require"
)

// update rewrites the recorded logs under testdata:
//
//	go test ./internal/ui/activity -run TestFixtures -update
var update = flag.Bool("update", false, "rewrite the recorded event logs in testdata")

// The logs below are what sind's create cluster writes, recorded with the
// real go-clikit Bus and Log under a clock the test moves by hand, so that
// the file is the same on each run. The span structure follows
// pkg/cluster/create.go: the steps pull images, preflight (hidden), nodes,
// mesh registration and slurm, a target for each node and image, a wait
// "ready" with its limit under each node, and the step rollback after a
// failure or an interrupt.

// fixtures lists the files of testdata and what writes each.
var fixtures = []struct {
	file string
	make func(t *testing.T) []byte
}{
	{"create-ok.jsonl", func(t *testing.T) []byte { return recordCreate(t, okRun) }},
	{"create-failed.jsonl", func(t *testing.T) []byte { return recordCreate(t, failedRun) }},
	{"create-canceled.jsonl", func(t *testing.T) []byte { return recordCreate(t, canceledRun) }},
	{"interleaved.jsonl", recordInterleaved},
}

func TestFixtures(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.file, func(t *testing.T) {
			got := f.make(t)
			path := filepath.Join("testdata", f.file)
			if *update {
				require.NoError(t, os.WriteFile(path, got, 0o644))
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, string(want), string(got), "run go test -run TestFixtures -update")
		})
	}
}

// clock is a Bus clock that moves when the test says so.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// classed is an error with the progress class sind gives it: stopped, for
// work a failure elsewhere or an interrupt ended, and interrupted.
type classed struct {
	error
	class progress.Class
}

func (e classed) Unwrap() error                 { return e.error }
func (e classed) ProgressClass() progress.Class { return e.class }

// timedOut is the error of a readiness wait that ran out of time.
type timedOut struct{}

func (timedOut) Error() string { return "timed out" }
func (timedOut) Unwrap() error { return context.DeadlineExceeded }

// outcome is how a recorded create ends.
type outcome int

const (
	okRun outcome = iota
	failedRun
	canceledRun
)

// recording is the clock and the runs' common start of a recorded command.
type recording struct {
	run    string
	trace  progress.TraceContext
	start  time.Time
	nodes  []node
	result outcome
}

// node is a node of a recorded cluster and what happens to it.
type node struct {
	name, role string
	// ready is when the node's probes pass; zero for a node that never
	// gets ready within the wait limit.
	ready time.Duration
	// queued is a node whose work never starts, as an interrupt that
	// comes first leaves it.
	queued bool
}

var (
	traceUI   = mustTrace("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	traceTerm = progress.TraceContext{Trace: progress.TraceID{0x7a, 0x1c, 0x9e, 0x42, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}}
	traceTwo  = progress.TraceContext{Trace: progress.TraceID{0x51, 0xd0, 0x3b, 0x77, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}}
	epoch     = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
)

func mustTrace(traceparent string) progress.TraceContext {
	tc, ok := progress.ParseTraceContext(traceparent, "")
	if !ok {
		panic("bad traceparent " + traceparent)
	}
	return tc
}

// recordCreate records a create cluster of the cluster dev.
func recordCreate(t *testing.T, result outcome) []byte {
	rec := recording{run: "5d3c0a17b94e82f6", trace: traceUI, start: epoch, result: result}
	switch result {
	case okRun:
		rec.nodes = []node{
			{name: "controller", role: "controller", ready: 14 * time.Second},
			{name: "worker-0", role: "worker", ready: 19 * time.Second},
			{name: "worker-1", role: "worker", ready: 26 * time.Second},
		}
	case failedRun:
		rec.nodes = []node{
			{name: "controller", role: "controller", ready: 14 * time.Second},
			{name: "worker-0", role: "worker", ready: 21 * time.Second},
			{name: "worker-1", role: "worker"},
			{name: "worker-2", role: "worker"},
			{name: "worker-3", role: "worker", ready: 340 * time.Second},
		}
	case canceledRun:
		rec.nodes = []node{
			{name: "controller", role: "controller", ready: 14 * time.Second},
			{name: "worker-0", role: "worker", ready: 60 * time.Second},
			{name: "worker-1", role: "worker", ready: 60 * time.Second},
			{name: "worker-2", role: "worker", queued: true},
		}
	}
	return normalizeIDs(rec.record(t))
}

// recordInterleaved records two runs that overlap in time, a terminal run
// that succeeds and a UI run that fails, and writes their lines in the
// order the events happened, as two processes appending to one file would.
func recordInterleaved(t *testing.T) []byte {
	ok := recording{run: "c0ffee00c0ffee01", trace: traceTerm, start: epoch.Add(5 * time.Second), result: okRun, nodes: []node{
		{name: "controller", role: "controller", ready: 12 * time.Second},
		{name: "worker-0", role: "worker", ready: 17 * time.Second},
	}}
	failed := recording{run: "c0ffee00c0ffee02", trace: traceTwo, start: epoch.Add(21 * time.Second), result: failedRun, nodes: []node{
		{name: "controller", role: "controller", ready: 13 * time.Second},
		{name: "worker-0", role: "worker"},
	}}
	return normalizeIDs(interleave(ok.record(t), failed.record(t)))
}

// record writes the log of the run: the command span of create cluster
// and what it does, as the outcome says.
func (rec recording) record(t *testing.T) []byte {
	var out bytes.Buffer
	clk := &clock{t: rec.start}
	log := progress.NewLog(&out, progress.LogOptions{Program: "sind", Version: "v0.11.0", Run: rec.run})
	bus := progress.NewBus(progress.BusOptions{
		Sinks: []progress.Sink{log}, Now: clk.Now, Trace: rec.trace, Program: "sind",
	})
	ctx, command := progress.Start(progress.WithBus(context.Background(), bus), progress.KindCommand, "create cluster")
	err := rec.create(ctx, clk)
	if err != nil && rec.result == canceledRun {
		err = classed{err, progress.ClassCanceled}
	}
	command.End(err)
	bus.Close()
	require.NoError(t, log.Close())
	return out.Bytes()
}

// create is the work of create cluster. The pull and the preflight run
// side by side, then come the nodes, and when they are ready mesh
// registration and slurm.
func (rec recording) create(ctx context.Context, clk *clock) error {
	pullAndPreflight(ctx, clk)

	nodesErr := rec.nodesStep(ctx, clk)
	if nodesErr != nil {
		rec.rollback(ctx, clk)
		return fmt.Errorf("setting up nodes: %w", nodesErr)
	}

	mctx, mesh := progress.Start(ctx, progress.KindStep, "mesh registration")
	dockerCall(mctx, clk, "docker exec", 300*time.Millisecond, nil)
	clk.Advance(900 * time.Millisecond)
	mesh.End(nil)

	sctx, slurm := progress.Start(ctx, progress.KindStep, "slurm",
		progress.WithFlags(progress.Fold), progress.Total(len(rec.nodes)))
	targets := make([]*progress.Span, len(rec.nodes))
	ctxs := make([]context.Context, len(rec.nodes))
	for i, n := range rec.nodes {
		ctxs[i], targets[i] = progress.Start(sctx, progress.KindTarget, n.name,
			progress.Queued(), progress.Node(n.name), progress.Role(n.role))
	}
	for i, n := range rec.nodes {
		targets[i].Run()
		dockerCall(ctxs[i], clk, "docker exec", 400*time.Millisecond, nil)
		unit := "slurmd"
		if n.role == "controller" {
			unit = "slurmctld"
		}
		_, wait := progress.Start(ctxs[i], progress.KindWait, "ready", progress.Timeout(272*time.Second))
		clk.Advance(1500 * time.Millisecond)
		wait.Update(progress.Message(unit))
		clk.Advance(2500 * time.Millisecond)
		wait.End(nil)
		targets[i].End(nil)
	}
	slurm.End(nil)
	clk.Advance(120 * time.Millisecond)
	return nil
}

// pullAndPreflight records the step pull images, whose target shows the
// bytes of the image, next to the hidden step preflight.
func pullAndPreflight(ctx context.Context, clk *clock) {
	const size = 96 << 20
	pctx, pull := progress.Start(ctx, progress.KindStep, "pull images",
		progress.WithFlags(progress.Fold|progress.ShowLines), progress.Total(1))
	_, image := progress.Start(pctx, progress.KindTarget, "ghcr.io/gsi-hpc/sind-node:latest",
		progress.Queued(), progress.Node("ghcr.io/gsi-hpc/sind-node:latest"),
		progress.Work(progress.Bytes, size))
	prep, preflight := progress.Start(ctx, progress.KindStep, "preflight", progress.WithFlags(progress.Hidden))
	clk.Advance(80 * time.Millisecond)
	image.Run()
	dockerCall(prep, clk, "docker version", 150*time.Millisecond, nil)
	dockerCall(prep, clk, "docker network inspect", 120*time.Millisecond,
		errors.New("docker network inspect sind-dev: exit status 1"))
	for done := int64(0); done < size; {
		clk.Advance(250 * time.Millisecond)
		done += 4 << 20
		image.Advance(4 << 20)
	}
	preflight.End(nil)
	clk.Advance(300 * time.Millisecond)
	image.End(nil)
	pull.End(nil)
	clk.Advance(50 * time.Millisecond)
}

// dockerCall records a hidden docker call that waits for a slot of the
// client, runs for d and ends with err. A call that fails carries docker's
// exit code.
func dockerCall(ctx context.Context, clk *clock, name string, d time.Duration, err error) {
	_, call := progress.Start(ctx, progress.KindCall, name, progress.Queued(), progress.WithFlags(progress.Hidden))
	clk.Advance(15 * time.Millisecond)
	call.Run()
	clk.Advance(d)
	if err != nil {
		call.End(err, progress.Exit(1))
		return
	}
	call.End(nil)
}

// step is something that happens at an offset after the nodes were
// started.
type step struct {
	at time.Duration
	do func()
}

// fate is what becomes of a node.
type fate int

const (
	fateOK       fate = iota // ready in time
	fateTimeout              // its wait runs out of time
	fateCanceled             // stopped while it waited
	fateQueued               // never started
)

// waitLimit is the --wait limit of the recorded commands.
const waitLimit = 300 * time.Second

// cancelAt is when what still waits is stopped: the interrupt, or the
// first node that fails. Zero when nothing is.
func (rec recording) cancelAt() time.Duration {
	if rec.result == canceledRun {
		return 38 * time.Second
	}
	for _, n := range rec.nodes {
		if n.ready == 0 {
			return waitLimit
		}
	}
	return 0
}

func (rec recording) fate(n node) fate {
	cancel := rec.cancelAt()
	switch {
	case n.queued:
		return fateQueued
	case n.ready == 0:
		return fateTimeout
	case cancel == 0 || n.ready < cancel:
		return fateOK
	}
	return fateCanceled
}

// nodesStep records the step nodes: a target for each node, announced
// queued before the first runs, and under each node the docker calls, the
// wait ready with the probe it is at, and the call setup. It returns the
// error of the node that failed first, or the interrupt.
func (rec recording) nodesStep(ctx context.Context, clk *clock) error {
	nctx, nodes := progress.Start(ctx, progress.KindStep, "nodes",
		progress.WithFlags(progress.Fold), progress.Total(len(rec.nodes)))
	ctxs := make([]context.Context, len(rec.nodes))
	targets := make([]*progress.Span, len(rec.nodes))
	for i, n := range rec.nodes {
		ctxs[i], targets[i] = progress.Start(nctx, progress.KindTarget, n.name,
			progress.Queued(), progress.Node(n.name), progress.Role(n.role))
	}
	for i, n := range rec.nodes {
		if rec.fate(n) == fateQueued {
			continue
		}
		targets[i].Run()
		dockerCall(ctxs[i], clk, "docker create", 700*time.Millisecond, nil)
		dockerCall(ctxs[i], clk, "docker start", 350*time.Millisecond, nil)
	}
	// The wait counts from here, after the docker calls.
	var (
		steps    []step
		firstErr error
		cancel   = classed{context.Canceled, progress.ClassCanceled}
	)
	for i, n := range rec.nodes {
		f := rec.fate(n)
		if f == fateQueued {
			continue
		}
		_, wait := progress.Start(ctxs[i], progress.KindWait, "ready", progress.Timeout(waitLimit))
		wait.Update(progress.Message("container"))
		// The probes pass one after the other until the node is ready,
		// or until it settles at the one that fails.
		until := n.ready
		if f != fateOK {
			until = min(rec.cancelAt(), 60*time.Second)
		}
		for p, probe := range []string{"systemd", "sshd", "munge"} {
			steps = append(steps, step{until * time.Duration(p+1) / 4, func() { wait.Update(progress.Message(probe)) }})
		}
		switch f {
		case fateOK:
			steps = append(steps, step{n.ready, func() {
				wait.End(nil)
				sctx, setup := progress.Start(ctxs[i], progress.KindCall, "setup")
				dockerCall(sctx, clk, "docker exec", 600*time.Millisecond, nil)
				clk.Advance(400 * time.Millisecond)
				setup.End(nil)
				targets[i].End(nil)
			}})
		case fateTimeout:
			err := fmt.Errorf("node %s: %w; last probe error: probe munge: munge not ready", n.name, timedOut{})
			steps = append(steps, step{waitLimit, func() {
				wait.End(fmt.Errorf("%w; last probe error: probe munge: munge not ready", timedOut{}))
				targets[i].End(err)
				if firstErr == nil {
					firstErr = err
				}
			}})
		case fateCanceled:
			steps = append(steps, step{rec.cancelAt() + 20*time.Millisecond, func() {
				wait.End(cancel)
				targets[i].End(classed{fmt.Errorf("node %s: %w", n.name, cancel), progress.ClassCanceled})
			}})
		}
	}
	slices.SortStableFunc(steps, func(a, b step) int { return int(a.at - b.at) })
	var at time.Duration
	for _, s := range steps {
		clk.Advance(s.at - at)
		at = s.at
		s.do()
	}
	if rec.result == canceledRun {
		for i, n := range rec.nodes {
			if rec.fate(n) == fateQueued {
				targets[i].End(cancel)
			}
		}
		firstErr = cancel
	}
	nodes.End(firstErr)
	return firstErr
}

// rollback records the step rollback, which runs after a failure or an
// interrupt and removes what the command made.
func (rec recording) rollback(ctx context.Context, clk *clock) {
	clk.Advance(30 * time.Millisecond)
	rctx, step := progress.Start(context.WithoutCancel(ctx), progress.KindStep, "rollback")
	for _, n := range rec.nodes {
		if n.queued {
			continue
		}
		dockerCall(rctx, clk, "docker rm", 450*time.Millisecond, nil)
	}
	dockerCall(rctx, clk, "docker network rm", 380*time.Millisecond, nil)
	step.End(nil)
	clk.Advance(20 * time.Millisecond)
}

var (
	idKey = regexp.MustCompile(`"(span|parent)":"([0-9a-f]{16})"`)
	timeK = regexp.MustCompile(`"time":"([^"]+)"`)
)

// normalizeIDs replaces the random span ids of a log by 0000000000000001
// and on, in the order they appear, as the golden log of go-clikit names
// them #1, #2. The trace line keeps its parent: that is the caller's span.
func normalizeIDs(log []byte) []byte {
	ids := map[string]string{}
	lines := strings.Split(string(log), "\n")
	for i, line := range lines {
		if strings.Contains(line, `"type":"trace"`) {
			continue
		}
		lines[i] = idKey.ReplaceAllStringFunc(line, func(m string) string {
			sub := idKey.FindStringSubmatch(m)
			id, ok := ids[sub[2]]
			if !ok {
				id = fmt.Sprintf("%016x", len(ids)+1)
				ids[sub[2]] = id
			}
			return fmt.Sprintf(`"%s":"%s"`, sub[1], id)
		})
	}
	return []byte(strings.Join(lines, "\n"))
}

// interleave merges the logs of several runs by the time of their events;
// the trace line of a run goes with its first event.
func interleave(logs ...[]byte) []byte {
	type entry struct {
		at       string
		run, idx int
		line     string
	}
	var all []entry
	for run, log := range logs {
		lines := strings.Split(strings.TrimSuffix(string(log), "\n"), "\n")
		var first string
		for _, line := range lines {
			if m := timeK.FindStringSubmatch(line); m != nil {
				first = m[1]
				break
			}
		}
		for idx, line := range lines {
			at := first
			if m := timeK.FindStringSubmatch(line); m != nil {
				at = m[1]
			}
			all = append(all, entry{at, run, idx, line})
		}
	}
	slices.SortFunc(all, func(a, b entry) int {
		return cmpAll(a.at, b.at, a.run, b.run, a.idx, b.idx)
	})
	var out bytes.Buffer
	for _, e := range all {
		out.WriteString(e.line + "\n")
	}
	return out.Bytes()
}

// cmpAll compares the pairs of keys in order; the times are RFC 3339 with
// a fixed number of digits, so that they sort as text.
func cmpAll(at, bt string, ar, br, ai, bi int) int {
	if c := strings.Compare(at, bt); c != 0 {
		return c
	}
	if ar != br {
		return ar - br
	}
	return ai - bi
}
