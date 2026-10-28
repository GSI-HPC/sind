// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"cmp"
	"fmt"

	clidoctor "github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/doctor"
)

// Budget is what new nodes take of the Docker host, beside the nodes that
// run on it now: their memory limits, which Docker does not set aside but
// which jobs can fill, and their number.
type Budget struct {
	// HostMB is the memory of the Docker host, HostCPUs its CPUs.
	HostMB, HostCPUs int
	// NowMB is the memory limits of the nodes that run or are frozen
	// now, in every realm, of which there are NowNodes; Unknown of them
	// have a size that sind ui could not read, which NowMB leaves out.
	NowMB, NowNodes, Unknown int
	// AddMB is the memory limits of the new nodes, of which there are
	// AddNodes.
	AddMB, AddNodes int
	// Inotify says that the Doctor warns of the inotify limit of the host,
	// which the nodes then reach: its detail.
	Inotify string
}

// TotalMB is the memory limits of the nodes once the new ones run.
func (b Budget) TotalMB() int { return b.NowMB + b.AddMB }

// Nodes is the number of nodes once the new ones run.
func (b Budget) Nodes() int { return b.NowNodes + b.AddNodes }

// Over reports whether the memory limits come to more than the host has.
func (b Budget) Over() bool { return b.TotalMB() > b.HostMB }

// High reports whether the memory limits come to more than HostMemoryWarn
// percent of the host, but not more than it has.
func (b Budget) High() bool { return !b.Over() && b.TotalMB()*100 > b.HostMB*HostMemoryWarn }

// The bar of the budget is 1000 wide: the memory of the host, or, while
// the limits come to more, the limits, so that the host is a mark on it.
func (b Budget) scale() int { return max(b.HostMB, b.TotalMB(), 1) }

// NowW is the width of the nodes that run now on the bar of the budget.
func (b Budget) NowW() int { return b.NowMB * 1000 / b.scale() }

// AddW is the width of the new nodes on the bar of the budget.
func (b Budget) AddW() int { return b.AddMB * 1000 / b.scale() }

// HostX is where the memory of the host ends on the bar of the budget.
func (b Budget) HostX() int { return b.HostMB * 1000 / b.scale() }

// Room is how many more nodes of mb the host has memory for beside the
// nodes that run now.
func (b Budget) Room(mb int) int { return max(b.HostMB-b.NowMB, 0) / max(mb, 1) }

// hostBudget returns the budget of count new nodes of memoryMB and cpus
// each, nil while sind ui does not know the memory of the Docker host or
// that of the new nodes.
func hostBudget(c Context, count, memoryMB int) *Budget {
	host := int(c.Snap.Docker.MemTotal >> 20)
	if host <= 0 || memoryMB <= 0 {
		return nil
	}
	b := &Budget{HostMB: host, HostCPUs: c.Snap.Docker.NCPU, AddMB: count * memoryMB, AddNodes: count}
	var running []string
	for _, nodes := range c.Snap.Nodes {
		for _, n := range nodes {
			if n.State == cluster.StateRunning || n.State == cluster.StatePaused {
				running = append(running, n.Container)
			}
		}
	}
	b.NowNodes = len(running)
	defaultMB, _ := config.MemoryMB(config.DefaultMemory) // a constant
	if c.Sizes == nil {
		b.Unknown = len(running)
	} else {
		for _, f := range c.Sizes(running) {
			if f.Err != "" {
				b.Unknown++
				continue
			}
			b.NowMB += cmp.Or(int(f.Memory>>20), defaultMB)
		}
	}
	if c.Snap.Doctor != nil && b.Nodes() >= doctor.InotifyNodes {
		for _, ch := range c.Snap.Doctor.Checks {
			if ch.Name == "inotify" && ch.Status == clidoctor.StatusWarning {
				b.Inotify = ch.Detail
			}
		}
	}
	return b
}

// budgetProblems adds what the budget b of new workers of memoryMB and
// cpus each says to the command: a problem of the field "count", with the
// room the host has left, while the memory limits come to more than it
// has, until the form says to add them anyway, as in a new cluster, and
// then a warning; else a warning above HostMemoryWarn percent; and a
// warning of a worker with more CPUs than the host.
func budgetProblems(f Form, cmd *Command, b *Budget, memoryMB, cpus int, ps *problems) {
	if b == nil {
		return
	}
	cmd.Budget = b
	cmd.OverHost = b.Over()
	switch {
	case b.Over() && !checkbox(f, "overcommit", "Add anyway", ps):
		ps.add("count", "How many", false, fmt.Errorf("the host has room for about %s of %s. Add fewer, give them less memory, or tick Add anyway",
			countOf(b.Room(memoryMB), "more worker"), MemoryLabel(memoryMB)))
	case b.Over():
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("You add them anyway: the memory limits come to %s, more than the %s of the Docker host.",
			MemoryLabel(b.TotalMB()), MemoryLabel(b.HostMB)))
	case b.High():
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("The memory limits of the nodes then come to %s, %d%% of the %s of the Docker host, which then can run out of memory.",
			MemoryLabel(b.TotalMB()), b.TotalMB()*100/b.HostMB, MemoryLabel(b.HostMB)))
	}
	if b.HostCPUs > 0 && cpus > b.HostCPUs {
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("Each new worker asks for %s; the Docker host has %d. Slurm then offers CPUs that the host does not have.", countOf(cpus, "CPU"), b.HostCPUs))
	}
}
