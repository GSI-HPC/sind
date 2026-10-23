// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageError(t *testing.T) {
	digest := "sha256:" + strings.Repeat("0f", 32)
	for _, s := range []string{
		"sind-node", "sind-node:26.05", "ghcr.io/gsi-hpc/sind-node:latest", "localhost:5000/own/node:dev",
		"Registry.Example.org/own__node", "[::1]:5000/node", "node@" + digest, "ghcr.io/gsi-hpc/sind-node:26.05@" + digest,
	} {
		assert.NoError(t, imageError(s), s)
	}
	for _, tt := range []struct{ image, want string }{
		{"sind node", "use no spaces, such as ghcr.io/gsi-hpc/sind-node:latest"},
		{"ghcr.io/GSI-HPC/sind-node:26.05", "use lowercase letters, such as ghcr.io/gsi-hpc/sind-node:26.05"},
		{"ghcr.io/GSI/Node:V1", "use lowercase letters, such as ghcr.io/gsi/node:V1"},
		{"Node@" + digest, "use lowercase letters, such as node@" + digest},
		{"node@sha256:abc", "write the digest after @ as sha256: and its 64 hex digits"},
		{"node:", "write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05"},
		{"node:-dev", "write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05"},
		{strings.Repeat("a", 256) + ":1", "use at most 255 characters before the tag; this name has 256"},
		{"-v", "write an image reference such as ghcr.io/gsi-hpc/sind-node:latest"},
		{"own/Node_:x", "write an image reference such as ghcr.io/gsi-hpc/sind-node:latest"},
	} {
		err := imageError(tt.image)
		require.Error(t, err, tt.image)
		assert.Equal(t, tt.want, err.Error(), tt.image)
	}
}

func TestBuildCreateCluster_YAMLImages(t *testing.T) {
	cmd, err := build(t, "create-cluster", yamlForm("kind: Cluster\nname: lab\ndefaults:\n  image: Own/Node\nnodes:\n  - role: controller\n    image: Own/Node\n  - role: worker\n    image: \"own/node:\"\n"))
	require.NoError(t, err)
	assert.Equal(t, []Problem{
		{Field: "yaml", Label: "Config", Text: "image Own/Node: use lowercase letters, such as own/node"},
		{Field: "yaml", Label: "Config", Text: "image own/node:: write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05"},
	}, cmd.Problems)
}

func TestQOSError(t *testing.T) {
	assert.NoError(t, qosError("MaxJobs", "high"))
	assert.NoError(t, qosError("QOS", "normal"))
	assert.NoError(t, qosError("qoslevel", "+Normal, "))
	err := qosError("DefaultQOS", "normal,high")
	require.Error(t, err)
	assert.Equal(t, "DefaultQOS=normal,high names the QOS high, but a new cluster has only the QOS normal, and sind creates no QOS; remove the limit, or create the QOS after the cluster", err.Error())
}

func TestBuildCreateCluster_QOS(t *testing.T) {
	f := rows(valid("db", "on"), AccountRows, "a0", "name", "physics")
	f["a0-lk"], f["a0-lv"] = []string{"QOS", "MaxJobs"}, []string{"high", "4"}
	cmd, err := build(t, "create-cluster", f)
	require.NoError(t, err)
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, "a0-lk", cmd.Problems[0].Field)
	assert.Equal(t, "Limits of physics", cmd.Problems[0].Label)
	assert.Contains(t, cmd.Problems[0].Text, "QOS=high names the QOS high")
	assert.Contains(t, cmd.Config.Content, "MaxJobs: \"4\"", "the other limits stay")
	assert.NotContains(t, cmd.Config.Content, "high")

	cmd, err = build(t, "create-cluster", yamlForm("kind: Cluster\nname: lab\nnodes:\n  - role: controller\n  - role: db\n  - role: worker\naccounts:\n  - name: physics\n    limits:\n      DefaultQOS: high\n"))
	require.NoError(t, err)
	assert.Equal(t, []Problem{{Field: "yaml", Label: "Config", Text: "account physics: DefaultQOS=high names the QOS high, but a new cluster has only the QOS normal, and sind creates no QOS; remove the limit, or create the QOS after the cluster"}}, cmd.Problems)
}

func TestSlurmWarnings(t *testing.T) {
	cluster := func(main string, backup bool, managed *bool) config.Cluster {
		return config.Cluster{
			Nodes: []config.Node{{Role: config.RoleController, BackupController: backup, Managed: managed}},
			Slurm: config.Slurm{Main: config.Section{Content: main}},
		}
	}
	assert.Empty(t, slurmWarnings(cluster("SelectType=select/cons_tres\n# a comment\n\ninclude /etc/slurm/more.conf\n", false, nil)))
	assert.Equal(t, []string{"The lines of slurm.conf set ClusterName, SlurmUser, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start."},
		slurmWarnings(cluster("clustername=prod\nSlurmUser=root\n", false, nil)))
	assert.Equal(t, []string{"The lines of slurm.conf set SlurmdSpoolDir, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start."},
		slurmWarnings(cluster("SlurmctldHost=x\nStateSaveLocation=/s\nSlurmdSpoolDir=/d\n", true, nil)), "the config refuses the others with a backup controller")
	off := false
	assert.Empty(t, slurmWarnings(cluster("ClusterName=prod\n", false, &off)), "the config refuses Slurm config of an unmanaged cluster")

	c := cluster("SelectType select/cons_tres # no =\n", false, nil)
	c.Slurm.Plugstack = config.Section{Content: "optional /usr/lib64/slurm/spank_demo.so\n"}
	c.Slurm.Cgroup = config.Section{Fragments: map[string]string{"ram": "ConstrainRAMSpace=yes\nConstrainCores\n"}}
	assert.Equal(t, []string{
		`The lines of slurm.conf have "SelectType select/cons_tres", which is no Key=Value pair. Slurm may refuse it and not start.`,
		`The lines of cgroup.conf have "ConstrainCores", which is no Key=Value pair. Slurm may refuse it and not start.`,
	}, slurmWarnings(c))
}

func TestBuildCreateCluster_SlurmWarnings(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("slurm-main", "ClusterName=prod"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Problems)
	assert.Contains(t, cmd.Warns, "The lines of slurm.conf set ClusterName, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start.")
}
