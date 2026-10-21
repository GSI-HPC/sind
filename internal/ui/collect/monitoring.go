// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// monitoringEvery is how often the monitoring stacks are read when no
// event says so.
const monitoringEvery = 30 * time.Second

// Monitoring reads the monitoring stack of each realm (cluster.GetMonitoring)
// on an event of the realm and every 30 s.
type Monitoring struct {
	Store  *store.Store
	Client *docker.Client
	Bus    *Bus
	Log    *slog.Logger
	// Every replaces the default interval (30 s) of the full read.
	Every time.Duration
}

// Run reads until ctx ends.
func (m *Monitoring) Run(ctx context.Context) {
	every := m.Every
	if every <= 0 {
		every = monitoringEvery
	}
	p := perRealm{
		store: m.Store, bus: m.Bus, every: every,
		wants: func(ev Event) bool { return ev.Kind != KindLock },
		stale: func(sn store.Snapshot) []string { return slices.Collect(maps.Keys(sn.Monitorings)) },
		read: func(ctx context.Context, realm string) {
			mon, err := cluster.GetMonitoring(ctx, m.Client, realm)
			switch {
			case errors.Is(err, cluster.ErrNoMonitoring):
				m.Store.SetMonitoring(realm, nil)
			case err != nil:
				if ctx.Err() == nil {
					logOr(m.Log).Debug("reading the monitoring stack", "realm", realm, "err", err)
				}
			default:
				m.Store.SetMonitoring(realm, mon)
			}
		},
	}
	p.run(ctx)
}
