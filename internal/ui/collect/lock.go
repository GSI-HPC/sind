// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/state"
)

// lockEvery is how often the realm locks are read when no event says so,
// which finds a lock whose command died.
const lockEvery = 15 * time.Second

// Lock reads who holds the lock of each realm (state.Holder), when a
// network <realm>-lock is created or removed, and when a realm is first seen.
type Lock struct {
	Store  *store.Store
	Client *docker.Client
	Bus    *Bus
	Log    *slog.Logger
	// Every replaces the default interval (15 s) of the full read.
	Every time.Duration
}

// Run reads until ctx ends.
func (l *Lock) Run(ctx context.Context) {
	every := l.Every
	if every <= 0 {
		every = lockEvery
	}
	p := perRealm{
		store: l.Store, bus: l.Bus, every: func() time.Duration { return every },
		wants: func(ev Event) bool { return ev.Kind == KindLock || ev.Kind == KindRefresh },
		stale: func(sn store.Snapshot) []string { return slices.Collect(maps.Keys(sn.Locks)) },
		read: func(ctx context.Context, realm string) {
			h, err := state.Holder(ctx, l.Client, realm)
			if err != nil {
				if ctx.Err() == nil {
					logOr(l.Log).Debug("reading the realm lock", "realm", realm, "err", err)
				}
				return
			}
			l.Store.SetLock(realm, h)
		},
	}
	p.run(ctx)
}
