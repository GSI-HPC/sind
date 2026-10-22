// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCreateCluster_Slurm(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("db", "on",
		"slurm-main", "SelectType=select/cons_tres\r\nMaxJobCount=100", "slurm-cgroup", "  \n", "slurm-slurmdbd", "PurgeJobAfter=12month\n",
		"identity", "clientIds", "ctlusers", "on", "cvmfs", "on"))
	require.NoError(t, err)
	require.Empty(t, cmd.Problems)
	assert.Contains(t, cmd.Config.Content, `storage:
  dataStorage:
    type: hostPath
    hostPath: /scratch/hpc
  cvmfs: true
slurm:
  main: |
    SelectType=select/cons_tres
    MaxJobCount=100
  slurmdbd: |
    PurgeJobAfter=12month
identity:
  mode: clientIds
  controllerUsers: true
`)
	assert.Equal(t, []string{"slurm-main", "slurm-cgroup", "slurm-gres", "slurm-topology", "slurm-plugstack", "slurm-slurmdbd"}, SlurmFields())
}

func TestBuildCreateCluster_Identity(t *testing.T) {
	for mode, want := range map[string]string{"": "", "local": "", "nssSlurm": "identity:\n  mode: nssSlurm\n"} {
		cmd, err := build(t, "create-cluster", valid("identity", mode, "ctlusers", "on"))
		require.NoError(t, err)
		require.Empty(t, cmd.Problems, mode)
		if want == "" {
			assert.NotContains(t, cmd.Config.Content, "identity:")
		} else {
			assert.Contains(t, cmd.Config.Content, want, "controllerUsers only with clientIds")
		}
	}

	cmd, err := build(t, "create-cluster", valid("identity", "ldap"))
	require.NoError(t, err)
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, Problem{Field: "identity", Label: "Identity", Text: "pick local, nssSlurm or clientIds", Default: true}, cmd.Problems[0])
}

func TestBuildCreateCluster_SlurmTheConfigChecks(t *testing.T) {
	cmd, err := build(t, "create-cluster", valid("slurm-slurmdbd", "PurgeJobAfter=1month", "identity", "nssSlurm", "controller-unmanaged", "on"))
	require.NoError(t, err)
	var texts []string
	for _, p := range cmd.Problems {
		texts = append(texts, p.Text)
	}
	require.Len(t, texts, 3, "%q", texts)
	assert.Contains(t, texts[0], "identity nssSlurm requires a managed controller")
	assert.Contains(t, texts[1], "slurm slurmdbd requires a managed controller")
	assert.Contains(t, texts[2], "slurm slurmdbd requires a managed db node")
}
