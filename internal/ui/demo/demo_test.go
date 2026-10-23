// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/probe"
)

func TestDemo_Setup(t *testing.T) {
	st := store.New()
	d := New(st, false)
	d.Setup()
	sn := st.Snapshot()

	assert.Len(t, sn.Realms, 2)
	assert.WithinDuration(t, time.Now(), d.Now(), time.Second)
	require.NotNil(t, sn.Doctor)
	var warnings int
	for _, c := range sn.Doctor.Checks {
		if c.Status == "warning" {
			warnings++
		}
	}
	assert.Equal(t, 1, warnings, "one warning")

	require.Len(t, sn.Runs, 7, "a few runs that came before")
	status := map[string]int{}
	for _, r := range sn.Runs {
		require.True(t, r.Done())
		status[r.Status]++
	}
	assert.Equal(t, map[string]int{store.StatusOK: 5, store.StatusFailed: 1, store.StatusCanceled: 1}, status)
	assert.Empty(t, sn.Tasks)
	assert.Empty(t, sn.Locks)

	// Runs of the demo are new ones, not those of the history.
	d.sim.Feed(nil)
	assert.Len(t, st.Snapshot().Runs, 7)
}

func TestDemo_Still(t *testing.T) {
	first, second := store.New(), store.New()
	New(first, true).Setup()
	New(second, true).Setup()
	a, b := first.Snapshot(), second.Snapshot()
	assert.Equal(t, a.Runs, b.Runs, "the same runs on every start")
	assert.Equal(t, a.Doctor, b.Doctor)
	assert.Equal(t, a.Tasks, b.Tasks)

	d := New(store.New(), true)
	assert.Equal(t, Epoch, d.Now())
	require.Len(t, a.Runs, 8)
	mid, ok := a.Run(runID(midRunIndex))
	require.True(t, ok)
	assert.False(t, mid.Done(), "a run that goes on")
	assert.Equal(t, Epoch.Add(-midRunAge), mid.Start)
	require.Len(t, a.Tasks, 1)
	task, ok := a.Task(midRunTask)
	require.True(t, ok)
	assert.Equal(t, mid.Trace, task.Trace, "its task finds the run")
	assert.Equal(t, store.TaskRunning, task.Status)
	assert.NotNil(t, a.Locks["ci"])
	assert.Equal(t, 5, len(a.Clusters["sind"])+len(a.Clusters["ci"]), "nightly is there, half made")
	assert.Equal(t, cluster.StateMixed, a.ClustersOf("ci")[0].State)
	for _, r := range a.Runs {
		assert.False(t, r.Start.After(Epoch))
		if r.End != nil {
			assert.False(t, r.End.After(Epoch))
		}
	}

	_, err := d.Starter().Start(run.Spec{Argv: []string{"sind", "power", "on"}})
	assert.Error(t, err, "a still demo plays nothing")
}

func TestDemo_Exec(t *testing.T) {
	live, still := New(store.New(), false).Exec().(*Exec), New(store.New(), true).Exec().(*Exec)
	assert.NotZero(t, live.Every)
	assert.Zero(t, still.Every)
	assert.Equal(t, Epoch, still.Now())
}

func TestDemo_SaveFile(t *testing.T) {
	d := New(store.New(), false)
	require.NoError(t, d.SaveFile("/x.yaml", []byte("data")))
	assert.Equal(t, []byte("data"), d.sim.files["/x.yaml"])
}

func TestDemo_DataDirectories(t *testing.T) {
	d := New(store.New(), false)
	_, err := Stat("/home/demo/sind/hpc")
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.NoError(t, d.MkdirAll("/home/demo/sind/hpc"))
}

func TestDemo_Refresh(t *testing.T) {
	st := store.New()
	d := New(st, false)
	d.Refresh("sind")
	assert.Len(t, st.Snapshot().Realms, 2)
}

func TestDemo_Doctor(t *testing.T) {
	st := store.New()
	d := New(st, false)
	d.checkTime = time.Millisecond
	d.Setup()
	before := st.Snapshot().Doctor.Time

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	d.RequestDoctor()
	d.RequestDoctor() // never blocks
	require.Eventually(t, func() bool { return st.Snapshot().Doctor.Time.After(before) && !st.Snapshot().Doctor.Running }, 5*time.Second, time.Millisecond)
	cancel()
	<-done
}

func TestDemo_DoctorWithoutReport(t *testing.T) {
	st := store.New()
	d := New(st, true)
	d.checkTime = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.checkHost(ctx); close(done) }()
	require.Eventually(t, func() bool { d := st.Snapshot().Doctor; return d != nil && d.Running }, 5*time.Second, time.Millisecond)
	cancel() // the checks end with the demo
	<-done
	assert.Equal(t, Epoch, st.Snapshot().Doctor.Time)
	assert.False(t, st.Snapshot().Doctor.Running)
}

func TestDemo_Drifts(t *testing.T) {
	st := store.New()
	d := New(st, false)
	d.driftEvery = time.Millisecond
	d.Setup()
	slurmd := func() bool {
		for _, n := range st.Snapshot().Status("sind", "dev").Nodes {
			if n.Name == "worker-4.dev" {
				return n.Health.Services[probe.ServiceSlurmd]
			}
		}
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	require.Eventually(t, slurmd, 5*time.Second, time.Millisecond, "a service flips by itself")
	cancel()
	<-done

	// Where nothing can flip, nothing is published.
	for _, n := range d.world.nodesOf("sind", "dev") {
		d.world.apply("sind", opRemove, n)
	}
	d.Refresh("sind")
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
}

func TestDemo_StillStandsStill(t *testing.T) {
	st := store.New()
	d := New(st, true)
	d.driftEvery = time.Millisecond
	d.Setup()
	before := st.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	assert.Equal(t, before.Statuses, st.Snapshot().Statuses, "nothing moves")
}

func TestDemo_Images(t *testing.T) {
	ctx := t.Context()
	img, local, err := InspectImage(ctx, config.DefaultImage)
	require.NoError(t, err)
	assert.True(t, local)
	assert.Equal(t, slurmNew, img.Labels[cluster.LabelSlurmVersion])
	_, local, _ = InspectImage(ctx, "elsewhere:1")
	assert.False(t, local)

	v, err := DiscoverVersion(ctx, "sind-node:dev")
	require.NoError(t, err)
	assert.Equal(t, slurmNew, v)
	_, err = DiscoverVersion(ctx, "elsewhere:1")
	assert.ErrorContains(t, err, "the demo pulls nothing")
}

func TestDemo_InspectContainer(t *testing.T) {
	d := New(store.New(), true)
	ctx := t.Context()
	info, err := d.InspectContainer(ctx, "sind-dev-worker-5")
	require.NoError(t, err)
	assert.Equal(t, int64(2e9), info.HostConfig.NanoCPUs)
	assert.Equal(t, int64(1<<30), info.HostConfig.Memory)
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:"+slurmNew, info.ImageRef)

	info, err = d.InspectContainer(ctx, "sind-legacy-worker-0")
	require.NoError(t, err)
	assert.Equal(t, int64(1e9), info.HostConfig.NanoCPUs, "sind's defaults")
	assert.Equal(t, int64(512<<20), info.HostConfig.Memory)

	_, err = d.InspectContainer(ctx, "sind-dev-worker-99")
	assert.EqualError(t, err, "no such container: sind-dev-worker-99")
}
