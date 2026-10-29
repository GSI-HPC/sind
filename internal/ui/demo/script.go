// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
)

// The scripts below play what sind's commands do, as spans of a
// go-clikit progress.Bus, whose Log writes the event log that sind ui
// reads. The span structure follows pkg/cluster (create.go, delete.go,
// worker_remove.go): the steps pull images, preflight (hidden), nodes,
// mesh registration and slurm, a target for each node, a wait "ready" with
// its limit, hidden docker calls, and the step rollback after a failure or
// an interrupt.

// waitLimit is the --wait limit of the commands.
const waitLimit = 300 * time.Second

// node is a node a script works on.
type node struct {
	name    string
	cluster string
	role    string
	// ready is when the node's probes pass, counted from the start of the
	// wait; zero for a node that never gets ready within the limit.
	ready time.Duration
	// reason is why Slurm drains the node, for opDrain; unit is the
	// service that opRestart restarts.
	reason, unit string
	// cpus and memoryMB are the size of the node when it is made; 0 for
	// the size of its cluster.
	cpus, memoryMB int
}

// Effects of a script on the world, for emit.
const (
	opCreate  = "create"  // the container exists, stopped
	opStart   = "start"   // it runs, its services are not up yet
	opReady   = "ready"   // its services are up
	opStop    = "stop"    // it is stopped
	opPause   = "pause"   // its processes are frozen
	opUnpause = "unpause" // they run again
	opRemove  = "remove"  // the container is gone
	opConnect = "connect" // an outside container joins the mesh network
	opDisconn = "disconn" // it leaves
	opMonitor = "monitor" // the realm's monitoring stack exists
	opDrain   = "drain"   // Slurm starts no new job on it
	opResume  = "resume"  // Slurm schedules on it again
	opRestart = "restart" // one of its services comes up again
)

// play is what a script plays with: a clock, and a function that tells the
// world what the script does to it (nil for a recorded run).
type play struct {
	clk  clock
	emit func(op string, n node)
}

func (p play) do(op string, n node) {
	if p.emit != nil {
		p.emit(op, n)
	}
}

// pause lets d pass without a way out: a short pause of a step, which an
// interrupt waits for, as sind waits for a docker call.
func (p play) pause(ctx context.Context, d time.Duration) {
	_ = p.clk.Sleep(context.WithoutCancel(ctx), d)
}

// classed is an error with the progress class sind gives it.
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

// stopped is the error of work an interrupt ended.
var stopped = classed{context.Canceled, progress.ClassCanceled}

// interrupted returns stopped, once ctx has ended.
func interrupted(ctx context.Context) error {
	if ctx.Err() != nil {
		return stopped
	}
	return nil
}

// call plays a hidden docker call that waits for a slot of the client, runs
// for d and ends with fail. A call that fails carries docker's exit code.
func (p play) call(ctx context.Context, name string, d time.Duration, fail error) {
	_, c := progress.Start(ctx, progress.KindCall, name, progress.Queued(), progress.Flagged(progress.FlagHidden))
	p.pause(ctx, 15*time.Millisecond)
	c.Run()
	p.pause(ctx, d)
	if fail != nil {
		c.End(fail, progress.Exit(1))
		return
	}
	c.End(nil)
}

// preflight plays the hidden step preflight: the checks before anything is
// made.
func (p play) preflight(ctx context.Context) (context.Context, *progress.Span) {
	prep, step := progress.Start(ctx, progress.KindStep, "preflight", progress.Flagged(progress.FlagHidden))
	p.call(prep, "docker version", 150*time.Millisecond, nil)
	p.call(prep, "docker network inspect", 120*time.Millisecond,
		errors.New("docker network inspect sind-dev: exit status 1"))
	return prep, step
}

// pull plays the step pull images, whose target counts the bytes of the
// image as sind does, with its docker call, and says when docker extracts
// it, next to the preflight, which waits for it. A fail ends the pull with
// it.
func (p play) pull(ctx context.Context, image string, fail error) error {
	const (
		ticks = 8
		size  = 412_000_000
	)
	pctx, pull := progress.Start(ctx, progress.KindStep, "pull images",
		progress.Flagged(progress.FlagFold|progress.FlagShowLines), progress.Total(1))
	ictx, img := progress.Start(pctx, progress.KindTarget, image,
		progress.Queued(), progress.Node(image))
	p.pause(ctx, 80*time.Millisecond)
	img.Run()
	prep, preflight := p.preflight(ctx)
	_, wait := progress.Start(prep, progress.KindWait, "images")
	_, call := progress.Start(ictx, progress.KindCall, "docker pull", progress.Flagged(progress.FlagHidden))
	if fail != nil {
		p.pause(ctx, 700*time.Millisecond)
		call.End(fail, progress.Exit(1))
		wait.End(context.Canceled)
		preflight.End(nil)
		img.End(fail)
		pull.End(fail)
		return fail
	}
	img.Update(progress.Work(progress.UnitBytes, size))
	for i := range ticks {
		p.pause(ctx, 900*time.Millisecond)
		img.SetAmount(size * int64(i+1) / ticks)
	}
	img.Update(progress.Message("extracting"))
	p.pause(ctx, 700*time.Millisecond)
	call.End(nil)
	img.Update(progress.Message(""))
	wait.End(nil)
	preflight.End(nil)
	p.pause(ctx, 300*time.Millisecond)
	img.End(nil)
	pull.End(nil)
	p.pause(ctx, 50*time.Millisecond)
	return nil
}

// step is something that happens at an offset after the wait began.
type step struct {
	at time.Duration
	do func()
}

// nodesStep plays the step nodes: a target for each node, announced queued
// before the first runs, and under each node the docker calls, the wait
// ready with the probe it is at, and the call setup. It returns the error
// of the node that failed first, or the interrupt.
func (p play) nodesStep(ctx context.Context, nodes []node) error {
	nctx, nodesSpan := progress.Start(ctx, progress.KindStep, "nodes",
		progress.Flagged(progress.FlagFold), progress.Total(len(nodes)))
	ctxs := make([]context.Context, len(nodes))
	targets := make([]*progress.Span, len(nodes))
	for i, n := range nodes {
		ctxs[i], targets[i] = progress.Start(nctx, progress.KindTarget, n.name,
			progress.Queued(), progress.Node(n.name), progress.Role(n.role))
	}
	for i, n := range nodes {
		targets[i].Run()
		p.call(ctxs[i], "docker create", 700*time.Millisecond, nil)
		p.do(opCreate, n)
		p.call(ctxs[i], "docker start", 350*time.Millisecond, nil)
		p.do(opStart, n)
	}
	// The wait counts from here, after the docker calls.
	var (
		steps    []step
		firstErr error
		waits    = make([]*progress.Span, len(nodes))
		done     = make([]bool, len(nodes))
	)
	for i, n := range nodes {
		_, wait := progress.Start(ctxs[i], progress.KindWait, "ready", progress.Timeout(waitLimit))
		wait.Update(progress.Message("container"))
		waits[i] = wait
		// The probes pass one after the other until the node is ready, or
		// until it settles at the one that fails.
		until := n.ready
		if until == 0 {
			until = 60 * time.Second
		}
		for k, probe := range []string{"systemd", "sshd", "munge"} {
			steps = append(steps, step{until * time.Duration(k+1) / 4, func() { wait.Update(progress.Message(probe)) }})
		}
		if n.ready == 0 {
			err := fmt.Errorf("node %s: %w; last probe error: probe munge: munge not ready", n.name, timedOut{})
			steps = append(steps, step{waitLimit, func() {
				wait.End(fmt.Errorf("%w; last probe error: probe munge: munge not ready", timedOut{}))
				targets[i].End(err)
				done[i], firstErr = true, err
			}})
			continue
		}
		steps = append(steps, step{n.ready, func() {
			wait.End(nil)
			p.do(opReady, n)
			sctx, setup := progress.Start(ctxs[i], progress.KindCall, "setup")
			p.call(sctx, "docker exec", 600*time.Millisecond, nil)
			p.pause(ctx, 400*time.Millisecond)
			setup.End(nil)
			targets[i].End(nil)
			done[i] = true
		}})
	}
	slices.SortStableFunc(steps, func(a, b step) int { return int(a.at - b.at) })
	var at time.Duration
	for _, s := range steps {
		if err := p.clk.Sleep(ctx, s.at-at); err != nil {
			firstErr = stopped
			break
		}
		at = s.at
		s.do()
		if firstErr != nil {
			break
		}
	}
	// What still waits is stopped, by the interrupt or by the failure.
	p.pause(ctx, 20*time.Millisecond)
	for i, n := range nodes {
		if !done[i] {
			waits[i].End(stopped)
			targets[i].End(classed{fmt.Errorf("node %s: %w", n.name, stopped), progress.ClassCanceled})
		}
	}
	nodesSpan.End(firstErr)
	return firstErr
}

// rollback plays the step rollback, which runs after a failure or an
// interrupt and removes what the command made.
func (p play) rollback(ctx context.Context, nodes []node) {
	p.pause(ctx, 30*time.Millisecond)
	rctx, rollback := progress.Start(context.WithoutCancel(ctx), progress.KindStep, "rollback")
	for _, n := range nodes {
		p.call(rctx, "docker rm", 450*time.Millisecond, nil)
		p.do(opRemove, n)
	}
	p.call(rctx, "docker network rm", 380*time.Millisecond, nil)
	rollback.End(nil)
	p.pause(ctx, 20*time.Millisecond)
}

// meshRegistration plays the step mesh registration.
func (p play) meshRegistration(ctx context.Context) {
	mctx, mesh := progress.Start(ctx, progress.KindStep, "mesh registration")
	p.call(mctx, "docker exec", 300*time.Millisecond, nil)
	p.pause(ctx, 900*time.Millisecond)
	mesh.End(nil)
}

// each plays a step with a target for each node, which work plays one after
// the other. A target that comes after the interrupt is stopped without
// running; the step ends with the interrupt.
func (p play) each(ctx context.Context, name string, nodes []node, work func(ctx context.Context, i int)) error {
	sctx, sp := progress.Start(ctx, progress.KindStep, name,
		progress.Flagged(progress.FlagFold), progress.Total(len(nodes)))
	ctxs := make([]context.Context, len(nodes))
	targets := make([]*progress.Span, len(nodes))
	for i, n := range nodes {
		ctxs[i], targets[i] = progress.Start(sctx, progress.KindTarget, n.name,
			progress.Queued(), progress.Node(n.name), progress.Role(n.role))
	}
	var err error
	for i := range nodes {
		if err == nil {
			err = interrupted(ctx)
		}
		if err != nil {
			targets[i].End(err)
			continue
		}
		targets[i].Run()
		work(ctxs[i], i)
		targets[i].End(nil)
	}
	sp.End(err)
	return err
}

// slurm plays the step slurm: the daemons of each node start.
func (p play) slurm(ctx context.Context, nodes []node) error {
	return p.each(ctx, "slurm", nodes, func(ctx context.Context, i int) {
		p.call(ctx, "docker exec", 400*time.Millisecond, nil)
		_, wait := progress.Start(ctx, progress.KindWait, "ready", progress.Timeout(272*time.Second))
		p.pause(ctx, 1500*time.Millisecond)
		wait.Update(progress.Message(daemonOf(nodes[i].role)))
		p.pause(ctx, 2500*time.Millisecond)
		wait.End(nil)
	})
}

// fate wraps what ends a command: an interrupt is canceled, whatever the
// error says.
func fate(err error) error {
	if errors.Is(err, context.Canceled) {
		return classed{err, progress.ClassCanceled}
	}
	return err
}

// createSpec says what a create does.
type createSpec struct {
	// image is pulled first; empty pulls nothing, as create worker.
	image string
	// pullFail is why the pull fails, if it does.
	pullFail error
	// mesh registers the cluster at the mesh, as create cluster does.
	mesh  bool
	nodes []node
}

// create returns the work of a run that plays create.
func create(s createSpec) func(context.Context, play) error {
	return func(ctx context.Context, p play) error { return p.create(ctx, s) }
}

// create plays create cluster and create worker.
func (p play) create(ctx context.Context, s createSpec) error {
	if s.image != "" {
		if err := p.pull(ctx, s.image, s.pullFail); err != nil {
			return fmt.Errorf("pulling %s: %w", s.image, err)
		}
	} else {
		_, preflight := p.preflight(ctx)
		preflight.End(nil)
	}
	if err := p.nodesStep(ctx, s.nodes); err != nil {
		p.rollback(ctx, s.nodes)
		return fate(fmt.Errorf("setting up nodes: %w", err))
	}
	if s.mesh {
		p.meshRegistration(ctx)
	}
	if err := p.slurm(ctx, s.nodes); err != nil {
		p.rollback(ctx, s.nodes)
		return fate(fmt.Errorf("starting slurm: %w", err))
	}
	p.pause(ctx, 120*time.Millisecond)
	return nil
}

// remove plays the steps that remove nodes: each target a container, then
// the network of the clusters, and with last the mesh of the realm.
func (p play) remove(ctx context.Context, nodes []node, volumes, last bool) error {
	mctx, dereg := progress.Start(ctx, progress.KindStep, "mesh deregistration")
	p.call(mctx, "docker exec", 300*time.Millisecond, nil)
	dereg.End(nil)
	err := p.each(ctx, "nodes", nodes, func(ctx context.Context, i int) {
		p.call(ctx, "docker rm", 450*time.Millisecond, nil)
		p.do(opRemove, nodes[i])
	})
	if err != nil {
		return fate(fmt.Errorf("removing nodes: %w", err))
	}
	nctx, network := progress.Start(ctx, progress.KindStep, "network")
	p.call(nctx, "docker network rm", 380*time.Millisecond, nil)
	network.End(nil)
	if volumes {
		vctx, vols := progress.Start(ctx, progress.KindStep, "volumes")
		p.call(vctx, "docker volume rm", 300*time.Millisecond, nil)
		vols.End(nil)
	}
	if last {
		xctx, mesh := progress.Start(ctx, progress.KindStep, "mesh")
		p.call(xctx, "docker rm", 500*time.Millisecond, nil)
		p.call(xctx, "docker network rm", 380*time.Millisecond, nil)
		mesh.End(nil)
	}
	return nil
}

// deleteWorkers plays delete worker: slurm forgets the nodes, then their
// containers go.
func (p play) deleteWorkers(ctx context.Context, nodes []node) error {
	sctx, slurm := progress.Start(ctx, progress.KindStep, "slurm")
	p.call(sctx, "docker exec", 500*time.Millisecond, nil)
	p.pause(ctx, 700*time.Millisecond)
	slurm.End(nil)
	err := p.each(ctx, "nodes", nodes, func(ctx context.Context, i int) {
		p.call(ctx, "docker rm", 450*time.Millisecond, nil)
		p.do(opRemove, nodes[i])
	})
	if err != nil {
		return fate(fmt.Errorf("removing nodes: %w", err))
	}
	return nil
}

// powerStep is one thing a power command does to a node.
type powerStep struct {
	call string
	d    time.Duration
	ops  []string
}

// powerOps are the docker calls of each sind power subcommand.
var powerOps = map[string][]powerStep{
	"shutdown": {{"docker stop", 1200 * time.Millisecond, []string{opStop}}},
	"on":       {{"docker start", 600 * time.Millisecond, []string{opStart, opReady}}},
	"reboot": {
		{"docker stop", 1200 * time.Millisecond, []string{opStop}},
		{"docker start", 600 * time.Millisecond, []string{opStart, opReady}},
	},
	"cycle": {
		{"docker kill", 400 * time.Millisecond, []string{opStop}},
		{"docker start", 600 * time.Millisecond, []string{opStart, opReady}},
	},
	"cut":      {{"docker kill", 400 * time.Millisecond, []string{opStop}}},
	"freeze":   {{"docker pause", 300 * time.Millisecond, []string{opPause}}},
	"unfreeze": {{"docker unpause", 300 * time.Millisecond, []string{opUnpause}}},
}

// power plays sind power op: the step nodes, a target for each node.
func (p play) power(ctx context.Context, op string, nodes []node) error {
	steps := powerOps[op]
	err := p.each(ctx, "nodes", nodes, func(ctx context.Context, i int) {
		for _, s := range steps {
			p.call(ctx, s.call, s.d, nil)
			for _, o := range s.ops {
				p.do(o, nodes[i])
			}
		}
	})
	if err != nil {
		return fate(fmt.Errorf("power %s: %w", op, err))
	}
	return nil
}

// attach plays connect container and disconnect container: the one target
// is the container.
func (p play) attach(ctx context.Context, op string, n node) error {
	err := p.each(ctx, "connect", []node{n}, func(ctx context.Context, _ int) {
		p.call(ctx, "docker network connect", 500*time.Millisecond, nil)
		p.do(op, n)
	})
	return fate(err)
}

// generic plays a command the demo knows nothing of: one step of two
// seconds.
func (p play) generic(ctx context.Context, name string) error {
	sctx, sp := progress.Start(ctx, progress.KindStep, name)
	p.call(sctx, "docker exec", 2*time.Second, nil)
	sp.End(nil)
	return nil
}
