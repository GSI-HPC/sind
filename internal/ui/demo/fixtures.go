// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"fmt"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/probe"
)

// Home is the home directory of the demo's user.
const Home = "/home/demo"

// HostMemory is the memory of the demo's Docker host, in bytes.
const HostMemory = 32 << 30

// ConfigDir is where the demo's dialogs say they save the configs. Nothing
// is written there: the demo keeps them in memory.
const ConfigDir = Home + "/.local/state/sind/ui/configs"

// roleCount is a role of a seeded cluster and how many nodes have it.
type roleCount struct {
	role  config.Role
	count int
}

// seed describes a cluster of the demo's start.
type seed struct {
	realm, name string
	slurm       string
	monitoring  bool
	volume      bool
	data        string
	users       []string
	roles       []roleCount
	// cpus and memory are the size of its nodes, 0 for sind's defaults.
	cpus   int
	memory int64
}

// seeds are the clusters the demo starts with: two realms, four clusters of
// different shapes. dev is the big one, with a pair of controllers, every
// optional role, monitoring and users; default is the small one on a host
// directory; legacy runs the older Slurm and has a stopped worker; ci is the
// realm of a CI job.
var seeds = []seed{
	{realm: "sind", name: "default", slurm: slurmNew, data: Home + "/sind-demo",
		roles: []roleCount{{config.RoleController, 1}, {config.RoleWorker, 1}}},
	{realm: "sind", name: "dev", slurm: slurmNew, monitoring: true, volume: true, data: "sind-dev-data", users: []string{"alice", "bob"}, cpus: 2, memory: 1 << 30,
		roles: []roleCount{{config.RoleController, 2}, {config.RoleDB, 1}, {config.RoleAPI, 1}, {config.RoleSubmitter, 1}, {config.RoleWorker, 6}}},
	{realm: "sind", name: "legacy", slurm: slurmOld, data: Home + "/legacy",
		roles: []roleCount{{config.RoleController, 1}, {config.RoleWorker, 2}}},
	{realm: "ci", name: "pr-145", slurm: slurmNew, volume: true, data: "ci-pr-145-data",
		roles: []roleCount{{config.RoleController, 1}, {config.RoleWorker, 2}}},
}

// Seeded returns the world the demo starts with, and a few things in it that
// need the user: a worker whose slurmd is down, a stopped worker, a
// connected container.
func Seeded() *World {
	w := NewWorld()
	for _, s := range seeds {
		c := w.clusterOf(s.realm, s.name)
		c.slurm, c.monitoring, c.volume, c.dataPath, c.users = s.slurm, s.monitoring, s.volume, s.data, s.users
		c.cpus, c.memory = s.cpus, s.memory
		for _, rc := range s.roles {
			for i := range rc.count {
				short := string(rc.role)
				switch {
				case rc.role == config.RoleController && i == 1:
					short = cluster.ControllerBackupShortName
				case rc.role == config.RoleWorker:
					short = fmt.Sprintf("worker-%d", i)
				}
				ns := &nodeState{short: short, role: rc.role, ip: fmt.Sprintf("172.%d.0.%d", c.net, c.nextHost), state: cluster.StateRunning, up: map[probe.Service]bool{}}
				for _, svc := range services(rc.role) {
					ns.up[svc] = true
				}
				c.nodes = append(c.nodes, ns)
				c.nextHost++
			}
		}
	}
	sind := w.realmOf("sind")
	sind.monitoring = true
	sind.external = []string{"kind-control-plane"}
	// slurmd of one worker of dev is down: the page shows a failed service.
	// Slurm runs jobs on two others and has drained one.
	dev := w.lookup("sind", "dev")
	dev.find("worker-4").up[probe.ServiceSlurmd] = false
	dev.find("worker-0").busy = 2
	dev.find("worker-1").busy = 1
	dev.find("worker-5").drain = "memory errors, admin 09:02"
	// A worker of legacy is stopped: the cluster is mixed.
	legacy := w.lookup("sind", "legacy").find("worker-1")
	legacy.state, legacy.up = cluster.StateStopped, map[probe.Service]bool{}
	return w
}

// Checks returns the results of the host checks: all fine but one warning.
func Checks() []store.DoctorCheck {
	return []store.DoctorCheck{
		{Name: "Docker Engine", Status: "ok", Detail: "Docker Engine 28.5.1"},
		{Name: "Docker daemon", Status: "ok", Detail: "rootful, no userns-remap"},
		{Name: "Docker host", Status: "ok", Detail: "local daemon at unix:///var/run/docker.sock"},
		{Name: "cgroup v2", Status: "ok", Detail: "cgroup2 with nsdelegate"},
		{Name: "inotify", Status: "warning", Detail: "fs.inotify.max_user_instances is 128; 9 nodes and more can run out",
			Remediation: "Raise the limit now:\n\n  sudo sysctl fs.inotify.max_user_instances=1024"},
		{Name: "DNS policy", Status: "ok", Detail: "systemd-resolved forwards *.sind to sind-dns"},
	}
}

// doctorAt returns the report of the host checks that ended at t.
func doctorAt(t time.Time) *store.DoctorReport {
	return &store.DoctorReport{Time: t, Checks: Checks()}
}
