// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

func init() {
	register(
		Action{ID: "create-worker", Build: buildCreateWorker, Cluster: formCluster},
		Action{ID: "delete-cluster", Build: buildDeleteCluster, Cluster: formCluster, Typed: formCluster},
		Action{ID: "delete-clusters", Build: buildDeleteClusters, Typed: typedClusters},
		Action{ID: "delete-worker", Build: buildDeleteWorker, Cluster: formCluster, Typed: deleteWorkerTyped},
		Action{ID: "delete-realm", Build: buildDeleteRealm, Typed: func(f Form) string { return Realm(f) }},
	)
}

// deleteWorkerTyped is what the user types to delete workers: the name of
// the worker when one goes, else the name of the cluster.
func deleteWorkerTyped(f Form) string {
	if names := nodeNames(f); len(names) == 1 {
		return names[0]
	}
	return formCluster(f)
}

func buildCreateWorker(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	count, err := number(f, "count", "How many", 1, maxCount, -1)
	if err != nil {
		return Command{}, err
	}
	words := append([]string{"create", "worker"}, clusterWord(cl.Name)...)
	if count != 1 {
		words = append(words, "--count", fmt.Sprint(count))
	}
	nodes := c.Snap.ClusterNodes(realm, cl.Name)
	size := templateSize(c, nodes, c.Snap.Status(realm, cl.Name))
	if s := strings.TrimSpace(f.Get("cpus")); s != "" {
		cpus, err := number(f, "cpus", "CPUs", 1, maxCPUs, -1)
		if err != nil {
			return Command{}, err
		}
		words = append(words, "--cpus", fmt.Sprint(cpus))
		size.cpus = cpus
	}
	if m := strings.TrimSpace(f.Get("memory")); m != "" {
		if err := config.CheckMemory("memory", m); err != nil {
			return Command{}, fmt.Errorf("memory: %w", err)
		}
		words = append(words, "--memory", m)
		size.memoryMB, _ = config.MemoryMB(m) // checked
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
	cmd.Notes = append(cmd.Notes, strings.TrimSpace(size.why+" sind adds the workers, then tells slurmctld about them."))
	return cmd, nil
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
	nodes := c.Snap.ClusterNodes(realm, cl.Name)
	cmd.Goes = append(cmd.Goes,
		fmt.Sprintf("%d container%s: %s", len(nodes), plural(len(nodes)), strings.Join(shortNames(nodes), ", ")),
		"the network "+string(cluster.NetworkName(realm, cl.Name)),
	)
	// The volumes that sind delete cluster removes: those of its names,
	// not others that start alike, such as those of cluster dev-gpu.
	var volumes []string
	for _, t := range cluster.AllVolumeTypes {
		name := string(cluster.VolumeName(realm, cl.Name, t))
		if slices.ContainsFunc(c.Snap.Volumes[realm], func(v cluster.VolumeSummary) bool { return v.Name == name }) {
			volumes = append(volumes, name)
		}
	}
	if len(volumes) > 0 {
		cmd.Goes = append(cmd.Goes, "the volume"+plural(len(volumes))+" "+strings.Join(volumes, ", "))
	}
	cmd.Goes = append(cmd.Goes, "its jobs, its accounting and its Slurm state")
	if st := c.Snap.Status(realm, cl.Name); st != nil {
		for _, m := range st.Mounts {
			if m.Type == config.StorageHostPath {
				cmd.Stays = append(cmd.Stays, fmt.Sprintf("the host directory %s, mounted at %s", m.Source, m.Path))
			}
		}
	} else {
		cmd.Stays = append(cmd.Stays, "the host directories it mounts, if any")
	}
	switch others := len(c.Snap.Clusters[realm]) - 1; {
	case others == 1:
		cmd.Stays = append(cmd.Stays, "the other cluster of realm "+realm+", and its mesh")
	case others > 1:
		cmd.Stays = append(cmd.Stays, fmt.Sprintf("the other %d clusters of realm %s, and its mesh", others, realm))
	default:
		cmd.Goes = append(cmd.Goes, "the mesh, the DNS server and the SSH relay of realm "+realm+", as it is its last cluster")
	}
	return cmd, nil
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
	for i, p := range parts {
		if i > 0 && parts[i-1].Realm == p.Realm {
			continue
		}
		n := 0
		for _, q := range parts {
			if q.Realm == p.Realm {
				n++
			}
		}
		if n == len(c.Snap.Clusters[p.Realm]) {
			cmd.Notes = append(cmd.Notes, fmt.Sprintf("These are all the clusters of realm %s, so sind also removes its mesh, DNS server and SSH relay.", p.Realm))
		}
	}
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
	containers := make([]string, len(picked))
	for i, n := range picked {
		containers[i] = n.Container
	}
	if len(picked) == 1 {
		cmd.Goes = append(cmd.Goes, "the container "+containers[0]+" and its DNS record", "its Slurm node: jobs on it fail")
	} else {
		cmd.Goes = append(cmd.Goes, "the containers "+strings.Join(containers, ", ")+" and their DNS records", "their Slurm nodes: jobs on them fail")
	}
	left := 0
	for _, n := range c.Snap.ClusterNodes(realm, cl.Name) {
		if n.Role == config.RoleWorker {
			left++
		}
	}
	left -= len(picked)
	if left > 0 {
		cmd.Stays = append(cmd.Stays, fmt.Sprintf("%s with %s, its controller and its data", cl.Name, countOf(left, "worker")))
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
	var names, dirs []string
	unseen := false
	for _, cl := range c.Snap.Clusters[realm] {
		names = append(names, cl.Name)
		st := c.Snap.Status(realm, cl.Name)
		if st == nil {
			unseen = true
			continue
		}
		for _, m := range st.Mounts {
			if m.Type == config.StorageHostPath {
				dirs = append(dirs, fmt.Sprintf("the host directory %s, mounted at %s in %s", m.Source, m.Path, cl.Name))
			}
		}
	}
	if len(names) > 0 {
		cmd.Goes = append(cmd.Goes, countOf(len(names), "cluster")+": "+strings.Join(names, ", "))
	}
	cmd.Goes = append(cmd.Goes, "the mesh network, the DNS server and the SSH relay")
	if c.Snap.Monitorings[realm] != nil {
		cmd.Goes = append(cmd.Goes, "the monitoring stack")
	}
	cmd.Goes = append(cmd.Goes, "the exported SSH config")
	cmd.Stays = append(cmd.Stays, dirs...)
	if unseen {
		cmd.Stays = append(cmd.Stays, "the host directories that its clusters mount, if any")
	}
	// sind ui reads the containers on the mesh with the mesh; until it has,
	// it cannot say whether there are any.
	switch m, ok := c.Snap.Meshes[realm]; {
	case !ok || m.Err != "":
		cmd.Stays = append(cmd.Stays, "the containers that sind did not make, if any: sind detaches them")
	case len(m.External) > 0:
		cmd.Stays = append(cmd.Stays, "the containers that sind did not make, "+strings.Join(m.External, ", ")+": sind detaches them")
	}
	return cmd, nil
}
