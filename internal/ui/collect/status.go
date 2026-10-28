// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
)

// The defaults of Status.
const (
	// statusEvery is how often the status of the clusters whose page is
	// open is read.
	statusEvery = 10 * time.Second
	// statusAfter is how long after an event of a cluster its status is
	// read, so that the burst of events of a command makes one read.
	statusAfter = time.Second
	// statusAllEvery is how often the status of every cluster is read
	// while a page is open, for the counts of the rail and the Overview.
	statusAllEvery = 30 * time.Second
)

// Watched counts the pages that watch the status of each cluster. A
// stream of the browser reports the topics its fragments watch with Watch,
// and releases them when it ends (ui.Env.AddWatcher takes Watch as it is).
type Watched struct {
	mu     sync.Mutex
	counts map[store.StatusKey]int
	// fresh are the clusters that went from none to one watcher since
	// Status last looked: their status is read at once.
	fresh map[store.StatusKey]bool
	wake  chan struct{}
}

// NewWatched returns a Watched that no one watches yet.
func NewWatched() *Watched {
	return &Watched{
		counts: map[store.StatusKey]int{},
		fresh:  map[store.StatusKey]bool{},
		wake:   make(chan struct{}, 1),
	}
}

// statusKey returns the cluster that a status topic names.
func statusKey(t store.Topic) (store.StatusKey, bool) {
	rest, ok := strings.CutPrefix(string(t), "status/")
	if !ok {
		return store.StatusKey{}, false
	}
	realm, name, ok := strings.Cut(rest, "/")
	if !ok || realm == "" || name == "" || strings.Contains(name, "/") {
		return store.StatusKey{}, false
	}
	return store.StatusKey{Realm: realm, Cluster: name}, true
}

// Watch counts the clusters whose status topic is in topics, and returns
// the function that stops counting them, which is safe to call more than
// once. Other topics are ignored.
func (w *Watched) Watch(topics []store.Topic) (release func()) {
	var keys []store.StatusKey
	for _, t := range topics {
		if k, ok := statusKey(t); ok && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return func() {}
	}
	w.mu.Lock()
	for _, k := range keys {
		if w.counts[k]++; w.counts[k] == 1 {
			w.fresh[k] = true
		}
	}
	w.mu.Unlock()
	poke(w.wake)
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			for _, k := range keys {
				if w.counts[k]--; w.counts[k] <= 0 {
					delete(w.counts, k)
					delete(w.fresh, k)
				}
			}
			w.mu.Unlock()
			poke(w.wake)
		})
	}
}

// Keys returns the clusters that are watched, sorted.
func (w *Watched) Keys() []store.StatusKey {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.SortedFunc(maps.Keys(w.counts), compareKeys)
}

// takeFresh returns the clusters that are newly watched, and forgets them.
func (w *Watched) takeFresh() []store.StatusKey {
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := slices.SortedFunc(maps.Keys(w.fresh), compareKeys)
	clear(w.fresh)
	return keys
}

func compareKeys(a, b store.StatusKey) int {
	return cmp.Or(cmp.Compare(a.Realm, b.Realm), cmp.Compare(a.Cluster, b.Cluster))
}

// Status reads the status of every cluster (cluster.GetStatus), as every
// page counts the nodes with a service down in the rail. It asks every
// node, so it reads a cluster whose page is open (Watched) more often than
// the others:
//
//   - a cluster when its page opens, every Every while it is open, and
//     After an event of the cluster;
//   - every cluster of the store when sind ui starts, when the store first
//     lists it, every All while a page is open (Viewers), and After an
//     event of the cluster.
//
// The status of a cluster that the store no longer lists, and that no page
// watches, is dropped. With the status of each cluster it reads what Slurm
// says (cluster.GetSlurmInfo), as every page counts a worker that Slurm
// sends no jobs, such as a drained one, too; it goes with the status, as
// does why the nodes that need a look do (readFaults), which the node page
// explains.
type Status struct {
	Store   *store.Store
	Client  *docker.Client
	Bus     *Bus
	Watched *Watched
	// Viewers counts the pages open. Without one the read of every
	// cluster every All waits, and the first page that opens reads at
	// once. nil reads always.
	Viewers *Viewers
	Log     *slog.Logger

	// Every, After and All replace the defaults (10 s, 1 s, 30 s) when
	// set.
	Every, After, All time.Duration
}

// listed returns the clusters that the snapshot lists, sorted.
func listed(sn store.Snapshot) []store.StatusKey {
	var keys []store.StatusKey
	for realm, cs := range sn.Clusters {
		for _, c := range cs {
			keys = append(keys, store.StatusKey{Realm: realm, Cluster: c.Name})
		}
	}
	slices.SortFunc(keys, compareKeys)
	return keys
}

// clustersChanged reports whether a topic of ts is the clusters of a realm.
func clustersChanged(ts []store.Topic) bool {
	return slices.ContainsFunc(ts, func(t store.Topic) bool { return strings.HasPrefix(string(t), "clusters/") })
}

// Run reads until ctx ends.
func (s *Status) Run(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer sub.Close()
	changes := s.Store.Notifier().Subscribe()
	defer changes.Close()
	tick := time.NewTicker(cmp.Or(s.Every, statusEvery))
	defer tick.Stop()
	all := time.NewTicker(cmp.Or(s.All, statusAllEvery))
	defer all.Stop()
	after := cmp.Or(s.After, statusAfter)
	paused := func() bool { return false }
	var resume <-chan struct{}
	if s.Viewers != nil {
		paused, resume = s.Viewers.None, s.Viewers.arrivals()
	}

	// seen are the clusters of the store that were read, so that one it
	// newly lists is read at once.
	seen := map[store.StatusKey]bool{}
	read := listed(s.Store.Snapshot())
	for _, k := range read {
		seen[k] = true
	}
	s.readEach(ctx, read)
	// due are the clusters to read, and when.
	due := map[store.StatusKey]time.Time{}
	for {
		var timer *time.Timer
		var timerC <-chan time.Time
		if next, ok := earliest(due); ok {
			timer = time.NewTimer(time.Until(next))
			timerC = timer.C
		}
		read = nil
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.Watched.wake:
			keep := s.keep()
			s.Store.KeepStatuses(keep...)
			for k := range due {
				if !slices.Contains(keep, k) {
					delete(due, k)
				}
			}
			read = s.Watched.takeFresh()
		case <-tick.C:
			read = s.Watched.Keys()
		case <-all.C:
			if !paused() {
				read = listed(s.Store.Snapshot())
			}
		case <-resume:
			read = listed(s.Store.Snapshot())
		case <-changes.Changed():
			if !clustersChanged(changes.Take()) {
				break
			}
			cur := listed(s.Store.Snapshot())
			for _, k := range cur {
				if !seen[k] {
					seen[k] = true
					read = append(read, k)
				}
			}
			// A cluster that is gone is new again when it comes back.
			maps.DeleteFunc(seen, func(k store.StatusKey, _ bool) bool { return !slices.Contains(cur, k) })
			s.Store.KeepStatuses(s.keep()...)
		case <-sub.Changed():
			now := time.Now()
			keys := s.keep()
			for _, ev := range sub.Take() {
				at := now.Add(after)
				if ev.Kind == KindRefresh {
					at = now
				}
				for _, k := range keys {
					if ev.Kind != KindLock && (ev.Realm == "" || ev.Realm == k.Realm && (ev.Cluster == "" || ev.Cluster == k.Cluster)) {
						if old, ok := due[k]; !ok || at.Before(old) {
							due[k] = at
						}
					}
				}
			}
		case <-timerC:
		}
		if timer != nil {
			timer.Stop()
		}
		now := time.Now()
		for k, at := range due {
			if !at.After(now) {
				read = append(read, k)
				delete(due, k)
			}
		}
		s.readEach(ctx, read)
	}
}

// keep returns the clusters whose status the store keeps: the ones it
// lists and the ones whose page is open, sorted, each once.
func (s *Status) keep() []store.StatusKey {
	keys := append(listed(s.Store.Snapshot()), s.Watched.Keys()...)
	slices.SortFunc(keys, compareKeys)
	return slices.Compact(keys)
}

// earliest returns the earliest time in due.
func earliest(due map[store.StatusKey]time.Time) (time.Time, bool) {
	var first time.Time
	found := false
	for _, at := range due {
		if !found || at.Before(first) {
			first, found = at, true
		}
	}
	return first, found
}

// readEach reads the status of the clusters at once, each once.
func (s *Status) readEach(ctx context.Context, keys []store.StatusKey) {
	slices.SortFunc(keys, compareKeys)
	keys = slices.Compact(keys)
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Go(func() { s.read(ctx, k) })
	}
	wg.Wait()
}

// read reads the status of one cluster into the store and what Slurm says
// (cluster.GetSlurmInfo), at the same time, and after the status why the
// nodes that need a look do (readFaults). It stores each as soon as it has
// it, so a slurmctld that is slow to answer does not hold up the status. A
// cluster that is gone has none of them; what cannot be read otherwise
// stays as it is. What the store would not keep (keep), as the cluster went
// while it was read, is not stored.
func (s *Status) read(ctx context.Context, k store.StatusKey) {
	var slurm sync.WaitGroup
	slurm.Go(func() { s.readSlurm(ctx, k) })
	defer slurm.Wait()
	st, err := cluster.GetStatus(ctx, s.Client, k.Realm, k.Cluster)
	switch {
	case errors.Is(err, cluster.ErrClusterNotFound):
		slurm.Wait()
		s.Store.SetStatus(k.Realm, k.Cluster, nil)
		s.Store.SetSlurm(k.Realm, k.Cluster, nil)
		s.Store.SetFaults(k.Realm, k.Cluster, nil)
	case err != nil:
		if ctx.Err() == nil {
			logOr(s.Log).Debug("reading the status", "realm", k.Realm, "cluster", k.Cluster, "err", err)
		}
	default:
		if !slices.Contains(s.keep(), k) {
			return
		}
		s.Store.SetStatus(k.Realm, k.Cluster, st)
		faults := readFaults(ctx, s.Client, k.Realm, k.Cluster, st)
		if ctx.Err() == nil && slices.Contains(s.keep(), k) {
			s.Store.SetFaults(k.Realm, k.Cluster, faults)
		}
	}
}

// readSlurm reads what Slurm says about one cluster into the store, with
// its error, as a page shows why sinfo did not answer; unless the read was
// cancelled, or the store would not keep the cluster (keep), as it went
// while it was read.
func (s *Status) readSlurm(ctx context.Context, k store.StatusKey) {
	info, err := cluster.GetSlurmInfo(ctx, s.Client, k.Realm, k.Cluster)
	sl := &store.Slurm{Info: info}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		sl.Err = doctor.ErrorLine(err)
	}
	if slices.Contains(s.keep(), k) {
		s.Store.SetSlurm(k.Realm, k.Cluster, sl)
	}
}
