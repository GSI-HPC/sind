// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"sync"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/collect"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/state"
)

// The Env keys that the collectors provide, for the parts that act on them.
// The values are plain funcs, so that no part imports another.
const (
	// ColRefreshKey holds a func(realm string) that reads that realm again
	// now, such as after a task that changed it ended.
	ColRefreshKey = "collect.refresh"
	// ColDoctorKey holds a func() that runs the host checks again.
	ColDoctorKey = "collect.doctor"
)

// colKey is the Env key of the collectors that Setup made.
type colKey struct{}

// colSet is what the collect service builds in Setup and runs.
type colSet struct {
	events     *collect.Events
	sind       *collect.Sind
	lock       *collect.Lock
	monitoring *collect.Monitoring
	status     *collect.Status
	doctor     *collect.Doctor
}

func init() {
	RegisterService(colService())
	RegisterService(colActivityService())
}

// colService is the service that fills the store with what Docker and the
// host know (package collect). Its Setup provides ColRefreshKey and
// ColDoctorKey and registers the Watcher that tells which cluster pages
// are open.
func colService() Service {
	return Service{Name: "collect", Setup: colSetup, Run: colRun}
}

func colSetup(env *Env) error {
	client := docker.NewClient(env.Exec)
	bus := collect.NewBus()
	watched := collect.NewWatched()
	set := &colSet{
		events:     &collect.Events{Exec: env.Exec, Bus: bus, Log: env.Log},
		sind:       &collect.Sind{Store: env.Store, Client: client, Bus: bus, Log: env.Log},
		lock:       &collect.Lock{Store: env.Store, Client: client, Bus: bus, Log: env.Log},
		monitoring: &collect.Monitoring{Store: env.Store, Client: client, Bus: bus, Log: env.Log},
		status:     &collect.Status{Store: env.Store, Client: client, Bus: bus, Watched: watched, Log: env.Log},
		doctor:     collect.NewDoctor(env.Store, client),
	}
	env.Put(colKey{}, set)
	env.Put(ColRefreshKey, func(realm string) { bus.Publish(collect.Event{Kind: collect.KindRefresh, Realm: realm}) })
	env.Put(ColDoctorKey, set.doctor.Request)
	env.AddWatcher(watched.Watch)
	return nil
}

func colRun(ctx context.Context, env *Env) {
	set, ok := env.Get(colKey{}).(*colSet)
	if !ok {
		return
	}
	var wg sync.WaitGroup
	for _, run := range []func(context.Context){
		set.events.Run, set.sind.Run, set.lock.Run, set.monitoring.Run, set.status.Run, set.doctor.Run,
	} {
		wg.Go(func() { run(ctx) })
	}
	wg.Wait()
}

// colActivityService follows the activity log that sind commands append to,
// which is where the runs of the store come from.
func colActivityService() Service {
	return Service{Name: "collect-activity", Run: func(ctx context.Context, env *Env) {
		path, err := state.ActivityLogPath()
		if err != nil {
			env.Log.Warn("sind ui shows no activity: no state directory", "err", err)
			return
		}
		activity.Follow(ctx, env.Store, path)
	}}
}
