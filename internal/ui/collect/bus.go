// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import "sync"

// Kind says what an Event is about.
type Kind int

// The kinds of Event.
const (
	// KindResync means that events may have been missed: read everything
	// of the realm again (everything of every realm when the realm is
	// empty).
	KindResync Kind = iota
	// KindContainer is an event of a container, such as a node starting.
	KindContainer
	// KindNetwork is an event of a network, other than the realm lock.
	KindNetwork
	// KindLock is an event of the network <realm>-lock, which a sind
	// command holds while it changes the realm.
	KindLock
	// KindRefresh asks for a read of the realm now; see Refresher.
	KindRefresh
)

// An Event tells the collectors that something changed in a realm.
// The zero Event is a resync of every realm.
type Event struct {
	Kind  Kind
	Realm string
	// Cluster is the cluster the event belongs to, empty for a network or
	// container that belongs to none, such as the mesh.
	Cluster string
}

// A Bus delivers Events to its subscribers. A subscriber never misses an
// event and never blocks the publisher: it holds the distinct events that
// it has not taken yet.
type Bus struct {
	mu   sync.Mutex
	subs map[*Sub]struct{}
}

// NewBus returns a bus without subscribers.
func NewBus() *Bus { return &Bus{subs: map[*Sub]struct{}{}} }

// Sub is one subscription to a Bus.
type Sub struct {
	bus     *Bus
	mu      sync.Mutex
	pending map[Event]struct{}
	wake    chan struct{}
}

// Subscribe returns a subscription that receives the events published from
// now on. Close it when done.
func (b *Bus) Subscribe() *Sub {
	s := &Sub{bus: b, pending: map[Event]struct{}{}, wake: make(chan struct{}, 1)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Publish gives ev to every subscriber.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		s.mu.Lock()
		s.pending[ev] = struct{}{}
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// C receives when Drain has events.
func (s *Sub) C() <-chan struct{} { return s.wake }

// Drain returns the distinct events published since the last call, in no
// order.
func (s *Sub) Drain() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, 0, len(s.pending))
	for ev := range s.pending {
		out = append(out, ev)
	}
	clear(s.pending)
	return out
}

// Close ends the subscription.
func (s *Sub) Close() {
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
}
