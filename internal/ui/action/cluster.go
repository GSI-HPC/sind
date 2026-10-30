// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

func init() {
	register(
		Action{ID: "create-worker", Build: buildCreateWorker, Cluster: formCluster},
		Action{ID: "delete-cluster", Build: buildDeleteCluster, Cluster: formCluster, Typed: formCluster},
		Action{ID: "delete-clusters", Build: buildDeleteClusters, Typed: typedClusters, TypedWhat: func(Form) string { return "the number of clusters" }},
		Action{ID: "delete-worker", Build: buildDeleteWorker, Cluster: formCluster, Typed: deleteWorkerTyped, TypedWhat: deleteWorkerTypedWhat},
		Action{ID: "delete-realm", Build: buildDeleteRealm, Typed: func(f Form) string { return Realm(f) }},
	)
}

// deleteWorkerTyped is what the user types to delete workers: the name of
// the worker when one goes, else how many go, so that the confirmation
// names what goes and not the cluster, which stays; nothing while the form
// names no worker.
func deleteWorkerTyped(f Form) string {
	switch names := NodeNames(f); len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strconv.Itoa(len(names))
	}
}

// deleteWorkerTypedWhat says what deleteWorkerTyped is: the name of the
// worker, or the number of workers.
func deleteWorkerTypedWhat(f Form) string {
	if len(NodeNames(f)) == 1 {
		return "the name of the worker"
	}
	return "the number of workers"
}

func buildCreateWorker(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	// A wrong field is a problem at the field, and the command takes its
	// default until the user fixes it, as New cluster does.
	var ps problems
	count, err := number(f, "count", "How many", 1, maxCount, -1)
	if err != nil {
		ps.add("count", "How many", true, fmt.Errorf("write a number from 1 to %d", maxCount))
		count = 1
	}
	words := append([]string{"create", "worker"}, clusterWord(cl.Name)...)
	if count != 1 {
		words = append(words, "--count", fmt.Sprint(count))
	}
	nodes := c.Snap.ClusterNodes(realm, cl.Name)
	size := templateSize(c, nodes, c.Snap.Status(realm, cl.Name))
	if s := strings.TrimSpace(f.Get("cpus")); s != "" {
		if cpus, err := number(f, "cpus", "CPUs", 1, maxCPUs, -1); err != nil {
			ps.add("cpus", "CPUs", true, fmt.Errorf("write a number from 1 to %d", maxCPUs))
		} else {
			words = append(words, "--cpus", fmt.Sprint(cpus))
			size.cpus = cpus
		}
	}
	if m := strings.TrimSpace(f.Get("memory")); m != "" {
		if m, err := memorySize(m); err != nil {
			ps.add("memory", "Memory", true, err)
		} else {
			words = append(words, "--memory", m)
			size.memoryMB, _ = config.MemoryMB(m) // checked
		}
	}
	cmd := NewCommand(sindArgv(realm, words...)...)
	added := nextWorkers(nodes, count)
	names := added[0]
	if count > 1 {
		names += " … " + added[count-1]
	}
	cmd.Summary = fmt.Sprintf("%d × %s, %s: %s", count, size.cpusLabel(), size.memoryLabel(), names)
	for _, n := range added {
		cmd.Nodes = append(cmd.Nodes, PlannedNode{Short: n, Role: string(config.RoleWorker)})
	}
	cmd.OK = "Add " + countOr(added, "workers")
	if size.why != "" {
		cmd.Notes = append(cmd.Notes, size.why)
	}
	cmd.Notes = append(cmd.Notes, workerSlurmNote(c.Snap.Status(realm, cl.Name)))
	b := hostBudget(c, count, count*size.memoryMB, count*size.cpus)
	budgetProblems(f, &cmd, b, workersOver(size.memoryMB), &ps)
	cpuWarning(&cmd, b, "Each new worker", size.cpus)
	cmd.Problems = ps
	return cmd, nil
}

// workerSlurmNote says what Slurm does with new workers of the cluster
// with the status st, which may be nil: they join the partition all, and
// sind reconfigures slurmctld, which running jobs survive; on an
// unmanaged cluster sind does nothing in Slurm.
func workerSlurmNote(st *cluster.Status) string {
	if st != nil {
		for _, n := range st.Nodes {
			if n.Role == config.RoleController && !n.Managed {
				return "The cluster is unmanaged, so the new workers are too: sind writes no Slurm config for them and starts no slurmd."
			}
		}
	}
	return "The new workers join the partition `" + slurm.PartitionAll + "`. Your own `PartitionName` lines do not change. sind then runs `scontrol reconfigure` on the controller, and running jobs go on."
}

// workerSize is the size that new workers get where the form leaves it
// out, and where it comes from.
type workerSize struct {
	// cpus and memoryMB are 0 where sind ui does not know them.
	cpus, memoryMB int
	// of is the worker that new workers are like, "" for none.
	of string
	// why says why sind ui does not know the size, "" where it does.
	why string
}

// cpusLabel writes the CPUs of a new worker.
func (s workerSize) cpusLabel() string {
	if s.cpus == 0 {
		return "the CPUs of " + s.of
	}
	return fmt.Sprintf("%d CPU%s", s.cpus, plural(s.cpus))
}

// memoryLabel writes the memory of a new worker.
func (s workerSize) memoryLabel() string {
	if s.memoryMB == 0 {
		return "the memory of " + s.of
	}
	return MemoryLabel(s.memoryMB)
}

// templateSize returns the size of the newest worker of nodes, of the
// cluster with the status st, which new workers take, or sind's defaults
// for a cluster without a worker. A limit that the worker has none of is
// sind's default too, as sind create worker gives it.
func templateSize(c Context, nodes []cluster.NodeSummary, st *cluster.Status) workerSize {
	defaultMB, _ := config.MemoryMB(config.DefaultMemory) // a constant
	newest, ok := NewestWorker(nodes, st)
	if !ok {
		return workerSize{cpus: config.DefaultCPUs, memoryMB: defaultMB}
	}
	size := workerSize{of: ShortName(newest)}
	if c.Worker == nil {
		return size
	}
	facts := c.Worker(newest.Container)
	if facts.Err != "" {
		size.why = "sind ui cannot look at the size of " + size.of + ": " + facts.Err + "."
		return size
	}
	size.cpus = cmp.Or(facts.CPUs, config.DefaultCPUs)
	size.memoryMB = cmp.Or(int(facts.Memory>>20), defaultMB)
	return size
}

// plural is the "s" of a count other than one.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func buildDeleteCluster(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	cmd := NewCommand(sindArgv(realm, append([]string{"delete", "cluster"}, clusterWord(cl.Name)...)...)...)
	cmd.OK = "Delete cluster " + cl.Name
	g := clusterGoesOf(c, realm, cl.Name)
	for _, d := range g.data {
		cmd.Goes = append(cmd.Goes, d.Line())
	}
	cmd.Goes = append(cmd.Goes,
		fmt.Sprintf("%d container%s: %s", len(g.shorts), plural(len(g.shorts)), strings.Join(g.shorts, ", ")),
		"the network "+string(cluster.NetworkName(realm, cl.Name)),
	)
	if len(g.volumes) > 0 {
		cmd.Goes = append(cmd.Goes, "the volume"+plural(len(g.volumes))+" "+strings.Join(g.volumes, ", "))
	}
	cmd.Goes = append(cmd.Goes, "its jobs, its accounting and its Slurm state")
	if g.unseen {
		cmd.Stays = append(cmd.Stays, "the host directories it mounts, if any")
	}
	for _, m := range g.dirs {
		cmd.Stays = append(cmd.Stays, fmt.Sprintf("the host directory %s, mounted at %s", m.Source, m.Path))
	}
	switch others := len(c.Snap.Clusters[realm]) - 1; {
	case others == 1:
		cmd.Stays = append(cmd.Stays, "the other cluster of realm "+realm+", and its mesh")
	case others > 1:
		cmd.Stays = append(cmd.Stays, fmt.Sprintf("the other %d clusters of realm %s, and its mesh", others, realm))
	default:
		cmd.Goes = append(cmd.Goes, "the mesh, the DNS server and the SSH relay of realm "+realm+", as it is its last cluster")
	}
	cmd.Ack = lossAck([]Loss{{Data: g.data}})
	return cmd, nil
}

// Loss is what a delete removes and keeps of one cluster, or of a realm
// whose last cluster goes: a row of the table of Delete clusters and
// Delete realm.
type Loss struct {
	// Realm and Cluster name the cluster; Cluster is "" for the row of the
	// realm.
	Realm, Cluster string
	// About says what the row is now, as "2 nodes · running".
	About string
	// Data are the volumes of the data of the users that go, the worst
	// loss, which the row names first.
	Data []DataVolume
	// Goes is the rest that goes, and Stays what stays.
	Goes  string
	Stays []Kept
}

// Name names the row: the cluster, or the realm.
func (l Loss) Name() string {
	if l.Cluster == "" {
		return "realm " + l.Realm
	}
	return l.Cluster
}

// DataVolume is a volume of a cluster that holds the data of its users.
type DataVolume struct {
	// Name is the volume, Cluster its cluster, and What what it holds, as
	// "every file in /data".
	Name, Cluster, What string
	// Home says that it holds the home directories of the users, else it
	// is the data volume.
	Home bool
}

// Kind names the volume: "the data volume" or "the home volume".
func (d DataVolume) Kind() string {
	if d.Home {
		return "the home volume"
	}
	return "the data volume"
}

// Line says what goes with the volume: "the data volume sind-dev-data:
// every file in /data".
func (d DataVolume) Line() string { return d.Kind() + " " + d.Name + ": " + d.What }

// Kept is a thing that a delete keeps, with what it is: "/home/demo" and
// "the host directory at /data".
type Kept struct{ What, Note string }

// clusterGoes is what a delete of a cluster removes and keeps, in parts.
type clusterGoes struct {
	// shorts are its nodes, volumes the names of its volumes of sind's
	// own, and kinds their kinds.
	shorts, volumes, kinds []string
	data                   []DataVolume
	// dirs are the host directories that it mounts, which stay; unseen
	// says that sind ui has not read them.
	dirs   []cluster.MountPoint
	unseen bool
}

// clusterGoesOf returns what sind delete cluster removes of the cluster
// and what it keeps. The volumes are those of its names, not others that
// start alike, such as those of cluster dev-gpu.
func clusterGoesOf(c Context, realm, name string) clusterGoes {
	g := clusterGoes{shorts: shortNames(c.Snap.ClusterNodes(realm, name))}
	st := c.Snap.Status(realm, name)
	for _, t := range cluster.AllVolumeTypes {
		vol := string(cluster.VolumeName(realm, name, t))
		if !slices.ContainsFunc(c.Snap.Volumes[realm], func(v cluster.VolumeSummary) bool { return v.Name == vol }) {
			continue
		}
		switch t {
		case cluster.VolumeData:
			path := cluster.DefaultDataMountPath
			if st != nil {
				for _, m := range st.Mounts {
					if m.Data {
						path = m.Path
					}
				}
			}
			g.data = append(g.data, DataVolume{Name: vol, Cluster: name, What: "every file in " + path})
		case cluster.VolumeHome:
			g.data = append(g.data, DataVolume{Name: vol, Cluster: name, What: "the home directories of its users", Home: true})
		default:
			g.volumes, g.kinds = append(g.volumes, vol), append(g.kinds, string(t))
		}
	}
	if st == nil {
		g.unseen = true
		return g
	}
	for _, m := range st.Mounts {
		if m.Type == config.StorageHostPath {
			g.dirs = append(g.dirs, m)
		}
	}
	return g
}

// clusterLoss is the row of the cluster in the table of a delete; named
// says that the row names the realm of the cluster, as the table has
// clusters of several realms.
func clusterLoss(c Context, realm, name string, named bool) Loss {
	g := clusterGoesOf(c, realm, name)
	l := Loss{Realm: realm, Cluster: name, Data: g.data}
	if g.unseen {
		l.Stays = append(l.Stays, Kept{What: "the host directories it mounts, if any"})
	}
	for _, m := range g.dirs {
		l.Stays = append(l.Stays, Kept{What: m.Source, Note: "the host directory at " + m.Path})
	}
	about := []string{countOf(len(g.shorts), "node")}
	if named {
		about = append([]string{"realm " + realm}, about...)
	}
	if cl, _ := c.Snap.Cluster(realm, name); cl.State != "" {
		about = append(about, string(cl.State))
	}
	l.About = strings.Join(about, " · ")
	var goes []string
	if len(g.shorts) > 0 {
		goes = append(goes, countOf(len(g.shorts), "container"))
	}
	goes = append(goes, "its network")
	if len(g.kinds) > 0 {
		goes = append(goes, "its "+JoinWords(g.kinds)+" volume"+plural(len(g.kinds)))
	}
	l.Goes = strings.Join(goes, ", ")
	return l
}

// realmLoss is the row of a realm whose last cluster goes: what its
// clusters share goes, and the containers that sind did not make stay.
func realmLoss(c Context, realm string) Loss {
	l := Loss{Realm: realm, About: "no cluster is left"}
	goes := []string{"the mesh", "the DNS server", "the SSH relay"}
	if c.Snap.Monitorings[realm] != nil {
		goes = append(goes, "the monitoring stack")
	}
	l.Goes = JoinWords(append(goes, "the exported SSH config"))
	// sind ui reads the containers on the mesh with the mesh; until it has,
	// it cannot say whether there are any.
	switch m, ok := c.Snap.Meshes[realm]; {
	case !ok || m.Err != "":
		l.Stays = append(l.Stays, Kept{What: "the containers that sind did not make, if any", Note: "sind detaches them"})
	default:
		for _, e := range m.External {
			l.Stays = append(l.Stays, Kept{What: e, Note: "sind detaches it"})
		}
	}
	return l
}

// lossAck is the sentence that the user ticks to confirm that the data
// volumes of the rows go, "" when none does.
func lossAck(rows []Loss) string {
	var data []DataVolume
	for _, r := range rows {
		data = append(data, r.Data...)
	}
	switch len(data) {
	case 0:
		return ""
	case 1:
		d := data[0]
		return fmt.Sprintf("I know that %s `%s` of %s goes, with %s.", d.Kind(), d.Name, d.Cluster, d.What)
	}
	names := make([]string, len(data))
	for i, d := range data {
		names[i] = fmt.Sprintf("`%s` of %s", d.Name, d.Cluster)
	}
	return "I know that the volumes " + JoinWords(names) + " go, with every file in them."
}

// GoneRealms returns the realms whose last clusters the refs, each
// realm/name, all name: a delete of them removes the realm too.
func GoneRealms(sn store.Snapshot, refs []string) []string {
	n := map[string]int{}
	for _, ref := range refs {
		realm, name, _ := strings.Cut(ref, "/")
		if _, ok := sn.Cluster(realm, name); ok {
			n[realm]++
		}
	}
	var out []string
	for realm, k := range n {
		if k == len(sn.Clusters[realm]) {
			out = append(out, realm)
		}
	}
	slices.SortFunc(out, natsort.Compare)
	return out
}

// countOf writes n things: 1 cluster, 2 clusters.
func countOf(n int, thing string) string {
	return fmt.Sprintf("%d %s%s", n, thing, plural(n))
}

// maxClusters is the most clusters that one delete of several takes.
const maxClusters = 32

// ClusterRefs returns the clusters that the field "clusters" names, each as
// realm/name, without repeats, in the order of the form. A value may hold
// several, separated by commas or spaces.
func ClusterRefs(f Form) []string {
	var out []string
	for _, v := range f["clusters"] {
		for _, ref := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
			if !slices.Contains(out, ref) {
				out = append(out, ref)
			}
		}
	}
	return out
}

// typedClusters is what the user types to delete several clusters: their
// count. Without clusters there is nothing to confirm, and the build fails.
func typedClusters(f Form) string {
	if n := len(ClusterRefs(f)); n > 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// buildDeleteClusters deletes several clusters, of one realm or of
// several: sind delete cluster takes one, so each is a part of its own.
func buildDeleteClusters(f Form, c Context) (Command, error) {
	refs := ClusterRefs(f)
	if len(refs) == 0 {
		return Command{}, &Missing{Field: "clusters", Text: "pick the clusters to delete"}
	}
	if len(refs) > maxClusters {
		return Command{}, fmt.Errorf("clusters: pick at most %d clusters", maxClusters)
	}
	parts := make([]Part, 0, len(refs))
	for _, ref := range refs {
		realm, name, ok := strings.Cut(ref, "/")
		if !ok {
			return Command{}, fmt.Errorf("clusters: %q is not REALM/CLUSTER", ref)
		}
		if err := config.CheckName("realm", realm); err != nil {
			return Command{}, fmt.Errorf("clusters: %w", err)
		}
		if err := config.CheckName("cluster", name); err != nil {
			return Command{}, fmt.Errorf("clusters: %w", err)
		}
		if _, ok := c.Snap.Cluster(realm, name); !ok {
			return Command{}, fmt.Errorf("realm %s has no cluster %s", realm, name)
		}
		argv := sindArgv(realm, append([]string{"delete", "cluster"}, clusterWord(name)...)...)
		parts = append(parts, Part{Action: "delete-cluster", Realm: realm, Cluster: name, Argv: argv, Display: Quote(argv)})
	}
	slices.SortFunc(parts, func(a, b Part) int {
		return cmp.Or(natsort.Compare(a.Realm, b.Realm), natsort.Compare(a.Cluster, b.Cluster))
	})
	cmd := NewParts(parts)
	cmd.OK = "Delete " + countOf(len(parts), "cluster")
	cmd.Notes = append(cmd.Notes, "sind ui runs one command for each cluster. The commands of one realm run one after the other, as each waits for the lock of the realm.")
	gone := GoneRealms(c.Snap, refs)
	named := parts[0].Realm != parts[len(parts)-1].Realm
	for i, p := range parts {
		cmd.Losses = append(cmd.Losses, clusterLoss(c, p.Realm, p.Cluster, named))
		if i+1 < len(parts) && parts[i+1].Realm == p.Realm {
			continue
		}
		// The last part of its realm.
		if slices.Contains(gone, p.Realm) {
			cmd.Losses = append(cmd.Losses, realmLoss(c, p.Realm))
			continue
		}
		n := 0
		for _, q := range parts {
			if q.Realm == p.Realm {
				n++
			}
		}
		switch others := len(c.Snap.Clusters[p.Realm]) - n; others {
		case 1:
			cmd.Stays = append(cmd.Stays, "the other cluster of realm "+p.Realm+" and its mesh")
		default:
			cmd.Stays = append(cmd.Stays, fmt.Sprintf("the other %d clusters of realm %s and its mesh", others, p.Realm))
		}
	}
	cmd.Stays = append(cmd.Stays, "the images")
	cmd.Ack = lossAck(cmd.Losses)
	return cmd, nil
}

func buildDeleteWorker(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	picked, err := pickNodes(f, c, realm, cl.Name, "deleted")
	if err != nil {
		return Command{}, err
	}
	shorts := shortNames(picked)
	cmd := NewCommand(sindArgv(realm, append([]string{"delete", "worker"}, nodeWords(cl.Name, shorts)...)...)...)
	cmd.OK = "Delete " + countOr(shorts, "workers")
	if len(picked) == 1 {
		cmd.Goes = append(cmd.Goes, "the container "+picked[0].Container+" and its DNS record", "its Slurm node: jobs on it fail")
	} else {
		cmd.Goes = append(cmd.Goes, "the containers of "+NodeSet(shorts)+" and their DNS records", "their Slurm nodes: jobs on them fail")
	}
	var left []string
	for _, n := range c.Snap.ClusterNodes(realm, cl.Name) {
		if n.Role == config.RoleWorker && !slices.Contains(shorts, ShortName(n)) {
			left = append(left, ShortName(n))
		}
	}
	if len(left) > 0 {
		cmd.Stays = append(cmd.Stays, fmt.Sprintf("%s with %s (%s), its controller and its data", cl.Name, countOf(len(left), "worker"), NodeSet(left)))
	} else {
		cmd.Stays = append(cmd.Stays, cl.Name+" with no worker, its controller and its data: jobs wait until you add workers")
	}
	return cmd, nil
}

func buildDeleteRealm(f Form, c Context) (Command, error) {
	realm, err := checkRealm(f)
	if err != nil {
		return Command{}, err
	}
	if _, ok := c.Snap.Realm(realm); !ok {
		return Command{}, fmt.Errorf("realm: sind ui sees no realm %s", realm)
	}
	// The command always names the realm, so that a copy of it deletes
	// this realm and not the one SIND_REALM names in another shell.
	cmd := NewCommand("sind", "--realm", realm, "delete", "realm")
	cmd.OK = "Delete realm " + realm
	names := make([]string, 0, len(c.Snap.Clusters[realm]))
	for _, cl := range c.Snap.Clusters[realm] {
		names = append(names, cl.Name)
	}
	slices.SortFunc(names, natsort.Compare)
	for _, name := range names {
		cmd.Losses = append(cmd.Losses, clusterLoss(c, realm, name, false))
	}
	cmd.Losses = append(cmd.Losses, realmLoss(c, realm))
	var others []string
	for _, r := range c.Snap.Realms {
		if r.Name != realm {
			others = append(others, r.Name)
		}
	}
	switch len(others) {
	case 0:
	case 1:
		switch n := len(c.Snap.Clusters[others[0]]); n {
		case 0:
			cmd.Stays = append(cmd.Stays, "the other realm "+others[0])
		case 1:
			cmd.Stays = append(cmd.Stays, "the other realm "+others[0]+" and its cluster")
		default:
			cmd.Stays = append(cmd.Stays, fmt.Sprintf("the other realm %s and its %d clusters", others[0], n))
		}
	default:
		cmd.Stays = append(cmd.Stays, "the other realms "+JoinWords(others)+" and their clusters")
	}
	cmd.Stays = append(cmd.Stays, "the images")
	cmd.Ack = lossAck(cmd.Losses)
	return cmd, nil
}
