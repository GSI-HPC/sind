// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"context"
	"slices"
	"time"

	"github.com/GSI-HPC/go-clikit/progress/eventlog"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

const (
	// maxRuns is how many runs Follow keeps, the newest.
	maxRuns = 200
	// activeEvery is how often Follow reads the log while a run goes on,
	// and idleEvery how often otherwise.
	activeEvery = 50 * time.Millisecond
	idleEvery   = 250 * time.Millisecond
	// StaleAfter is how long a run that has not ended may go without an
	// event before Follow ends it as Unfinished: its process was killed,
	// or the log lost its end. A run that writes again goes on.
	StaleAfter = time.Hour
)

// Follow reads the event log at path as it grows and keeps the runs of st
// up to date, until ctx ends. It reads every 50 ms while a run has not
// ended and every 250 ms otherwise, and sets the runs, with the IDs of
// those that changed, only when something changed. The log may be missing
// or be rotated: see Tail. A run without an event for StaleAfter ends.
func Follow(ctx context.Context, st *store.Store, path string) {
	follow(ctx, st, path, maxRuns, activeEvery, idleEvery, StaleAfter)
}

// follow is Follow with the number of runs it keeps, its two intervals and
// the time after which a run is stale.
func follow(ctx context.Context, st *store.Store, path string, keep int, active, idle, staleAfter time.Duration) {
	red := NewReducer(keep)
	tail := eventlog.NewTail(path, red)
	busy := false
	var runs []store.Run
	for {
		var ids []string
		// A log that cannot be read is an empty one: the next poll tries again.
		if changed, _ := tail.Poll(); changed {
			if ids = red.Changed(); len(ids) > 0 {
				runs = red.Runs()
			}
		}
		marked, stale := endStale(runs, time.Now().Add(-staleAfter))
		for _, id := range stale {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			st.SetRuns(marked, ids...)
			busy = anyRunning(marked)
			runs = marked
		}
		every := idle
		if busy {
			every = active
		}
		timer := time.NewTimer(every)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// endStale returns runs with each run that has not ended and has had no
// event since before ends as Unfinished, at its last event, and the IDs of
// those runs.
func endStale(runs []store.Run, before time.Time) ([]store.Run, []string) {
	var out []store.Run
	var ids []string
	for i, r := range runs {
		if r.Done() || !r.Last.Before(before) {
			continue
		}
		if out == nil {
			out = slices.Clone(runs)
		}
		last := r.Last
		out[i].End, out[i].Unfinished = &last, true
		ids = append(ids, r.ID)
	}
	if out == nil {
		return runs, nil
	}
	return out, ids
}

// anyRunning reports whether a run has not ended.
func anyRunning(runs []store.Run) bool {
	for _, r := range runs {
		if !r.Done() {
			return true
		}
	}
	return false
}
