// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"context"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

const (
	// maxRuns is how many runs Follow keeps, the newest.
	maxRuns = 200
	// activeEvery is how often Follow reads the log while a run goes on,
	// and idleEvery how often otherwise.
	activeEvery = 250 * time.Millisecond
	idleEvery   = time.Second
)

// Follow reads the event log at path as it grows and keeps the runs of st
// up to date, until ctx ends. It reads every 250 ms while a run has not
// ended and every second otherwise, and sets the runs, with the IDs of
// those that changed, only when something changed. The log may be missing
// or be rotated: see Tail.
func Follow(ctx context.Context, st *store.Store, path string) {
	follow(ctx, st, path, maxRuns, activeEvery, idleEvery)
}

// follow is Follow with the number of runs it keeps and its two intervals.
func follow(ctx context.Context, st *store.Store, path string, keep int, active, idle time.Duration) {
	red := NewReducer(keep)
	tail := NewTail(path, red)
	busy := false
	for {
		if tail.Poll() {
			if ids := red.Changed(); len(ids) > 0 {
				runs := red.Runs()
				st.SetRuns(runs, ids...)
				busy = anyRunning(runs)
			}
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

// anyRunning reports whether a run has not ended.
func anyRunning(runs []store.Run) bool {
	for _, r := range runs {
		if !r.Done() {
			return true
		}
	}
	return false
}
