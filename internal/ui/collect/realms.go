// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// perRealm is the loop of the collectors that read one thing of each realm
// (the lock, the monitoring stack). It reads
//
//   - the realm of an event that wants says so, as soon as it arrives,
//   - every realm when the events resync, every interval, and when Docker
//     answers again after it did not,
//   - a realm the store has just learnt of.
//
// Docker not answering skips the reads that nothing asked for.
type perRealm struct {
	store *store.Store
	bus   *Bus
	// every is the interval of the full read.
	every time.Duration
	// wants says whether an event of a realm makes this collector read it.
	wants func(Event) bool
	// stale returns the realms that the snapshot holds a value of, which
	// are read although no longer listed, so that the value goes.
	stale func(store.Snapshot) []string
	// read reads one realm into the store.
	read func(ctx context.Context, realm string)
}

// realmNames returns the names of the realms of sn, and the ones stale
// adds, each once.
func (p *perRealm) realmNames(sn store.Snapshot) []string {
	var names []string
	for _, r := range sn.Realms {
		names = append(names, r.Name)
	}
	for _, r := range p.stale(sn) {
		if !slices.Contains(names, r) {
			names = append(names, r)
		}
	}
	return names
}

// run reads until ctx ends.
func (p *perRealm) run(ctx context.Context) {
	sub := p.bus.Subscribe()
	defer sub.Close()
	changes, unsubscribe := p.store.Subscribe()
	defer unsubscribe()
	tick := time.NewTicker(p.every)
	defer tick.Stop()

	seen := map[string]bool{}
	wasDown := false
	// readAll reads every realm of the snapshot, unless Docker is down.
	readAll := func() {
		sn := p.store.Snapshot()
		wasDown = sn.Docker.Err != ""
		if wasDown {
			return
		}
		names := p.realmNames(sn)
		clear(seen)
		for _, n := range names {
			seen[n] = true
		}
		p.readEach(ctx, names)
	}
	readAll()
	for {
		var names []string
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			readAll()
			continue
		case <-changes:
			sn := p.store.Snapshot()
			if sn.Docker.Err != "" {
				wasDown = true
				continue
			}
			if wasDown {
				// Docker answers again: read everything.
				readAll()
				continue
			}
			listed := map[string]bool{}
			for _, r := range sn.Realms {
				listed[r.Name] = true
				if !seen[r.Name] {
					seen[r.Name] = true
					names = append(names, r.Name)
				}
			}
			// A realm that is gone is new again when it comes back.
			maps.DeleteFunc(seen, func(r string, _ bool) bool { return !listed[r] })
		case <-sub.C():
			resync := false
			for _, ev := range sub.Drain() {
				switch {
				case ev.Realm == "":
					resync = true
				case p.wants(ev) && !slices.Contains(names, ev.Realm):
					names = append(names, ev.Realm)
				}
			}
			if resync {
				readAll()
				continue
			}
		}
		p.readEach(ctx, names)
	}
}

// readEach reads the realms at once.
func (p *perRealm) readEach(ctx context.Context, realms []string) {
	var wg sync.WaitGroup
	for _, r := range realms {
		wg.Go(func() { p.read(ctx, r) })
	}
	wg.Wait()
}
