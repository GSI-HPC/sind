// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"testing"

	clidoctor "github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// budgetCtx is the context of a host of hostMB and cpus whose nodes each
// have a memory limit of 1 GiB, but sind-dev-worker-0, which sind ui
// cannot look at, and sind-dev-worker-1, which has none.
func budgetCtx(st *testStoreT, hostMB int64, cpus int) Context {
	st.SetDocker(store.DockerState{Checked: true, MemTotal: hostMB << 20, NCPU: cpus})
	c := st.ctx()
	c.Worker = func(string) WorkerFacts { return WorkerFacts{CPUs: 1, Memory: 1 << 30} }
	c.Sizes = func(containers []string) []WorkerFacts {
		out := make([]WorkerFacts, len(containers))
		for i, name := range containers {
			switch name {
			case "sind-dev-worker-0":
				out[i] = WorkerFacts{Err: "docker: gone"}
			case "sind-dev-worker-1":
				out[i] = WorkerFacts{}
			default:
				out[i] = WorkerFacts{CPUs: 1, Memory: 1 << 30}
			}
		}
		return out
	}
	return c
}

func TestHostBudget(t *testing.T) {
	st := testStore()
	c := budgetCtx(st, 16<<10, 8)
	b := hostBudget(c, 2, 2048, 2)
	require.NotNil(t, b)
	// The 7 nodes of realm sind run; worker-0 is unknown, worker-1 has
	// sind's defaults of 512 MiB and 1 CPU, the others 1 GiB and 1 CPU.
	assert.Equal(t, Budget{HostMB: 16 << 10, HostCPUs: 8, NowMB: 5*1024 + 512, NowCPUs: 6, NowNodes: 7, Unknown: 1, AddMB: 2048, AddCPUs: 2, AddNodes: 2}, *b)
	assert.Equal(t, 5*1024+512+2048, b.TotalMB())
	assert.Equal(t, 8, b.TotalCPUs())
	assert.Equal(t, 9, b.Nodes())
	assert.False(t, b.Over())
	assert.False(t, b.High())
	assert.Equal(t, 10, b.Room(1024))
	assert.Equal(t, 0, Budget{HostMB: 1, NowMB: 2}.Room(0))

	// The bar is 1000 wide: the host, or the limits once they come to
	// more, so that the host is a mark on it.
	assert.Equal(t, []int{250, 125, 1000}, []int{Budget{HostMB: 4096, NowMB: 1024, AddMB: 512}.NowW(), Budget{HostMB: 4096, NowMB: 1024, AddMB: 512}.AddW(), Budget{HostMB: 4096}.HostX()})
	over := Budget{HostMB: 1000, NowMB: 1000, AddMB: 1000}
	assert.Equal(t, []int{500, 500, 500}, []int{over.NowW(), over.AddW(), over.HostX()})
	assert.Equal(t, 0, Budget{}.HostX())

	c.Sizes = nil
	b = hostBudget(c, 1, 1024, 1)
	assert.Equal(t, 7, b.Unknown)
	assert.Zero(t, b.NowMB)

	assert.Nil(t, hostBudget(c, 1, 0, 1), "the size of the new workers is unknown")
	st.SetDocker(store.DockerState{Checked: true})
	assert.Nil(t, hostBudget(st.ctx(), 1, 1024, 1), "the memory of the host is unknown")
}

func TestHostBudget_Limits(t *testing.T) {
	st := testStore()
	st.SetDoctor(&store.DoctorReport{Checks: []store.DoctorCheck{
		{Name: "cgroup", Status: clidoctor.StatusWarning},
		{Name: "inotify", Status: clidoctor.StatusWarning, Detail: "max_user_instances 128", Remediation: "sudo sysctl x=1"},
	}})
	c := budgetCtx(st, 64<<10, 8)
	inotify := HostWarn{Check: "inotify", Detail: "max_user_instances 128", Remediation: "sudo sysctl x=1", Nodes: 10}
	b := hostBudget(c, 2, 2048, 2)
	assert.Equal(t, []HostWarn{inotify}, b.Limits, "only the checks that hold from a number of nodes on")
	assert.Empty(t, b.Crossed(), "9 nodes")
	assert.Equal(t, []HostWarn{inotify}, hostBudget(c, 3, 1024, 3).Crossed())

	st.SetDoctor(&store.DoctorReport{Checks: []store.DoctorCheck{{Name: "inotify", Status: clidoctor.StatusOK}}})
	assert.Empty(t, hostBudget(budgetCtx(st, 64<<10, 8), 3, 1024, 3).Limits)
	st.SetDoctor(nil)
	assert.Empty(t, hostBudget(budgetCtx(st, 64<<10, 8), 30, 1024, 3).Limits, "no report of the Doctor yet")
}

func TestBuildCreateWorker_Budget(t *testing.T) {
	st := testStore()
	a, _ := Lookup("create-worker")

	// 5.5 GiB run now; 3 more of 1 GiB come to 8.5 GiB of 10 GiB.
	c := budgetCtx(st, 10<<10, 2)
	cmd, err := a.Build(form("cluster", "dev", "count", "3"), c)
	require.NoError(t, err)
	require.NotNil(t, cmd.Budget)
	assert.False(t, cmd.OverHost)
	assert.Empty(t, cmd.Problems)
	assert.Equal(t, []string{"The memory limits of the nodes then come to 8.5 GiB, 85% of the 10 GiB of the Docker host, which then can run out of memory."}, cmd.Warns)

	// 5 more come to 10.5 GiB.
	cmd, err = a.Build(form("cluster", "dev", "count", "5", "cpus", "4"), c)
	require.NoError(t, err)
	assert.True(t, cmd.OverHost)
	assert.Equal(t, []Problem{{Field: "count", Label: "How many",
		Text: "the host has room for about 4 more workers of 1 GiB. Add fewer, give them less memory, or tick Add anyway"}}, cmd.Problems)
	assert.Equal(t, []string{"Each new worker asks for 4 CPUs; the Docker host has 2. Slurm then offers CPUs that the host does not have."}, cmd.Warns)

	cmd, err = a.Build(form("cluster", "dev", "count", "5", "overcommit", "on"), c)
	require.NoError(t, err)
	assert.True(t, cmd.OverHost)
	assert.Empty(t, cmd.Problems)
	assert.Equal(t, []string{"You add them anyway: the memory limits come to 10.5 GiB, more than the 10 GiB of the Docker host."}, cmd.Warns)

	cmd, err = a.Build(form("cluster", "dev", "count", "5", "overcommit", "maybe"), c)
	require.NoError(t, err)
	assert.Equal(t, []Problem{{Field: "overcommit", Label: "Add anyway", Text: "tick the box or leave it empty", Default: true}}, cmd.Problems[:1])
	assert.Len(t, cmd.Problems, 2)

	// sind ui does not know the size of the new workers.
	c.Worker = nil
	cmd, err = a.Build(form("cluster", "dev", "count", "50"), c)
	require.NoError(t, err)
	assert.Nil(t, cmd.Budget)
}

func TestWorkerSlurmNote(t *testing.T) {
	assert.Contains(t, workerSlurmNote(nil), "join the partition `all`")
	assert.Contains(t, workerSlurmNote(&cluster.Status{Nodes: []*cluster.NodeStatus{{Role: config.RoleWorker}, {Role: config.RoleController, Managed: true}}}), "join the partition `all`")
	assert.Equal(t, "The cluster is unmanaged, so the new workers are too: sind writes no Slurm config for them and starts no slurmd.",
		workerSlurmNote(&cluster.Status{Nodes: []*cluster.NodeStatus{{Role: config.RoleController}}}))
}
