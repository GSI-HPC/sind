// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRestartService(t *testing.T) {
	s := testStore()
	stopped := node("dev", "worker-3", config.RoleWorker)
	stopped.State = cluster.StateStopped
	paused := node("dev", "worker-4", config.RoleWorker)
	paused.State = cluster.StatePaused
	s.SetNodes("sind", []cluster.NodeSummary{
		node("dev", "controller", config.RoleController), node("dev", "worker-0", config.RoleWorker),
		node("dev", "worker-1", config.RoleWorker), stopped, paused, node("dev", "worker-5", config.RoleWorker),
		node("default", "worker-0", config.RoleWorker),
	})
	health := func(slurmd bool) *cluster.NodeHealth {
		return &cluster.NodeHealth{State: cluster.StateRunning, Services: cluster.ServiceHealth{probe.ServiceSlurmd: slurmd, probe.ServiceMunge: true}}
	}
	noSSH := health(true)
	noSSH.Services[probe.ServiceSSHD] = false
	s.SetStatus("sind", "dev", &cluster.Status{Nodes: []*cluster.NodeStatus{
		nil, {Name: "controller.dev"}, {Name: "worker-0.dev", Health: health(false)}, {Name: "worker-3.dev"},
		{Name: "worker-5.dev", Health: noSSH},
	}})
	s.SetStatus("sind", "default", &cluster.Status{Nodes: []*cluster.NodeStatus{{Name: "worker-0", Health: health(true)}}})
	a, ok := Lookup("restart-service")
	require.True(t, ok)
	run := func(kv ...string) (Command, error) {
		t.Helper()
		return a.Build(form(kv...), s.ctx())
	}

	cmd, err := run("cluster", "dev", "nodes", "worker-0", "unit", " slurmd ")
	require.NoError(t, err)
	assert.Equal(t, []string{"sind", "ssh", "worker-0.dev", "--", "systemctl", "restart", "slurmd"}, cmd.Argv)
	assert.Equal(t, "sind ssh worker-0.dev -- systemctl restart slurmd", cmd.Display)
	assert.Equal(t, "Restart slurmd", cmd.OK)
	assert.Contains(t, cmd.Notes[0], "The other processes of worker-0 go on.")
	assert.Empty(t, cmd.Warns)
	assert.Equal(t, "dev", a.Cluster(form("cluster", "dev")))

	cmd, err = run("cluster", "default", "nodes", "worker-0", "unit", "munge")
	require.NoError(t, err)
	assert.Equal(t, "sind ssh worker-0 -- systemctl restart munge", cmd.Display)
	assert.Equal(t, []string{"munge is active: it stops for a moment."}, cmd.Warns)

	_, err = run("cluster", "dev", "nodes", "worker-0")
	var missing *Missing
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "Pick the service to restart.", missing.Hint())
	_, err = run("cluster", "dev", "unit", "slurmd")
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "Pick a node of dev.", missing.Hint(), "the field takes one node")
	for _, tc := range []struct {
		kv   []string
		want string
	}{
		{[]string{"cluster", "nowhere"}, "realm sind has no cluster nowhere"},
		{[]string{"cluster", "dev", "nodes", "nobody"}, "nodes: cluster dev has no node nobody"},
		{[]string{"cluster", "dev", "nodes", "worker-0,worker-1", "unit", "slurmd"}, "nodes: pick one node"},
		{[]string{"cluster", "dev", "nodes", "worker-3", "unit", "slurmd"}, "nodes: worker-3 is stopped: start it, and systemd starts its services"},
		{[]string{"cluster", "dev", "nodes", "worker-4", "unit", "slurmd"}, "nodes: worker-4 is frozen: unfreeze it, and its services run on"},
		{[]string{"cluster", "dev", "nodes", "worker-5", "unit", "slurmd"}, "nodes: sshd is down on worker-5, and sind ssh needs it: reboot worker-5"},
		{[]string{"cluster", "dev", "nodes", "worker-1", "unit", "slurmd"}, "nodes: sind ui has not probed worker-1 yet"},
		{[]string{"cluster", "dev", "nodes", "controller", "unit", "slurmd"}, "nodes: sind ui has not probed controller yet"},
		{[]string{"cluster", "dev", "nodes", "worker-0", "unit", "sshd; reboot"}, "unit: sind checks no service sshd; reboot on worker-0; it checks munge, slurmd"},
	} {
		_, err := run(tc.kv...)
		assert.EqualError(t, err, tc.want, "%v", tc.kv)
	}
	assert.Nil(t, NodeServices(nil, "worker-0", "dev"))
}
