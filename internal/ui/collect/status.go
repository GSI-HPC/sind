// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// The defaults of Status.
const (
	// statusEvery is how often the status of the clusters whose page is
	// open is read.
	statusEvery = 10 * time.Second
	// statusAfter is how long after an event of a cluster its status is
	// read, so that the burst of events of a command makes one read.
	statusAfter = time.Second
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
	w.poke()
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
			w.poke()
		})
	}
}

func (w *Watched) poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Keys returns the clusters that are watched, sorted.
func (w *Watched) Keys() []store.StatusKey {
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := make([]store.StatusKey, 0, len(w.counts))
	for k := range w.counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, compareKeys)
	return keys
}

// takeFresh returns the clusters that are newly watched, and forgets them.
func (w *Watched) takeFresh() []store.StatusKey {
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := make([]store.StatusKey, 0, len(w.fresh))
	for k := range w.fresh {
		keys = append(keys, k)
	}
	clear(w.fresh)
	slices.SortFunc(keys, compareKeys)
	return keys
}

func compareKeys(a, b store.StatusKey) int {
	return cmp.Or(cmp.Compare(a.Realm, b.Realm), cmp.Compare(a.Cluster, b.Cluster))
}

// Status reads the status of the clusters that Watched says are open
// (cluster.GetStatus; it asks every node, so only for what a page shows):
// when a cluster is first watched, every Every, and After an event of the
// cluster. The status of a cluster that nobody watches any more is dropped
// from the store.
type Status struct {
	Store   *store.Store
	Client  *docker.Client
	Bus     *Bus
	Watched *Watched
	Log     *slog.Logger

	// Every and After replace the defaults (10 s, 1 s) when set.
	Every, After time.Duration
}

// Run reads until ctx ends.
func (s *Status) Run(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer sub.Close()
	tick := time.NewTicker(cmp.Or(s.Every, statusEvery))
	defer tick.Stop()
	after := cmp.Or(s.After, statusAfter)

	// due are the clusters to read, and when.
	due := map[store.StatusKey]time.Time{}
	for {
		var timer *time.Timer
		var timerC <-chan time.Time
		if next, ok := earliest(due); ok {
			timer = time.NewTimer(time.Until(next))
			timerC = timer.C
		}
		var read []store.StatusKey
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-s.Watched.wake:
			keys := s.Watched.Keys()
			s.Store.KeepStatuses(keys...)
			for k := range due {
				if !slices.Contains(keys, k) {
					delete(due, k)
				}
			}
			read = s.Watched.takeFresh()
		case <-tick.C:
			read = s.Watched.Keys()
		case <-sub.C():
			now := time.Now()
			watched := s.Watched.Keys()
			for _, ev := range sub.Drain() {
				at := now.Add(after)
				if ev.Kind == KindRefresh {
					at = now
				}
				for _, k := range watched {
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

// read reads the status of one cluster into the store. A cluster that is
// gone has none; what cannot be read otherwise stays as it is.
func (s *Status) read(ctx context.Context, k store.StatusKey) {
	st, err := cluster.GetStatus(ctx, s.Client, k.Realm, k.Cluster)
	switch {
	case errors.Is(err, cluster.ErrClusterNotFound):
		s.Store.SetStatus(k.Realm, k.Cluster, nil)
	case err != nil:
		if ctx.Err() == nil {
			logOr(s.Log).Debug("reading the status", "realm", k.Realm, "cluster", k.Cluster, "err", err)
		}
	default:
		// A cluster whose page closed while it was read is not kept.
		if slices.Contains(s.Watched.Keys(), k) {
			s.Store.SetStatus(k.Realm, k.Cluster, st)
		}
	}
}
