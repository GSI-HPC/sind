// SPDX-License-Identifier: LGPL-3.0-or-later

package store

import (
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
)

// Topics of the sind data. The names use "/" as separator, so a fragment
// can depend on a prefix such as "status/".
const (
	// TopicRealms changes when the list of realms changes.
	TopicRealms Topic = "realms"
	// TopicDoctor changes when the host check starts, ends or reports.
	TopicDoctor Topic = "doctor"
	// TopicDocker changes when Docker becomes reachable or unreachable.
	TopicDocker Topic = "docker"
)

// ClustersTopic is the topic of the clusters of a realm.
func ClustersTopic(realm string) Topic { return Topic("clusters/" + realm) }

// NodesTopic is the topic of the nodes of a realm.
func NodesTopic(realm string) Topic { return Topic("nodes/" + realm) }

// NetworksTopic is the topic of the networks of a realm.
func NetworksTopic(realm string) Topic { return Topic("networks/" + realm) }

// VolumesTopic is the topic of the volumes of a realm.
func VolumesTopic(realm string) Topic { return Topic("volumes/" + realm) }

// MeshTopic is the topic of the mesh of a realm.
func MeshTopic(realm string) Topic { return Topic("mesh/" + realm) }

// LockTopic is the topic of the realm lock holder of a realm.
func LockTopic(realm string) Topic { return Topic("lock/" + realm) }

// MonitoringTopic is the topic of the monitoring stack of a realm.
func MonitoringTopic(realm string) Topic { return Topic("monitoring/" + realm) }

// StatusTopic is the topic of the status of one cluster.
func StatusTopic(realm, cluster string) Topic {
	return Topic("status/" + realm + "/" + cluster)
}

// StatusKey names a cluster.
type StatusKey struct {
	Realm   string
	Cluster string
}

// Topic is the status topic of the cluster.
func (k StatusKey) Topic() Topic { return StatusTopic(k.Realm, k.Cluster) }

// Mesh is what the mesh page shows of one realm.
type Mesh struct {
	// Info is the mesh infrastructure, nil when the realm has no mesh.
	Info *mesh.Info
	// Records are the mesh DNS records, in their natural order.
	Records []mesh.DNSRecord
	// External are the containers outside sind on the mesh network,
	// sorted.
	External []string
	// Err says in one line why the mesh could not be read.
	Err string
}

// Slurm is what Slurm says about a cluster, read next to its status.
type Slurm struct {
	// Info is what sinfo and the config files say; when sinfo did not
	// answer, the files alone, and nil when none could be read either.
	Info *cluster.SlurmInfo
	// Err says in one line why they could not be read.
	Err string
}

// DoctorCheck is the outcome of one host check, as `sind doctor -o json`
// prints it.
type DoctorCheck = doctor.Check

// DoctorReport is the outcome of the last host check.
type DoctorReport struct {
	// Time is when the check ended.
	Time   time.Time
	Checks []DoctorCheck
	// Running is set while a check runs.
	Running bool
}

// DockerState says whether the Docker daemon answers.
type DockerState struct {
	// Checked is false until the first attempt to reach Docker.
	Checked bool
	// Err is the one-line reason Docker is not reachable, empty when it
	// is.
	Err string
	// MemTotal is the memory of Docker's host in bytes, and NCPU its
	// CPUs; zero until they are read.
	MemTotal int64
	NCPU     int
}

// change changes the snapshot with fn under the lock. When fn returns
// true, the subscribers of each of ts are notified.
func (s *Store) change(fn func(*Snapshot) bool, ts ...Topic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !fn(&s.snap) {
		return
	}
	s.changes.Notify(ts...)
}

// setEntry sets m[k] to v in a copy of the map, or removes k when v is
// empty, and reports whether anything changed. A value equal to the stored
// one is not a change.
func setEntry[K comparable, V any](m *map[K]V, k K, v V, empty bool) bool {
	old, had := (*m)[k]
	if empty {
		if !had {
			return false
		}
	} else if had && reflect.DeepEqual(old, v) {
		return false
	}
	next := maps.Clone(*m)
	if next == nil {
		next = map[K]V{}
	}
	if empty {
		delete(next, k)
	} else {
		next[k] = v
	}
	*m = next
	return true
}

// SetRealms replaces the realms, sorted by name.
func (s *Store) SetRealms(realms []cluster.RealmSummary) {
	realms = slices.SortedFunc(slices.Values(realms), func(a, b cluster.RealmSummary) int {
		return natsort.Compare(a.Name, b.Name)
	})
	s.change(func(sn *Snapshot) bool {
		if len(realms) == 0 && len(sn.Realms) == 0 || reflect.DeepEqual(sn.Realms, realms) {
			return false
		}
		sn.Realms = realms
		return true
	}, TopicRealms)
}

// SetClusters replaces the clusters of the realm, sorted by name.
func (s *Store) SetClusters(realm string, clusters []cluster.Summary) {
	clusters = slices.SortedFunc(slices.Values(clusters), func(a, b cluster.Summary) int {
		return natsort.Compare(a.Name, b.Name)
	})
	s.change(func(sn *Snapshot) bool {
		return setEntry(&sn.Clusters, realm, clusters, len(clusters) == 0)
	}, ClustersTopic(realm))
}

// SetNodes replaces the nodes of the realm, sorted by cluster, then by
// role (controller, db, api, submitter, worker, others), then by name.
func (s *Store) SetNodes(realm string, nodes []cluster.NodeSummary) {
	nodes = slices.SortedStableFunc(slices.Values(nodes), compareNodes)
	s.change(func(sn *Snapshot) bool {
		old := sn.Nodes[realm]
		if !setEntry(&sn.Nodes, realm, nodes, len(nodes) == 0) {
			return false
		}
		record(sn, s.clock(), nodeChanges(realm, old, nodes))
		forget(sn, realm, "", func(k NodeKey) bool {
			return slices.ContainsFunc(nodes, func(n cluster.NodeSummary) bool {
				return n.Container == string(cluster.ContainerName(realm, k.Cluster, k.Node))
			})
		})
		return true
	}, NodesTopic(realm))
}

var nodeRoles = []config.Role{config.RoleController, config.RoleDB, config.RoleAPI, config.RoleSubmitter, config.RoleWorker}

func compareNodes(a, b cluster.NodeSummary) int {
	rank := func(r config.Role) int {
		if i := slices.Index(nodeRoles, r); i >= 0 {
			return i
		}
		return len(nodeRoles)
	}
	if c := natsort.Compare(a.Cluster, b.Cluster); c != 0 {
		return c
	}
	if c := rank(a.Role) - rank(b.Role); c != 0 {
		return c
	}
	if c := natsort.Compare(string(a.Role), string(b.Role)); c != 0 {
		return c
	}
	return natsort.Compare(a.Container, b.Container)
}

// SetNetworks replaces the networks of the realm, sorted by name.
func (s *Store) SetNetworks(realm string, networks []cluster.NetworkSummary) {
	networks = slices.SortedFunc(slices.Values(networks), func(a, b cluster.NetworkSummary) int {
		return natsort.Compare(a.Name, b.Name)
	})
	s.change(func(sn *Snapshot) bool {
		return setEntry(&sn.Networks, realm, networks, len(networks) == 0)
	}, NetworksTopic(realm))
}

// SetVolumes replaces the volumes of the realm, sorted by name.
func (s *Store) SetVolumes(realm string, volumes []cluster.VolumeSummary) {
	volumes = slices.SortedFunc(slices.Values(volumes), func(a, b cluster.VolumeSummary) int {
		return natsort.Compare(a.Name, b.Name)
	})
	s.change(func(sn *Snapshot) bool {
		return setEntry(&sn.Volumes, realm, volumes, len(volumes) == 0)
	}, VolumesTopic(realm))
}

// SetMesh replaces the mesh of the realm. A zero Mesh removes it. The
// store keeps its own copies of the slices; the Info is handed over and
// must not change afterwards.
func (s *Store) SetMesh(realm string, m Mesh) {
	m.Records = slices.Clone(m.Records)
	m.External = slices.Clone(m.External)
	slices.Sort(m.External)
	if len(m.Records) == 0 {
		m.Records = nil
	}
	if len(m.External) == 0 {
		m.External = nil
	}
	s.change(func(sn *Snapshot) bool {
		return setEntry(&sn.Meshes, realm, m, reflect.DeepEqual(m, Mesh{}))
	}, MeshTopic(realm))
}

// SetStatus replaces the status of a cluster. A nil status removes it. The
// status is handed over and must not change afterwards.
func (s *Store) SetStatus(realm, name string, st *cluster.Status) {
	k := StatusKey{realm, name}
	s.change(func(sn *Snapshot) bool {
		old := sn.Statuses[k]
		if !setEntry(&sn.Statuses, k, st, st == nil) {
			return false
		}
		if old != nil && st != nil {
			record(sn, s.clock(), statusChanges(realm, name, old, st))
		}
		return true
	}, k.Topic())
}

// SetSlurm replaces what Slurm says about a cluster. A nil value removes
// it. It is part of the cluster's status topic, as it is read with the
// status. The value is handed over and must not change afterwards.
func (s *Store) SetSlurm(realm, name string, sl *Slurm) {
	k := StatusKey{realm, name}
	s.change(func(sn *Snapshot) bool {
		old := sn.Slurms[k]
		if !setEntry(&sn.Slurms, k, sl, sl == nil) {
			return false
		}
		if old != nil && sl != nil {
			record(sn, s.clock(), slurmChanges(realm, name, old, sl))
		}
		return true
	}, k.Topic())
}

// keepEntries drops the entries of m whose key is not in keep, in a copy.
func keepEntries[V any](m *map[StatusKey]V, keep []StatusKey) {
	if len(*m) == 0 {
		return
	}
	next := maps.Clone(*m)
	maps.DeleteFunc(next, func(k StatusKey, _ V) bool { return !slices.Contains(keep, k) })
	*m = next
}

// KeepStatuses drops the status, what Slurm says and the faults of the
// nodes of every cluster that is not in keep, as the cluster is gone and no
// page shows it. It notifies nothing: no page shows a dropped status.
func (s *Store) KeepStatuses(keep ...StatusKey) {
	s.change(func(sn *Snapshot) bool {
		keepEntries(&sn.Statuses, keep)
		keepEntries(&sn.Slurms, keep)
		keepEntries(&sn.Faults, keep)
		return false
	})
}

// SetLock replaces the holder of the realm lock. A nil holder, the lock
// being free, removes it. The holder is handed over and must not change
// afterwards.
func (s *Store) SetLock(realm string, h *state.LockHolder) {
	s.change(func(sn *Snapshot) bool {
		return setEntry(&sn.Locks, realm, h, h == nil)
	}, LockTopic(realm))
}

// SetMonitoring replaces the monitoring stack of the realm. A nil value,
// the realm having no stack, removes it. The value is handed over and must
// not change afterwards.
func (s *Store) SetMonitoring(realm string, m *cluster.Monitoring) {
	s.change(func(sn *Snapshot) bool {
		return setEntry(&sn.Monitorings, realm, m, m == nil)
	}, MonitoringTopic(realm))
}

// SetDoctor replaces the doctor report. A nil report removes it.
func (s *Store) SetDoctor(r *DoctorReport) {
	if r != nil {
		c := *r
		c.Checks = slices.Clone(r.Checks)
		r = &c
	}
	s.change(func(sn *Snapshot) bool {
		if reflect.DeepEqual(sn.Doctor, r) {
			return false
		}
		sn.Doctor = r
		return true
	}, TopicDoctor)
}

// SetDocker replaces the Docker connection state.
func (s *Store) SetDocker(d DockerState) {
	s.change(func(sn *Snapshot) bool {
		if sn.Docker == d {
			return false
		}
		sn.Docker = d
		return true
	}, TopicDocker)
}

// Realm returns the realm named name.
func (sn Snapshot) Realm(name string) (cluster.RealmSummary, bool) {
	i := slices.IndexFunc(sn.Realms, func(r cluster.RealmSummary) bool { return r.Name == name })
	if i < 0 {
		return cluster.RealmSummary{}, false
	}
	return sn.Realms[i], true
}

// Cluster returns the cluster named name of the realm.
func (sn Snapshot) Cluster(realm, name string) (cluster.Summary, bool) {
	i := slices.IndexFunc(sn.Clusters[realm], func(c cluster.Summary) bool { return c.Name == name })
	if i < 0 {
		return cluster.Summary{}, false
	}
	return sn.Clusters[realm][i], true
}

// ClusterNodes returns the nodes of one cluster, in the order SetNodes
// keeps. The slice is a copy.
func (sn Snapshot) ClusterNodes(realm, name string) []cluster.NodeSummary {
	var out []cluster.NodeSummary
	for _, n := range sn.Nodes[realm] {
		if n.Cluster == name {
			out = append(out, n)
		}
	}
	return out
}

// Status returns the status of a cluster, nil when none is stored.
func (sn Snapshot) Status(realm, name string) *cluster.Status {
	return sn.Statuses[StatusKey{realm, name}]
}

// Slurm returns what Slurm says about a cluster, nil when none is stored.
func (sn Snapshot) Slurm(realm, name string) *Slurm {
	return sn.Slurms[StatusKey{realm, name}]
}
