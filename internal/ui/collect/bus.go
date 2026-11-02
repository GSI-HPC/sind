// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import "github.com/GSI-HPC/go-clikit/localweb/sse"

// Kind says what an Event is about.
type Kind int

// The kinds of Event.
const (
	// KindResync means that events may have been missed: read everything
	// of the realm again (everything of every realm when the realm is
	// empty).
	KindResync Kind = iota
	// KindChange is an event of a container, such as a node starting, or
	// of a network other than the realm lock.
	KindChange
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
type Bus = sse.Notifier[Event]
