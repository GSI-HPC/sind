// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
)

func init() {
	register(
		Action{ID: "create-worker", Build: buildCreateWorker, Cluster: formCluster},
		Action{ID: "delete-cluster", Build: buildDeleteCluster, Cluster: formCluster, Typed: formCluster},
		Action{ID: "delete-worker", Build: buildDeleteWorker, Cluster: formCluster, Typed: formCluster},
		Action{ID: "delete-realm", Build: buildDeleteRealm, Typed: func(f Form) string { return Realm(f) }},
	)
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

func buildDeleteCluster(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	cmd := NewCommand(sindArgv(realm, append([]string{"delete", "cluster"}, clusterWord(cl.Name)...)...)...)
	if len(c.Snap.ClustersOf(realm)) == 1 {
		cmd.Notes = append(cmd.Notes, "It is the last cluster of the realm, so sind also removes the mesh, the DNS server and the SSH relay.")
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
	shorts := make([]string, len(picked))
	for i, n := range picked {
		shorts[i] = ShortName(n)
	}
	return NewCommand(sindArgv(realm, append([]string{"delete", "worker"}, nodeWords(cl.Name, shorts)...)...)...), nil
}

func buildDeleteRealm(f Form, c Context) (Command, error) {
	realm, err := checkRealm(f)
	if err != nil {
		return Command{}, err
	}
	r, ok := c.Snap.Realm(realm)
	if !ok {
		return Command{}, fmt.Errorf("realm: sind ui sees no realm %s", realm)
	}
	cmd := NewCommand(sindArgv(realm, "delete", "realm")...)
	cmd.Notes = append(cmd.Notes, fmt.Sprintf("Deletes all %d clusters of realm %s, its mesh, DNS server, SSH relay, monitoring stack if it has one, and the exported SSH config. Connected containers stay; sind only detaches them.", r.Clusters, realm))
	return cmd, nil
}
