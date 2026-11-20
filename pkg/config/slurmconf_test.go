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

	// A backslash escapes the next character, so \# starts no comment.
	for line, want := range map[string]string{
		`StoragePass=a\#b # c`: "a#b",
		`StoragePass="a\#b c"`: "a#b c",
		`StoragePass=a\\#b`:    `a\`,
		`StoragePass=ab\`:      "ab",
		`StoragePass="a#b c"`:  `"a`, // as in Slurm, the comment goes first
		`StoragePass=a\b\\c`:   `ab\c`,
	} {
		assert.Equal(t, [][2]string{{"StoragePass", want}}, LinePairs(line), line)
	}
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

func TestListsFlag(t *testing.T) {
	assert.True(t, ListsFlag("use_interactive_step, ENABLE_NSS_SLURM", "enable_nss_slurm"))
	assert.False(t, ListsFlag("enable_nss_slurm_x", "enable_nss_slurm"))
}

func TestIsPlugin(t *testing.T) {
	assert.True(t, IsPlugin("auth/slurm", "auth/slurm"))
	assert.True(t, IsPlugin(" slurm ", "auth/slurm"))
	assert.False(t, IsPlugin("cred/slurm", "auth/slurm"))
	assert.False(t, IsPlugin("Auth/Slurm", "auth/slurm"))
	assert.False(t, IsPlugin("", "auth/slurm"))
	assert.True(t, IsPlugin("slurm", "slurm"))
	assert.False(t, IsPlugin("", "slurm"))
	assert.False(t, ListsPlugin("a,,b", "slurm"))
}

func TestListsPlugin(t *testing.T) {
	assert.True(t, ListsPlugin("auth/jwt", "auth/jwt"))
	assert.True(t, ListsPlugin("auth/munge, jwt", "auth/jwt"))
	assert.False(t, ListsPlugin("auth/jwtx", "auth/jwt"))
	assert.False(t, ListsPlugin("", "auth/jwt"))
}

func TestListsValue(t *testing.T) {
	assert.True(t, ListsValue("enable_nss_slurm", "enable_nss_slurm"))
	assert.True(t, ListsValue("use_interactive_step, enable_nss_slurm", "enable_nss_slurm"))
	assert.True(t, ListsValue("auth/slurm", "auth/slurm"))
	assert.False(t, ListsValue("enable_nss_slurm_x", "enable_nss_slurm"))
	assert.False(t, ListsValue("ENABLE_NSS_SLURM", "enable_nss_slurm"))
	assert.False(t, ListsValue("", "enable_nss_slurm"))
}

func TestFixedSlurmParameters(t *testing.T) {
	fixed := FixedSlurmParameters()
	assert.Contains(t, fixed, "ClusterName")
	fixed[0] = "changed"
	assert.Equal(t, "ClusterName", FixedSlurmParameters()[0], "a caller gets a copy")
}

func TestCluster_SlurmWarnings(t *testing.T) {
	cluster := func(main string, backup bool, managed *bool) Cluster {
		return Cluster{
			Nodes: []Node{{Role: RoleController, BackupController: backup, Managed: managed}},
			Slurm: Slurm{Main: Section{Content: main}},
		}
	}
	c := cluster("SelectType=select/cons_tres\n# a comment\n\ninclude /etc/slurm/more.conf\n", false, nil)
	assert.Empty(t, c.Warnings())
	c = cluster("clustername=prod\nSlurmUser=root\n", false, nil)
	assert.Equal(t, []string{"The lines of slurm.conf set ClusterName, SlurmUser, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start."},
		c.Warnings())
	c = cluster("SlurmctldHost=x\nStateSaveLocation=/s\nSlurmdSpoolDir=/d\n", true, nil)
	assert.Equal(t, []string{"The lines of slurm.conf set SlurmdSpoolDir, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start."},
		c.Warnings(), "the config refuses the others with a backup controller")
	off := false
	c = cluster("ClusterName=prod\n", false, &off)
	assert.Empty(t, c.Warnings(), "the config refuses Slurm config of an unmanaged cluster")

	c = cluster("SelectType select/cons_tres # no =\n", false, nil)
	c.Slurm.Plugstack = Section{Content: "optional /usr/lib64/slurm/spank_demo.so\n"}
	c.Slurm.Cgroup = Section{Fragments: map[string]string{"ram": "ConstrainRAMSpace=yes\nConstrainCores\n"}}
	assert.Equal(t, []string{
		`The lines of slurm.conf have "SelectType select/cons_tres", which is no Key=Value pair. Slurm may refuse it and not start.`,
		`The lines of cgroup.conf have "ConstrainCores", which is no Key=Value pair. Slurm may refuse it and not start.`,
	}, c.Warnings())
}
