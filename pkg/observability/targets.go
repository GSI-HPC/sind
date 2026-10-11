// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"cmp"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// Target is a monitored node: a node container of a monitored cluster,
// which mounts its directory of the realm's journal volume.
type Target struct {
	Cluster     string
	Node        string // the node's hostname, e.g. worker-0
	Role        config.Role
	Managed     bool // sind manages Slurm on the node (the sind.managed label)
	Container   docker.ContainerName
	ContainerID docker.ContainerID // the full ID, which names the node's cgroup
}

// Check is a health probe of a Slurm daemon on a node.
type Check struct {
	Service string // slurmctld, slurmd or slurmdbd
	Check   string // live, ready or tcp
	Module  string // the blackbox module: http or tcp
	Port    string
	Path    string // the HTTP path, empty for tcp
}

// Slurm daemons' ports.
const (
	slurmctldPort = "6817"
	slurmdPort    = "6818"
	slurmdbdPort  = "6819"
)

// Collection is what the stack collects from a node.
type Collection struct {
	// Journal and Cgroups are collected from every monitored node: its
	// journal and its container's and units' resource use.
	Journal, Cgroups bool
	// SlurmMetrics scrapes slurmctld's /metrics endpoints.
	SlurmMetrics bool
	// Checks are the node's Slurm daemon probes.
	Checks []Check
}

// Collect returns what the stack collects from a node of a role, managed or
// not: the journal and the cgroups of every node, slurmctld's metrics and
// probes on managed controllers, slurmd's on managed workers and slurmdbd's
// on a managed db node. Unmanaged nodes run no Slurm of sind's, so there
// are no Slurm probes that would only show sind's daemons as down.
// slurmdbd serves no HTTP, so its probe is a TCP connect; sackd and
// slurmrestd get none.
func Collect(role config.Role, managed bool) Collection {
	c := Collection{Journal: true, Cgroups: true}
	if !managed {
		return c
	}
	switch role {
	case config.RoleController:
		c.SlurmMetrics = true
		c.Checks = []Check{
			{Service: "slurmctld", Check: "live", Module: "http", Port: slurmctldPort, Path: "/livez"},
			{Service: "slurmctld", Check: "ready", Module: "http", Port: slurmctldPort, Path: "/readyz"},
		}
	case config.RoleWorker:
		c.Checks = []Check{{Service: "slurmd", Check: "ready", Module: "http", Port: slurmdPort, Path: "/readyz"}}
	case config.RoleDB:
		c.Checks = []Check{{Service: "slurmdbd", Check: "tcp", Module: "tcp", Port: slurmdbdPort}}
	}
	return c
}

// sortTargets sorts targets by cluster and node, so that the generated
// configuration does not depend on the order docker lists them in.
func sortTargets(targets []Target) []Target {
	sorted := slices.Clone(targets)
	slices.SortFunc(sorted, func(a, b Target) int {
		return cmp.Or(cmp.Compare(a.Cluster, b.Cluster), cmp.Compare(a.Node, b.Node))
	})
	return sorted
}

// componentName returns the Alloy component label of a node: "n_", the
// cluster, "__" and the node, with "-" turned into "_", as Alloy labels
// must be identifiers and cluster names may start with a digit. Cluster
// and node names are DNS labels without "_", and node names have no "--",
// so the name is unique. It must stay the same across sind versions: Alloy
// keys a journal's read position by it.
func componentName(cluster, node string) string {
	return "n_" + strings.ReplaceAll(cluster, "-", "_") + "__" + strings.ReplaceAll(node, "-", "_")
}

// CgroupPath returns the cgroup of a container under a cgroup driver:
// /system.slice/docker-<id>.scope with systemd, /docker/<id> with
// cgroupfs.
func CgroupPath(driver string, id docker.ContainerID) string {
	if driver == "cgroupfs" {
		return "/docker/" + string(id)
	}
	return "/system.slice/docker-" + string(id) + ".scope"
}
