// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCreateCluster_YAMLImages(t *testing.T) {
	cmd, err := build(t, "create-cluster", yamlForm("kind: Cluster\nname: lab\ndefaults:\n  image: Own/Node\nnodes:\n  - role: controller\n    image: Own/Node\n  - role: worker\n    image: \"own/node:\"\n"))
	require.NoError(t, err)
	assert.Equal(t, []Problem{
		{Field: "yaml", Label: "Config", Text: "image Own/Node: use lowercase letters, such as own/node"},
		{Field: "yaml", Label: "Config", Text: "image own/node:: write the tag after : with at most 128 letters, digits, _, . and -, such as 26.05"},
	}, cmd.Problems)
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
	assert.Equal(t, []Problem{{Field: "yaml", Label: "Config", Text: `account "physics": DefaultQOS=high names the QOS high, but a new cluster has only the QOS normal, and sind creates no QOS; remove the limit, or create the QOS after the cluster`}}, cmd.Problems)
}

func TestBuildCreateCluster_SlurmWarnings(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("slurm-main", "ClusterName=prod"))
	require.NoError(t, err)
	assert.Empty(t, cmd.Problems)
	assert.Contains(t, cmd.Warns, "The lines of slurm.conf set ClusterName, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start.")
}

// TestBuildCreateCluster_SlurmWarningsWithErrors checks that the plan warns
// of the Slurm config while the config has errors too, and lists each QOS
// problem of the YAML.
func TestBuildCreateCluster_SlurmWarningsWithErrors(t *testing.T) {
	cmd, err := build(t, "create-cluster", yamlForm("kind: Cluster\nname: lab\nnodes:\n  - role: controller\n  - role: db\n  - role: worker\n"+
		"slurm:\n  main: |\n    ClusterName=prod\n"+
		"accounts:\n  - name: physics\n    limits:\n      QOS: high\n  - name: theory\n    limits:\n      DefaultQOS: low\n"))
	require.NoError(t, err)
	require.Len(t, cmd.Problems, 2)
	assert.Contains(t, cmd.Problems[0].Text, `account "physics": QOS=high`)
	assert.Contains(t, cmd.Problems[1].Text, `account "theory": DefaultQOS=low`)
	assert.Contains(t, cmd.Warns, "The lines of slurm.conf set ClusterName, which sind sets itself. Slurm takes your value in place of sind's, and the cluster may not start.")
}
