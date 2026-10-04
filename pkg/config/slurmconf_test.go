// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLineParameter(t *testing.T) {
	for _, tt := range []struct {
		line, key, want string
		found           bool
	}{
		{"TaskPlugin=task/cgroup\n", "TaskPlugin", "task/cgroup", true},
		{"taskplugin=task/cgroup", "TaskPlugin", "task/cgroup", true},
		{"  TaskPlugin = task/affinity  ", "TaskPlugin", "task/affinity", true},
		{"TaskPlugin=\"task/cgroup,task/affinity\"", "TaskPlugin", "task/cgroup,task/affinity", true},
		{"SlurmctldParameters+=enable_configless", "SlurmctldParameters", "enable_configless", true},
		{"MpiDefault=none TaskPlugin=task/affinity", "TaskPlugin", "task/affinity", true},
		{"TaskPlugin=a TaskPlugin=b", "TaskPlugin", "b", true},
		{"# TaskPlugin=task/affinity", "TaskPlugin", "", false},
		{"MpiDefault=none # TaskPlugin=task/affinity", "TaskPlugin", "", false},
		{"TaskPluginParam=Verbose", "TaskPlugin", "", false},
		{"include /etc/slurm/x.conf", "TaskPlugin", "", false},
		{"", "TaskPlugin", "", false},
		// The pairs after NodeName or PartitionName belong to the node or
		// partition.
		{"PartitionName=p Nodes=ALL DefMemPerCPU=100", "DefMemPerCPU", "", false},
		{"nodename=n1 RealMemory=100", "RealMemory", "", false},
		{"DefMemPerCPU=200 PartitionName=p DefMemPerCPU=100", "DefMemPerCPU", "200", true},
		{"PartitionName=p Default=YES", "PartitionName", "p", true},
	} {
		got, found := LineParameter(tt.line, tt.key)
		assert.Equal(t, tt.want, got, tt.line)
		assert.Equal(t, tt.found, found, tt.line)
	}
}

func TestLinePairs(t *testing.T) {
	assert.Nil(t, LinePairs("# PartitionName=p\n"))
	assert.Equal(t, [][2]string{{"PartitionName", "p"}, {"Nodes", "worker-[0-1]"}, {"Default", "YES"}, {"AllowGroups", "a b"}},
		LinePairs(`PartitionName=p Nodes=worker-[0-1]  Default = YES AllowGroups="a b" # MaxTime=1`))
}

func TestSection_Lines(t *testing.T) {
	s := Section{Fragments: map[string]string{"b": "B=1\nB=2\n", "a": "A=1"}}
	var lines []string
	for line := range s.Lines() {
		lines = append(lines, line)
		if line == "B=1\n" {
			break
		}
	}
	assert.Equal(t, []string{"A=1", "B=1\n"}, lines)
}

func TestSection_Parameter(t *testing.T) {
	_, ok := Section{}.Parameter("TaskPlugin")
	assert.False(t, ok)

	s := Section{Content: "TaskPlugin=task/affinity\nMpiDefault=none\nTaskPlugin = task/cgroup\n"}
	v, ok := s.Parameter("taskplugin")
	assert.True(t, ok)
	assert.Equal(t, "task/cgroup", v)

	// Fragments are included in name order; the last assignment wins.
	frag := Section{Fragments: map[string]string{
		"b": "TaskPlugin=task/cgroup,task/affinity\n",
		"a": "TaskPlugin=task/cgroup\n",
		"c": "MpiDefault=none\n",
	}}
	v, ok = frag.Parameter("TaskPlugin")
	assert.True(t, ok)
	assert.Equal(t, "task/cgroup,task/affinity", v)
	assert.True(t, frag.SetsParameter("mpidefault"))
	assert.False(t, frag.SetsParameter("ReturnToService"))
}

func TestListsValue(t *testing.T) {
	assert.True(t, ListsValue("enable_nss_slurm", "enable_nss_slurm"))
	assert.True(t, ListsValue("use_interactive_step, enable_nss_slurm", "enable_nss_slurm"))
	assert.True(t, ListsValue("auth/slurm", "auth/slurm"))
	assert.False(t, ListsValue("enable_nss_slurm_x", "enable_nss_slurm"))
	assert.False(t, ListsValue("ENABLE_NSS_SLURM", "enable_nss_slurm"))
	assert.False(t, ListsValue("", "enable_nss_slurm"))
}
