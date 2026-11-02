// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"cmp"
	"errors"
	"fmt"

	clidoctor "github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/doctor"
)

// Budget is what new nodes take of the Docker host, beside the nodes that
// run on it now: their memory limits, which Docker does not set aside but
// which jobs can fill, their CPU limits, which they share, and their
// number.
type Budget struct {
	// HostMB is the memory of the Docker host, HostCPUs its CPUs.
	HostMB, HostCPUs int
	// NowMB and NowCPUs are the memory and CPU limits of the nodes that
	// run or are frozen now, in every realm, of which there are NowNodes;
	// Unknown of them have a size that sind ui could not read, which NowMB
	// and NowCPUs leave out.
	NowMB, NowCPUs, NowNodes, Unknown int
	// AddMB and AddCPUs are the memory and CPU limits of the new nodes,
	// of which there are AddNodes.
	AddMB, AddCPUs, AddNodes int
	// Limits are the warnings of the Doctor that hold from a number of
	// nodes on the host on, which Crossed are of them.
	Limits []HostWarn
}

// HostWarn is a warning of the Doctor that holds from a number of nodes on
// the host on, such as that of its inotify limit.
type HostWarn struct {
	// Check is the name of the check, Detail and Remediation are what it
	// says.
	Check, Detail, Remediation string
	// Nodes is the number of nodes from which the warning holds.
	Nodes int
}

// doctorNodeLimits are the checks of the Doctor whose warning holds from a
// number of nodes on the host on.
var doctorNodeLimits = []struct {
	check string
	nodes int
}{{"inotify", doctor.InotifyNodes}}

// TotalMB is the memory limits of the nodes once the new ones run.
func (b Budget) TotalMB() int { return b.NowMB + b.AddMB }

// Nodes is the number of nodes once the new ones run.
func (b Budget) Nodes() int { return b.NowNodes + b.AddNodes }

// TotalCPUs is the CPU limits of the nodes once the new ones run.
func (b Budget) TotalCPUs() int { return b.NowCPUs + b.AddCPUs }

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

// hostBudget returns the budget of count new nodes whose memory and CPU
// limits come to memoryMB and cpus, nil while sind ui does not know the
// memory of the Docker host or that of the new nodes.
func hostBudget(c Context, count, memoryMB, cpus int) *Budget {
	host := int(c.Snap.Docker.MemTotal >> 20)
	if host <= 0 || memoryMB <= 0 {
		return nil
	}
	b := &Budget{HostMB: host, HostCPUs: c.Snap.Docker.NCPU, AddMB: memoryMB, AddCPUs: cpus, AddNodes: count}
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
			b.NowCPUs += cmp.Or(f.CPUs, config.DefaultCPUs)
		}
	}
	if c.Snap.Doctor == nil {
		return b
	}
	for _, l := range doctorNodeLimits {
		for _, ch := range c.Snap.Doctor.Checks {
			if ch.Name == l.check && ch.Status == clidoctor.StatusWarning {
				b.Limits = append(b.Limits, HostWarn{Check: ch.Name, Detail: ch.Detail, Remediation: ch.Remediation, Nodes: l.nodes})
			}
		}
	}
	return b
}

// Crossed are the warnings of Limits that the nodes on the host reach
// once the new ones run.
func (b Budget) Crossed() []HostWarn {
	var out []HostWarn
	for _, w := range b.Limits {
		if b.Nodes() >= w.Nodes {
			out = append(out, w)
		}
	}
	return out
}

// budgetOver says what a form whose nodes come to more memory than the
// host has must do: the box that accepts it, the label of that box and
// the problem until it is ticked, in the field of field and label, and
// the warning once it is.
type budgetOver struct {
	box, field, label string
	problem           func(b *Budget) string
	anyway            string
}

// budgetProblems adds what the budget b says to the command: a problem
// while the memory limits come to more than the host has, until the form
// ticks the box of over, and then a warning; else a warning above
// HostMemoryWarn percent.
func budgetProblems(f Form, cmd *Command, b *Budget, over budgetOver, ps *problems) {
	if b == nil {
		return
	}
	cmd.Budget = b
	cmd.OverHost = b.Over()
	switch {
	case b.Over() && !checkbox(f, "overcommit", over.box, ps):
		ps.add(over.field, over.label, false, errors.New(over.problem(b)))
	case b.Over():
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("%s: the memory limits come to %s, more than the %s of the Docker host.",
			over.anyway, MemoryLabel(b.TotalMB()), MemoryLabel(b.HostMB)))
	case b.High():
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("The memory limits of the nodes then come to %s, %d%% of the %s of the Docker host, which then can run out of memory.",
			MemoryLabel(b.TotalMB()), b.TotalMB()*100/b.HostMB, MemoryLabel(b.HostMB)))
	}
}

// workersOver is what Add workers says of new workers of memoryMB each
// that do not fit the host: How many has the room the host has left.
func workersOver(memoryMB int) budgetOver {
	return budgetOver{box: "Add anyway", field: "count", label: "How many", anyway: "You add them anyway", problem: func(b *Budget) string {
		return fmt.Sprintf("the host has room for about %s of %s. Add fewer, give them less memory, or tick Add anyway",
			countOf(b.Room(memoryMB), "more worker"), MemoryLabel(memoryMB))
	}}
}

// clusterOver is what New cluster says of nodes that do not fit the host.
var clusterOver = budgetOver{box: "Create anyway", field: "overcommit", label: "Memory", anyway: "You create it anyway", problem: func(b *Budget) string {
	return fmt.Sprintf("with the nodes that run now, the memory limits come to %s, more than the %s of the Docker host; give the nodes less memory, or tick Create anyway",
		MemoryLabel(b.TotalMB()), MemoryLabel(b.HostMB))
}}

// cpuWarning warns of a new node, of who, with more CPUs than the host of
// b has: Slurm then offers CPUs that the host does not have.
func cpuWarning(cmd *Command, b *Budget, who string, cpus int) {
	if b != nil && b.HostCPUs > 0 && cpus > b.HostCPUs {
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("%s asks for %s; the Docker host has %d. Slurm then offers CPUs that the host does not have.", who, countOf(cpus, "CPU"), b.HostCPUs))
	}
}
