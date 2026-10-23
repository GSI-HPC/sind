// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

func init() {
	register(
		Action{ID: "create-worker", Build: buildCreateWorker, Cluster: formCluster},
		Action{ID: "delete-cluster", Build: buildDeleteCluster, Cluster: formCluster, Typed: formCluster},
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
	if s := strings.TrimSpace(f.Get("cpus")); s != "" {
		cpus, err := number(f, "cpus", "CPUs", 1, maxCPUs, -1)
		if err != nil {
			return Command{}, err
		}
		words = append(words, "--cpus", fmt.Sprint(cpus))
	}
	if m := strings.TrimSpace(f.Get("memory")); m != "" {
		if err := config.CheckMemory("memory", m); err != nil {
			return Command{}, fmt.Errorf("memory: %w", err)
		}
		words = append(words, "--memory", m)
	}
	cmd := NewCommand(sindArgv(realm, words...)...)
	added := nextWorkers(c.Snap.ClusterNodes(realm, cl.Name), count)
	cmd.Notes = append(cmd.Notes, fmt.Sprintf("Adds %s and tells slurmctld about them.", strings.Join(nodeWords(cl.Name, added), " ")))
	return cmd, nil
}

// plural is the "s" of a count other than one.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// countWorkers names workers for a button: the one worker, or how many.
func countWorkers(shorts []string) string {
	if len(shorts) == 1 {
		return shorts[0]
	}
	return fmt.Sprintf("%d workers", len(shorts))
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
		if slices.ContainsFunc(c.Snap.VolumesOf(realm), func(v cluster.VolumeSummary) bool { return v.Name == name }) {
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
	switch others := len(c.Snap.ClustersOf(realm)) - 1; {
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
	cmd.OK = "Delete " + countWorkers(shorts)
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
	for _, cl := range c.Snap.ClustersOf(realm) {
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
	if c.Snap.MonitoringOf(realm) != nil {
		cmd.Goes = append(cmd.Goes, "the monitoring stack")
	}
	cmd.Goes = append(cmd.Goes, "the exported SSH config")
	cmd.Stays = append(cmd.Stays, dirs...)
	if unseen {
		cmd.Stays = append(cmd.Stays, "the host directories that its clusters mount, if any")
	}
	// sind ui reads the containers on the mesh with the mesh; until it has,
	// it cannot say whether there are any.
	switch m, ok := c.Snap.MeshOf(realm); {
	case !ok || m.Err != "":
		cmd.Stays = append(cmd.Stays, "the containers that sind did not make, if any: sind detaches them")
	case len(m.External) > 0:
		cmd.Stays = append(cmd.Stays, "the containers that sind did not make, "+strings.Join(m.External, ", ")+": sind detaches them")
	}
	return cmd, nil
}
