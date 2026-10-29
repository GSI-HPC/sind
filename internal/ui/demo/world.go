// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/GSI-HPC/sind/pkg/probe"
)

// World is the Docker host of the demo: realms, clusters and nodes. The
// simulator changes it as its runs go on, and Publish writes what the real
// collectors would have read from Docker into the store.
type World struct {
	mu      sync.Mutex
	realms  []*realm
	nextNet int
	// published are the realms the last Publish wrote, so that the next
	// one clears those that are gone.
	published map[string]bool
	statuses  []store.StatusKey
	// outside are the containers of other tools on the host, with the
	// networks they have of their own: a realm whose mesh they join adds
	// its mesh network (realm.external).
	outside []store.Outside
	// now is the clock of what the world times, such as when a service
	// failed; nil is time.Now.
	now func() time.Time
}

type realm struct {
	name       string
	net        int
	monitoring bool
	// external are the containers of other tools on the mesh network.
	external []string
	clusters []*clusterState
}

type clusterState struct {
	name       string
	slurm      string
	net        int
	monitoring bool
	// volume is whether the data is a Docker volume, else a host path.
	volume   bool
	dataPath string
	users    []string
	nodes    []*nodeState
	nextHost int
	// cpus and memory are the size of each node, 0 for sind's defaults.
	cpus   int
	memory int64
}

type nodeState struct {
	short string
	role  config.Role
	ip    string
	state cluster.State
	// up are the services that run; a node that is not running has none.
	up map[probe.Service]bool
	// drain is the reason Slurm does not schedule on the node, empty for
	// none; busy are the CPUs its jobs hold (see slurmNode).
	drain string
	busy  int
	// calm is how many times Drift leaves the node alone, as after a
	// reboot that fixed it.
	calm int
	// failed is when each service that is down failed, and stopped when
	// the container stopped, with its exit code and whether the kernel
	// killed it for want of memory; zero for a time before the demo
	// started, which Publish sets (stamp).
	failed  map[probe.Service]time.Time
	stopped time.Time
	exit    int
	oom     bool
	// cpus and memory are the size the user asked for when the node was
	// made, 0 for the size of its cluster.
	cpus   int
	memory int64
}

// NewWorld returns a world with nothing in it.
func NewWorld() *World {
	return &World{nextNet: 18, published: map[string]bool{}}
}

// The Slurm versions of the images of the demo clusters.
const (
	slurmNew = "26.05.4"
	slurmOld = "25.11.8"
)

// services returns the services a node of the role runs once it is ready.
func services(role config.Role) []probe.Service {
	switch role {
	case config.RoleController:
		return []probe.Service{probe.ServiceMunge, probe.ServiceSSHD, probe.ServiceSlurmctld}
	case config.RoleDB:
		return []probe.Service{probe.ServiceMunge, probe.ServiceSSHD, probe.ServiceMariadb, probe.ServiceSlurmdbd}
	case config.RoleAPI:
		return []probe.Service{probe.ServiceMunge, probe.ServiceSSHD, probe.ServiceSlurmrestd}
	case config.RoleSubmitter:
		return []probe.Service{probe.ServiceMunge, probe.ServiceSSHD, probe.ServiceSackd}
	default:
		return []probe.Service{probe.ServiceMunge, probe.ServiceSSHD, probe.ServiceSlurmd}
	}
}

// realmOf returns the realm, made if it is not there yet.
func (w *World) realmOf(name string) *realm {
	for _, r := range w.realms {
		if r.name == name {
			return r
		}
	}
	r := &realm{name: name, net: w.nextNet}
	w.nextNet++
	w.realms = append(w.realms, r)
	return r
}

// clusterOf returns the cluster of the realm, made if it is not there yet.
func (w *World) clusterOf(realm, name string) *clusterState {
	r := w.realmOf(realm)
	for _, c := range r.clusters {
		if c.name == name {
			return c
		}
	}
	c := &clusterState{name: name, slurm: slurmNew, net: w.nextNet, nextHost: 2, dataPath: realm + "-" + name + "-data", volume: true}
	w.nextNet++
	r.clusters = append(r.clusters, c)
	return c
}

// lookup returns the cluster, or nil.
func (w *World) lookup(realm, name string) *clusterState {
	for _, r := range w.realms {
		if r.name != realm {
			continue
		}
		for _, c := range r.clusters {
			if c.name == name {
				return c
			}
		}
	}
	return nil
}

// find returns the node of the cluster, or nil.
func (c *clusterState) find(short string) *nodeState {
	for _, n := range c.nodes {
		if n.short == short {
			return n
		}
	}
	return nil
}

// nodesOf returns the nodes of the cluster, as the scripts name them.
func (w *World) nodesOf(realm, name string) []node {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.lookup(realm, name)
	if c == nil {
		return nil
	}
	out := make([]node, len(c.nodes))
	for i, n := range c.nodes {
		out[i] = node{name: n.short, cluster: name, role: string(n.role)}
	}
	return out
}

// clustersOf returns the names of the clusters of the realm.
func (w *World) clustersOf(realm string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, r := range w.realms {
		if r.name == realm {
			for _, c := range r.clusters {
				out = append(out, c.name)
			}
		}
	}
	return out
}

// InspectContainer answers docker inspect for a node of the world: its
// image and size.
func (w *World) InspectContainer(_ context.Context, name docker.ContainerName) (*docker.ContainerInfo, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.realms {
		for _, c := range r.clusters {
			for _, n := range c.nodes {
				if cluster.ContainerName(r.name, c.name, n.short) != name {
					continue
				}
				info := &docker.ContainerInfo{Name: name, ImageRef: "ghcr.io/gsi-hpc/sind-node:" + c.slurm}
				info.HostConfig.NanoCPUs = int64(cmp.Or(n.cpus, c.cpus, config.DefaultCPUs)) * 1e9
				info.HostConfig.Memory = cmp.Or(n.memory, c.memory, 512<<20)
				return info, nil
			}
		}
	}
	return nil, fmt.Errorf("no such container: %s", name)
}

// apply changes the world as a script says, for the realm of the node.
func (w *World) apply(realmName, op string, n node) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch op {
	case opConnect, opDisconn:
		r := w.realmOf(realmName)
		r.external = slices.DeleteFunc(r.external, func(s string) bool { return s == n.name })
		if op == opConnect {
			r.external = append(r.external, n.name)
		}
		return
	case opMonitor:
		w.realmOf(realmName).monitoring = true
		return
	}
	if op == opCreate {
		c := w.clusterOf(realmName, n.cluster)
		if c.find(n.name) == nil {
			c.nodes = append(c.nodes, &nodeState{
				short: n.name, role: config.Role(n.role), ip: fmt.Sprintf("172.%d.0.%d", c.net, c.nextHost),
				state: cluster.StateStopped, up: map[probe.Service]bool{},
				cpus: n.cpus, memory: int64(n.memoryMB) << 20,
			})
			c.nextHost++
		}
		return
	}
	c := w.lookup(realmName, n.cluster)
	if c == nil {
		return
	}
	ns := c.find(n.name)
	if ns == nil {
		return
	}
	switch op {
	case opStart:
		ns.state, ns.up, ns.failed = cluster.StateRunning, map[probe.Service]bool{}, nil
	case opReady:
		ns.state, ns.up, ns.calm, ns.failed = cluster.StateRunning, map[probe.Service]bool{}, readyCalm, nil
		for _, s := range services(ns.role) {
			ns.up[s] = true
		}
	case opRestart:
		if svc := probe.Service(n.unit); ns.state == cluster.StateRunning && slices.Contains(services(ns.role), svc) {
			ns.up[svc], ns.calm = true, readyCalm
			delete(ns.failed, svc)
		}
	case opDrain:
		ns.drain = cmp.Or(n.reason, "drained")
	case opResume:
		ns.drain = ""
	case opStop:
		ns.state, ns.up, ns.failed = cluster.StateStopped, map[probe.Service]bool{}, nil
		ns.stopped, ns.exit, ns.oom = w.clock(), 0, false
	case opPause:
		ns.state = cluster.StatePaused
	case opUnpause:
		ns.state = cluster.StateRunning
	case opRemove:
		c.nodes = slices.DeleteFunc(c.nodes, func(o *nodeState) bool { return o == ns })
	}
	w.prune()
}

// prune forgets the clusters without containers, and the realms without
// clusters, as Docker does: what has no container does not exist.
func (w *World) prune() {
	for _, r := range w.realms {
		r.clusters = slices.DeleteFunc(r.clusters, func(c *clusterState) bool { return len(c.nodes) == 0 })
	}
	w.realms = slices.DeleteFunc(w.realms, func(r *realm) bool { return len(r.clusters) == 0 })
}

// readyCalm is how many times Drift leaves a node alone once its services
// are up, so that a fix holds for ten minutes (at driftEvery) before the
// service fails again.
const readyCalm = 30

// Drift fails the slurmd of the node worker-4 of dev again a while after it
// came up, as a flaky service does: not while the node is calm, after its
// services came up. A failure stays until a restart or a reboot fixes it, so
// that the page of the node shows it steadily. It returns whether it
// changed anything.
func (w *World) Drift() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.lookup("sind", "dev")
	if c == nil {
		return false
	}
	n := c.find("worker-4")
	if n == nil || n.state != cluster.StateRunning || !n.up[probe.ServiceSlurmd] {
		return false
	}
	if n.calm > 0 {
		n.calm--
		return false
	}
	n.up[probe.ServiceSlurmd] = false
	n.markFailed(probe.ServiceSlurmd, w.clock())
	return true
}

// clock returns the time of the world.
func (w *World) clock() time.Time {
	if w.now == nil {
		return time.Now()
	}
	return w.now()
}

// markFailed notes that the service failed at at.
func (n *nodeState) markFailed(svc probe.Service, at time.Time) {
	if n.failed == nil {
		n.failed = map[probe.Service]time.Time{}
	}
	n.failed[svc] = at
}

// The ages of what was wrong before the demo started, as Publish stamps
// them (stamp): a service failed seededFailed before, a container stopped
// seededStopped before.
const (
	seededFailed  = 13 * time.Minute
	seededStopped = 43 * time.Minute
)

// stamp gives a time to what went wrong with the node before the demo
// started: the services that are down and the stop of its container.
func (n *nodeState) stamp(now time.Time) {
	if n.state == cluster.StateStopped && n.stopped.IsZero() {
		n.stopped = now.Add(-seededStopped)
	}
	if n.state != cluster.StateRunning {
		return
	}
	for _, svc := range services(n.role) {
		if !n.up[svc] && n.failed[svc].IsZero() && len(n.up) > 0 {
			n.markFailed(svc, now.Add(-seededFailed))
		}
	}
}

// fault returns what sind ui would read of why the node needs a look, and
// whether it does: the exit of its stopped container, or each service of
// it that failed, with the end of its journal.
func (n *nodeState) fault() (store.Fault, bool) {
	if n.state == cluster.StateStopped && !n.stopped.IsZero() {
		return store.Fault{Exit: &store.Exit{Code: n.exit, OOMKilled: n.oom, At: n.stopped}}, true
	}
	var f store.Fault
	for _, svc := range slices.Sorted(maps.Keys(n.failed)) {
		at := n.failed[svc]
		f.Units = append(f.Units, store.Unit{Name: string(svc), State: "failed", Result: "exit-code", Status: 1, Since: at, Journal: failureJournal(string(svc), at)})
	}
	return f, len(f.Units) > 0
}

// FailedSince returns when the unit of the container failed, and whether it
// is down, for the journal of the demo.
func (w *World) FailedSince(container, unit string) (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.realms {
		for _, c := range r.clusters {
			for _, n := range c.nodes {
				if string(cluster.ContainerName(r.name, c.name, n.short)) != container {
					continue
				}
				at, ok := n.failed[probe.Service(unit)]
				return at, ok
			}
		}
	}
	return time.Time{}, false
}

// StoppedAt returns when the container stopped, and whether it is
// stopped, for the logs of the demo.
func (w *World) StoppedAt(container string) (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.realms {
		for _, c := range r.clusters {
			for _, n := range c.nodes {
				if string(cluster.ContainerName(r.name, c.name, n.short)) == container {
					return n.stopped, n.state == cluster.StateStopped && !n.stopped.IsZero()
				}
			}
		}
	}
	return time.Time{}, false
}

// FactsOf returns which workers of the cluster of the container answer,
// and which are down, for the controller log of the demo.
func (w *World) FactsOf(container string) Facts {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.realms {
		for _, c := range r.clusters {
			if !slices.ContainsFunc(c.nodes, func(n *nodeState) bool { return string(cluster.ContainerName(r.name, c.name, n.short)) == container }) {
				continue
			}
			var f Facts
			for _, n := range c.nodes {
				switch {
				case n.role != config.RoleWorker:
				case n.state != cluster.StateRunning || !n.up[probe.ServiceSlurmd]:
					f.Down = append(f.Down, n.short)
				default:
					f.Up = append(f.Up, n.short)
				}
			}
			return f
		}
	}
	return Facts{}
}

// clusterStateOf returns the state of a cluster from the states of its
// nodes, of which it has at least one.
func clusterStateOf(c *clusterState) cluster.State {
	st := c.nodes[0].state
	for _, n := range c.nodes {
		if n.state != st {
			return cluster.StateMixed
		}
	}
	return st
}

// Publish writes the world to the store, and clears what an earlier call
// wrote and the world no longer has.
func (w *World) Publish(st *store.Store) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prune()
	slices.SortFunc(w.realms, func(a, b *realm) int { return strings.Compare(a.name, b.name) })
	st.SetDocker(store.DockerState{Checked: true, MemTotal: HostMemory, NCPU: 16})
	var realms []cluster.RealmSummary
	now := map[string]bool{}
	var keys []store.StatusKey
	for _, r := range w.realms {
		now[r.name] = true
		realms = append(realms, cluster.RealmSummary{Name: r.name, Clusters: len(r.clusters)})
		w.publishRealm(st, r, &keys)
	}
	st.SetRealms(realms)
	for name := range w.published {
		if !now[name] {
			w.clear(st, name)
		}
	}
	for _, k := range w.statuses {
		if !slices.Contains(keys, k) {
			st.SetStatus(k.Realm, k.Cluster, nil)
			st.SetSlurm(k.Realm, k.Cluster, nil)
			st.SetFaults(k.Realm, k.Cluster, nil)
		}
	}
	w.published, w.statuses = now, keys
}

// connectable returns the containers outside sind that are not on the
// mesh of r, each on its own networks and the meshes that it joins.
func (w *World) connectable(r *realm) []store.Outside {
	var out []store.Outside
	for _, o := range w.outside {
		if slices.Contains(r.external, o.Name) {
			continue
		}
		o.Networks = slices.Clone(o.Networks)
		for _, other := range w.realms {
			if slices.Contains(other.external, o.Name) {
				o.Networks = append(o.Networks, string(mesh.NewManager(nil, other.name).NetworkName()))
			}
		}
		out = append(out, o)
	}
	return out
}

// clear removes what the store holds of a realm that is gone.
func (w *World) clear(st *store.Store, name string) {
	st.SetClusters(name, nil)
	st.SetNodes(name, nil)
	st.SetNetworks(name, nil)
	st.SetVolumes(name, nil)
	st.SetMesh(name, store.Mesh{})
	st.SetMonitoring(name, nil)
}

// publishRealm writes one realm.
func (w *World) publishRealm(st *store.Store, r *realm, keys *[]store.StatusKey) {
	m := mesh.NewManager(nil, r.name)
	var (
		clusters []cluster.Summary
		nodes    []cluster.NodeSummary
		networks = []cluster.NetworkSummary{{Name: string(m.NetworkName()), Driver: "bridge", Subnet: fmt.Sprintf("172.%d.0.0/16", r.net), Gateway: fmt.Sprintf("172.%d.0.1", r.net)}}
		volumes  = []cluster.VolumeSummary{{Name: string(m.SSHVolumeName()), Driver: "local"}}
		records  []mesh.DNSRecord
		watched  []cluster.MonitoredCluster
	)
	for _, c := range r.clusters {
		sum := cluster.Summary{Name: c.name, SlurmVersion: c.slurm, State: clusterStateOf(c), NodeCount: len(c.nodes)}
		for _, n := range c.nodes {
			switch n.role {
			case config.RoleController:
				sum.Controllers++
			case config.RoleDB:
				sum.DBs++
			case config.RoleAPI:
				sum.APIs++
			case config.RoleSubmitter:
				sum.Submitters++
			default:
				sum.Workers++
			}
			fqdn := fmt.Sprintf("%s.%s.%s.sind", n.short, c.name, r.name)
			nodes = append(nodes, cluster.NodeSummary{
				Container: string(cluster.ContainerName(r.name, c.name, n.short)), Cluster: c.name, Role: n.role,
				FQDN: fqdn, IP: n.ip, State: n.state,
			})
			records = append(records, mesh.DNSRecord{Hostname: fqdn, IP: n.ip})
		}
		clusters = append(clusters, sum)
		net := cluster.NetworkName(r.name, c.name)
		networks = append(networks, cluster.NetworkSummary{Name: string(net), Driver: "bridge", Subnet: fmt.Sprintf("172.%d.0.0/16", c.net), Gateway: fmt.Sprintf("172.%d.0.1", c.net)})
		volumes = append(volumes,
			cluster.VolumeSummary{Name: string(cluster.VolumeName(r.name, c.name, cluster.VolumeConfig)), Driver: "local"},
			cluster.VolumeSummary{Name: string(cluster.VolumeName(r.name, c.name, cluster.VolumeMunge)), Driver: "local"})
		if c.volume {
			volumes = append(volumes, cluster.VolumeSummary{Name: c.dataPath, Driver: "local"})
		}
		if c.monitoring {
			watched = append(watched, cluster.MonitoredCluster{Name: c.name, Nodes: len(c.nodes), SlurmVersion: c.slurm, Slurm: slurmCountsOf(c)})
		}
		st.SetStatus(r.name, c.name, w.statusOf(r, c, m))
		st.SetSlurm(r.name, c.name, slurmOf(c))
		faults := map[string]store.Fault{}
		for _, n := range c.nodes {
			n.stamp(w.clock())
			if f, ok := n.fault(); ok {
				faults[n.short] = f
			}
		}
		st.SetFaults(r.name, c.name, faults)
		*keys = append(*keys, store.StatusKey{Realm: r.name, Cluster: c.name})
	}
	st.SetClusters(r.name, clusters)
	st.SetNodes(r.name, nodes)
	st.SetNetworks(r.name, networks)
	st.SetVolumes(r.name, volumes)
	st.SetMesh(r.name, store.Mesh{
		Info: &mesh.Info{
			Network: string(m.NetworkName()), DNSContainer: string(m.DNSContainerName()), DNSIP: fmt.Sprintf("172.%d.255.254", r.net),
			DNSZone: r.name + ".sind", DNSImage: "coredns/coredns:1.12.1",
			SSHContainer: string(m.SSHContainerName()), SSHVolume: string(m.SSHVolumeName()), SSHImage: "ghcr.io/gsi-hpc/sind-node:" + slurmNew,
		},
		Records:     records,
		External:    slices.Clone(r.external),
		Connectable: w.connectable(r),
	})
	if r.monitoring {
		st.SetMonitoring(r.name, monitoringOf(r, watched))
	} else {
		st.SetMonitoring(r.name, nil)
	}
}

// demoPending is how many jobs wait in the queue of a monitored cluster
// of the demo.
const demoPending = 3

// slurmCountsOf returns the numbers that Prometheus would have of a
// monitored cluster: its workers are Slurm's nodes, one whose slurmd does
// not run is down, one that the demo drained is in drain, and each other
// one runs a job.
func slurmCountsOf(c *clusterState) *cluster.SlurmCounts {
	s := &cluster.SlurmCounts{JobsPending: demoPending}
	for _, n := range c.nodes {
		if n.role != config.RoleWorker {
			continue
		}
		s.Nodes++
		switch {
		case n.state != cluster.StateRunning || !n.up[probe.ServiceSlurmd]:
			s.NodesDown++
		case n.drain != "":
			s.NodesDrain++
		default:
			s.JobsRunning++
		}
	}
	return s
}

// monitoringOf returns the monitoring stack of the realm.
func monitoringOf(r *realm, watched []cluster.MonitoredCluster) *cluster.Monitoring {
	svc := func(name, image string, host, port int, dns bool) cluster.MonitoringService {
		ip := fmt.Sprintf("172.%d.255.%d", r.net, host)
		url := fmt.Sprintf("http://%s.%s.sind:%d", name, r.name, port)
		if !dns {
			ip = fmt.Sprintf("172.%d.0.9", r.net)
			url = fmt.Sprintf("http://%s:%d", ip, port)
		}
		return cluster.MonitoringService{
			Name: observability.Service(name), Container: observability.ContainerName(r.name, observability.Service(name)), Image: image, IP: ip, URL: url,
			State: "running", Ready: true, Checked: true,
		}
	}
	return &cluster.Monitoring{
		Realm: r.name,
		Services: []cluster.MonitoringService{
			svc("grafana", "grafana/grafana:13.2.3", 253, 3000, true),
			svc("prometheus", "prom/prometheus:v3.15.0", 252, 9090, true),
			svc("loki", "grafana/loki:3.7.8", 251, 3100, true),
			svc("alloy", "grafana/alloy:v1.20.1", 0, 12345, false),
		},
		Clusters: watched,
	}
}

// statusOf returns the status of a cluster, as the page of the cluster
// shows it.
func (w *World) statusOf(r *realm, c *clusterState, m *mesh.Manager) *cluster.Status {
	st := &cluster.Status{Name: c.name, SlurmVersion: c.slurm, State: clusterStateOf(c), Monitoring: c.monitoring}
	pair := c.find(cluster.ControllerBackupShortName) != nil
	for _, n := range c.nodes {
		h := &cluster.NodeHealth{State: n.state, IP: n.ip, Services: cluster.ServiceHealth{}}
		for _, s := range services(n.role) {
			h.Services[s] = n.up[s]
		}
		if pair && n.role == config.RoleController {
			h.HA = &cluster.HAStatus{Position: "primary", InControl: n.state == cluster.StateRunning}
			if n.short == cluster.ControllerBackupShortName {
				h.HA = &cluster.HAStatus{Position: "backup"}
			}
		}
		st.Nodes = append(st.Nodes, &cluster.NodeStatus{Name: n.short + "." + c.name, Role: n.role, Managed: true, Health: h})
	}
	meshOK := fmt.Sprintf("172.%d.0.0/16", r.net)
	st.Network = &cluster.NetworkHealth{
		Mesh: true, MeshName: string(m.NetworkName()), MeshDriver: "bridge", MeshSubnet: meshOK, MeshGateway: fmt.Sprintf("172.%d.0.1", r.net),
		DNS: true, DNSName: string(m.DNSContainerName()), SSH: true, SSHName: string(m.SSHContainerName()),
		Cluster: true, ClusterName: string(cluster.NetworkName(r.name, c.name)), ClusterDriver: "bridge",
		ClusterSubnet: fmt.Sprintf("172.%d.0.0/16", c.net), ClusterGateway: fmt.Sprintf("172.%d.0.1", c.net),
	}
	if r.monitoring {
		yes := true
		st.Network.Grafana, st.Network.Prometheus, st.Network.Loki, st.Network.Alloy = &yes, &yes, &yes, &yes
	}
	st.Mounts = []cluster.MountPoint{
		{Path: "/etc/slurm", Source: string(cluster.VolumeName(r.name, c.name, cluster.VolumeConfig)), Type: config.StorageVolume, OK: true},
		{Path: "/etc/munge", Source: string(cluster.VolumeName(r.name, c.name, cluster.VolumeMunge)), Type: config.StorageVolume, OK: true},
	}
	data := cluster.MountPoint{Path: cluster.DefaultDataMountPath, Source: c.dataPath, Type: config.StorageVolume, OK: true, Data: true}
	if !c.volume {
		data.Type = config.StorageHostPath
	}
	st.Mounts = append(st.Mounts, data)
	return st
}

// configure changes the cluster, if it is there.
func (w *World) configure(realm, name string, fn func(*clusterState)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c := w.lookup(realm, name); c != nil {
		fn(c)
	}
}
