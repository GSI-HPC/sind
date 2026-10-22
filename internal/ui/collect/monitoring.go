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

// MonitoringEvery is how often the monitoring stacks are read when no
// event says so, unless the user sets another interval.
const MonitoringEvery = 30 * time.Second

// Monitoring reads the monitoring stack of each realm (cluster.GetMonitoring)
// on an event of the realm and every Interval, while a page is open.
type Monitoring struct {
	Store  *store.Store
	Client *docker.Client
	Bus    *Bus
	Log    *slog.Logger
	// Interval is the interval of the full read, which the user may change
	// as it runs; a change reads at once. nil is MonitoringEvery.
	Interval *Interval
	// Viewers counts the pages open. Without one the full read every
	// interval waits, and the first page that opens reads at once. nil
	// reads always.
	Viewers *Viewers
}

// Run reads until ctx ends.
func (m *Monitoring) Run(ctx context.Context) {
	interval := m.Interval
	if interval == nil {
		interval = NewInterval(MonitoringEvery)
	}
	p := perRealm{
		store: m.Store, bus: m.Bus, every: interval.Get, retime: interval.changed,
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
	if m.Viewers != nil {
		p.paused, p.resume = m.Viewers.None, m.Viewers.arrived
	}
	p.run(ctx)
}
