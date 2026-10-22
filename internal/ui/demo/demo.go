// SPDX-License-Identifier: LGPL-3.0-or-later

// Package demo is the made-up Docker host that sind ui --demo shows: two
// realms with clusters of different shapes and states, a mesh, a monitoring
// stack, the host checks with one warning, and a few runs of the activity
// log. Nothing in it touches Docker, starts a process or writes a file.
//
// A World holds the clusters and writes them to the store as the collectors
// would. A Sim plays the commands of the dialogs as the spans of a go-clikit
// progress.Bus: its event log goes through the real reducer of
// internal/ui/activity, so the progress display is the real one, and the
// world changes as the command would change Docker. An Exec answers the log
// streams. Demo ties them together for the server.
//
// A still demo (Options.Still) stands at Epoch, plays no command and moves
// nothing, so that its pages are the same on every run.
package demo

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/state"
)

// Epoch is the time a still demo stands at: a fixed instant, in UTC, so that
// the pages of sind ui --demo --still read the same on every run.
var Epoch = time.Date(2026, time.October, 21, 9, 15, 0, 0, time.UTC)

const (
	// driftEvery is how often a service of the demo flips.
	driftEvery = 20 * time.Second
	// checkTime is how long the host checks take.
	checkTime = 1200 * time.Millisecond
)

// Speed is how many times faster than real time the commands of a demo run;
// New reads it. Tests raise it.
var Speed = 2.0

// Demo is the demo host.
type Demo struct {
	st    *store.Store
	world *World
	sim   *Sim
	still bool

	doctor chan struct{}
	// The intervals of the demo's motion; tests shorten them.
	driftEvery, checkTime time.Duration
}

// New returns a demo that writes to st. A still demo stands at Epoch.
func New(st *store.Store, still bool) *Demo {
	d := &Demo{st: st, world: Seeded(), still: still, doctor: make(chan struct{}, 1), driftEvery: driftEvery, checkTime: checkTime}
	var clk clock = liveClock{speed: Speed}
	if still {
		clk = &manualClock{t: Epoch}
	}
	d.sim = NewSim(st, d.world, clk)
	d.sim.seq = 100
	d.sim.frozen = still
	return d
}

// Now is the time of the demo: Epoch for a still one.
func (d *Demo) Now() time.Time {
	if d.still {
		return Epoch
	}
	return time.Now()
}

// Starter returns the simulator of the commands.
func (d *Demo) Starter() run.Starter { return d.sim }

// SaveFile keeps the config a dialog saves, in memory.
func (d *Demo) SaveFile(path string, data []byte) error { return d.sim.SaveFile(path, data) }

// MkdirAll makes no directory: the clusters of the demo keep no data.
func (d *Demo) MkdirAll(string) error { return nil }

// Stat finds no file, so that a dialog offers to create a data directory
// as it does for a new one.
func Stat(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }

// demoImages are the images that the demo's Docker has, by reference: the
// default image, with the release of the demo's new clusters, the image
// of an older release, and one built without a label.
var demoImages = map[string]docker.Image{
	config.DefaultImage:                     {ID: "sha256:d1", Labels: docker.Labels{cluster.LabelSlurmVersion: slurmNew}},
	"ghcr.io/gsi-hpc/sind-node:" + slurmNew: {ID: "sha256:d1", Labels: docker.Labels{cluster.LabelSlurmVersion: slurmNew}},
	"ghcr.io/gsi-hpc/sind-node:" + slurmOld: {ID: "sha256:d2", Labels: docker.Labels{cluster.LabelSlurmVersion: slurmOld}},
	"sind-node:dev":                         {ID: "sha256:d3"},
}

// InspectImage looks at an image of the demo's Docker.
func InspectImage(_ context.Context, image string) (docker.Image, bool, error) {
	img, ok := demoImages[image]
	return img, ok, nil
}

// DiscoverVersion finds the Slurm release of an image of the demo's
// Docker, as a check does: the demo's newest one. The demo pulls nothing,
// so another image fails.
func DiscoverVersion(_ context.Context, image string) (string, error) {
	if _, ok := demoImages[image]; !ok {
		return "", fmt.Errorf("pulling %s: the demo pulls nothing", image)
	}
	return slurmNew, nil
}

// Exec returns the executor that answers the log streams.
func (d *Demo) Exec() cmdexec.Executor {
	e := &Exec{Now: d.Now}
	if !d.still {
		e.Every = 800 * time.Millisecond
	}
	return e
}

// Refresh publishes the world again, as a read of Docker would.
func (d *Demo) Refresh(string) { d.world.Publish(d.st) }

// RequestDoctor asks for the host checks to run again. It never blocks.
func (d *Demo) RequestDoctor() {
	select {
	case d.doctor <- struct{}{}:
	default:
	}
}

// Setup fills the store: the world, the host checks and the runs that came
// before. A still demo also has a run that goes on.
func (d *Demo) Setup() {
	now := d.Now()
	d.world.Publish(d.st)
	d.st.SetDoctor(doctorAt(now.Add(-3 * time.Minute)))
	for _, log := range history(now) {
		d.sim.Feed(log)
	}
	if d.still {
		d.startMidRun(now)
	}
}

// startMidRun shows the create cluster nightly of the realm ci half way: its
// run, its task, the lock it holds and the nodes it has made.
func (d *Demo) startMidRun(now time.Time) {
	d.sim.Feed(midRun(now))
	d.st.AddTask(store.Task{
		ID: midRunTask, Action: "create-cluster", Realm: "ci", Cluster: "nightly",
		Display: "sind --realm ci create cluster --config " + ConfigDir + "/nightly.yaml",
		Trace:   traceHex(traceID(midRunIndex)), Start: now.Add(-midRunAge), Status: store.TaskRunning,
	})
	d.st.SetLock("ci", &state.LockHolder{Command: "sind --realm ci create cluster", Host: "demo", PID: 4242, Since: now.Add(-midRunAge)})
	for _, n := range nodeSet("nightly", 0, 0, 0) {
		d.world.apply("ci", opCreate, n)
	}
	d.world.apply("ci", opStart, node{name: "controller", cluster: "nightly", role: "controller"})
	d.world.Publish(d.st)
}

// Run moves the demo until ctx ends: the host checks when asked, and, but
// for a still demo, a service that flips now and then.
func (d *Demo) Run(ctx context.Context) {
	var drift <-chan time.Time
	if !d.still {
		t := time.NewTicker(d.driftEvery)
		defer t.Stop()
		drift = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.doctor:
			d.checkHost(ctx)
		case <-drift:
			if d.world.Drift() {
				d.world.Publish(d.st)
			}
		}
	}
}

// checkHost pretends to run the host checks again.
func (d *Demo) checkHost(ctx context.Context) {
	snap := d.st.Snapshot().Doctor
	running := &store.DoctorReport{Running: true, Checks: Checks()}
	if snap != nil {
		running.Time = snap.Time
	}
	d.st.SetDoctor(running)
	t := time.NewTimer(d.checkTime)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
	d.st.SetDoctor(doctorAt(d.Now()))
}
