// SPDX-License-Identifier: LGPL-3.0-or-later

package store

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// What the store knows of one node beyond the lists and the status of its
// cluster: why it needs a look (Fault), which the collector reads with the
// status, and what changed on it while sind ui ran (Change), which the store
// records itself as the setters replace what they hold.

// Fault is what sind ui read of why a node needs a look: how its container
// stopped, or which services are not active and why.
type Fault struct {
	// Exit is how the container of a node that does not run stopped; nil
	// for one that runs, or when Docker could not tell.
	Exit *Exit
	// Units are the services of a running node that are not active.
	Units []Unit
}

// Exit is how a container stopped, as docker inspect says.
type Exit struct {
	// Code is its exit code; OOMKilled is set when the kernel killed it
	// for want of memory.
	Code      int
	OOMKilled bool
	// Error is what Docker says went wrong when it started the container
	// last, empty for nothing.
	Error string
	// At is when it stopped; zero when not known.
	At time.Time
}

// Unit is a systemd unit that is not active, as systemctl show says.
type Unit struct {
	Name string
	// State is its ActiveState, such as failed or inactive; Result says
	// why it stopped, such as exit-code or signal; Status is the exit
	// status of its main process.
	State, Result string
	Status        int
	// Since is when it last changed its state; zero when not known.
	Since time.Time
	// Journal is the end of its journal, the oldest line first.
	Journal []JournalLine
}

// JournalLine is a line of the journal: its time, and its text after the
// host name, as "slurmd[212]: error: Unable to register".
type JournalLine struct {
	At   time.Time
	Text string
}

// Unit returns the unit of the fault named name.
func (f Fault) Unit(name string) (Unit, bool) {
	i := slices.IndexFunc(f.Units, func(u Unit) bool { return u.Name == name })
	if i < 0 {
		return Unit{}, false
	}
	return f.Units[i], true
}

// SetFaults replaces what sind ui read of the nodes of a cluster that need
// a look, by short name. An empty map removes it. It is part of the
// cluster's status topic, as it is read with the status. The map is handed
// over and must not change afterwards.
func (s *Store) SetFaults(realm, name string, faults map[string]Fault) {
	k := StatusKey{realm, name}
	s.change(func(sn *Snapshot) bool {
		return setEntry(&sn.Faults, k, faults, len(faults) == 0)
	}, k.Topic())
}

// Fault returns what sind ui read of why a node needs a look.
func (sn Snapshot) Fault(realm, name, short string) (Fault, bool) {
	f, ok := sn.Faults[StatusKey{realm, name}][short]
	return f, ok
}

// NodeKey names a node: its realm, its cluster and its short name.
type NodeKey struct {
	Realm, Cluster, Node string
}

// The sources of a Change.
const (
	SourceDocker  = "docker"
	SourceSystemd = "systemd"
	SourceSlurm   = "sinfo"
)

// Change is a change of a node that sind ui saw: its container's state, a
// service that went down or came up, or its state in Slurm.
type Change struct {
	// At is when sind ui saw it, by the store's clock.
	At time.Time
	// Source is SourceDocker, SourceSystemd or SourceSlurm; Subject is
	// what changed, the container, a service or the Slurm state; State
	// is what it is now, such as running, active, down or "drained".
	Source, Subject, State string
	// Reason is what Slurm says of the state, as "memory errors".
	Reason string
	// OK is set for a change that is good news: a service that came up, a
	// container that runs, a Slurm state that takes jobs.
	OK bool
}

// MaxChanges is how many changes the store keeps of each node, the newest.
const MaxChanges = 20

// Changes returns what changed on the node while sind ui ran, the newest
// first.
func (sn Snapshot) Changes(realm, name, short string) []Change {
	return sn.NodeChanges[NodeKey{realm, name, short}]
}

// SetClock sets the clock that times the changes the store records, as a
// demo plays in a time of its own; nil is time.Now.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// clock returns the time by the store's clock; the lock is held.
func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// record adds the changes to the nodes they are for, in a copy of the
// map, each list the newest first and at most MaxChanges long.
func record(sn *Snapshot, at time.Time, changes map[NodeKey][]Change) {
	if len(changes) == 0 {
		return
	}
	next := maps.Clone(sn.NodeChanges)
	if next == nil {
		next = map[NodeKey][]Change{}
	}
	for k, cs := range changes {
		for i := range cs {
			cs[i].At = at
		}
		list := append(slices.Clone(cs), next[k]...)
		next[k] = list[:min(len(list), MaxChanges)]
	}
	sn.NodeChanges = next
}

// forget drops the changes of the nodes of the realm, of the cluster when
// name is set, that keep does not hold, in a copy of the map.
func forget(sn *Snapshot, realm, name string, keep func(NodeKey) bool) {
	if len(sn.NodeChanges) == 0 {
		return
	}
	next := maps.Clone(sn.NodeChanges)
	maps.DeleteFunc(next, func(k NodeKey, _ []Change) bool {
		return k.Realm == realm && (name == "" || k.Cluster == name) && !keep(k)
	})
	sn.NodeChanges = next
}

// nodeChanges returns the changes of the containers of a realm from old to
// nodes: each container whose state changed. A node that is new has none,
// and the changes of one that went are dropped (forget).
func nodeChanges(realm string, old, nodes []cluster.NodeSummary) map[NodeKey][]Change {
	out := map[NodeKey][]Change{}
	for _, n := range nodes {
		i := slices.IndexFunc(old, func(o cluster.NodeSummary) bool { return o.Container == n.Container })
		if i < 0 || old[i].State == n.State {
			continue
		}
		k := NodeKey{realm, n.Cluster, strings.TrimPrefix(n.Container, cluster.ContainerPrefix(realm, n.Cluster))}
		out[k] = append(out[k], Change{Source: SourceDocker, Subject: "container", State: string(n.State), OK: n.State == cluster.StateRunning})
	}
	return out
}

// statusChanges returns the services of the nodes of a cluster that went
// down or came up from the status old to st.
func statusChanges(realm, name string, old, st *cluster.Status) map[NodeKey][]Change {
	out := map[NodeKey][]Change{}
	for _, n := range st.Nodes {
		if n == nil {
			continue
		}
		o := statusNode(old, n.Name)
		if n.Health == nil || o == nil || o.Health == nil || n.Health.State != cluster.StateRunning || o.Health.State != cluster.StateRunning {
			continue
		}
		k := NodeKey{realm, name, strings.TrimSuffix(n.Name, "."+name)}
		for _, svc := range slices.Sorted(maps.Keys(n.Health.Services)) {
			up := n.Health.Services[svc]
			if was, ok := o.Health.Services[svc]; !ok || was == up {
				continue
			}
			c := Change{Source: SourceSystemd, Subject: string(svc), State: "down"}
			if up {
				c.State, c.OK = "active", true
			}
			out[k] = append(out[k], c)
		}
	}
	return out
}

// statusNode returns the node of the status named name, nil for none.
func statusNode(st *cluster.Status, name string) *cluster.NodeStatus {
	for _, n := range st.Nodes {
		if n != nil && n.Name == name {
			return n
		}
	}
	return nil
}

// slurmChanges returns the nodes of a cluster whose state or reason in
// Slurm changed from old to sl, as sinfo lists them.
func slurmChanges(realm, name string, old, sl *Slurm) map[NodeKey][]Change {
	out := map[NodeKey][]Change{}
	if old.Info == nil || sl.Info == nil {
		return out
	}
	for _, n := range sl.Info.Nodes {
		i := slices.IndexFunc(old.Info.Nodes, func(o slurm.NodeInfo) bool { return o.Name == n.Name })
		if i < 0 || old.Info.Nodes[i].State == n.State && old.Info.Nodes[i].Reason == n.Reason {
			continue
		}
		k := NodeKey{realm, name, n.Name}
		out[k] = append(out[k], Change{Source: SourceSlurm, Subject: "Slurm", State: n.State, Reason: n.Reason, OK: slurm.NodeStateTakesJobs(n.State)})
	}
	return out
}
