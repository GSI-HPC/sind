// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// yamlForm is a form in which the user edits the YAML.
func yamlForm(text string, kv ...string) Form {
	return form(append([]string{"yaml_on", "1", "yaml", text, "name", "ignored", "w0-count", "x"}, kv...)...)
}

func TestBuildCreateCluster_YAML(t *testing.T) {
	cmd, err := build(t, "create-cluster", yamlForm("kind: Cluster\nname: lab # my lab\nnodes:\n  - role: controller\n    backupController: true\n  - db\n  - worker: 3\n  - role: worker\n    cpus: 4\n"+
		"storage:\n  dataStorage:\n    hostPath: /scratch/missing-lab\n", "pull", "on", "wait", "0"))
	require.NoError(t, err)
	require.Empty(t, cmd.Problems)

	assert.Equal(t, "sind create cluster --config /state/sind/ui/configs/lab.yaml --pull --wait 0", cmd.Display)
	assert.Contains(t, cmd.Config.Content, "name: lab # my lab\n", "the YAML as the user wrote it")
	var shorts []string
	for _, n := range cmd.Nodes {
		shorts = append(shorts, n.Short)
	}
	assert.Equal(t, []string{"controller", "controller-backup", "db", "worker-0", "worker-1", "worker-2", "worker-3"}, shorts)
	assert.Equal(t, "7 containers, 10 CPUs, 3.5 GiB of memory.", cmd.Summary)
	assert.Equal(t, []string{"/scratch/missing-lab"}, cmd.Dirs, "a missing directory is created")
}

func TestBuildCreateCluster_YAMLLineEnds(t *testing.T) {
	cmd, err := build(t, "create-cluster", yamlForm("kind: Cluster\r\nname: lab\r\n"))
	require.NoError(t, err)
	assert.Equal(t, "kind: Cluster\nname: lab\n", cmd.Config.Content)
	assert.Len(t, cmd.Nodes, 2, "sind's default nodes: a controller and a worker")
}

func TestBuildCreateCluster_YAMLProblems(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		kv         []string
		want       []string
	}{
		{"does not parse", "kind: Cluster\nname: [", nil, []string{"YAML: "}},
		{"empty", "", nil, []string{"YAML: empty configuration"}},
		{"other realm", "kind: Cluster\nname: lab\nrealm: ci\n", nil, []string{"Realm: the YAML names realm ci, and this field sind; make them the same, or remove realm from the YAML"}},
		{"same realm", "kind: Cluster\nname: lab\nrealm: ci\n", []string{"realm", "ci"}, nil},
		{"name taken", "kind: Cluster\nname: dev\n", nil, []string{"Name: realm sind has a cluster dev already"}},
		{"bad name", "kind: Cluster\nname: Lab\nnodes: [controller, worker, db]\n", nil, []string{"Name: use lowercase letters, such as lab"}},
		{"config", "kind: Cluster\nname: lab\nnodes: [worker]\n", nil, []string{"Config: exactly one controller required, got 0"}},
		{"file", "kind: Cluster\nname: lab\nstorage:\n  dataStorage:\n    hostPath: /scratch/file\n", nil, []string{"Directory: /scratch/file is not a directory"}},
		{"wait", "kind: Cluster\nname: lab\n", []string{"wait", "soon"}, []string{"Wait for ready: write a time"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := build(t, "create-cluster", yamlForm(tc.yaml, tc.kv...))
			require.NoError(t, err)
			var got []string
			for _, p := range cmd.Problems {
				got = append(got, p.Label+": "+p.Text)
				if p.Label != "Wait for ready" && p.Label != "Realm" {
					assert.Equal(t, "yaml", p.Field, "a problem of the YAML links to it")
				}
			}
			require.Len(t, got, len(tc.want), "%q", got)
			for i, w := range tc.want {
				assert.Contains(t, got[i], w)
			}
		})
	}
}

func TestBuildCreateCluster_YAMLManyWorkers(t *testing.T) {
	cmd, err := build(t, "create-cluster", yamlForm(fmt.Sprintf("kind: Cluster\nname: lab\nnodes: [controller, worker: %d]\n", maxPlanned+1)))
	require.NoError(t, err)
	assert.Len(t, cmd.Nodes, 1+maxPlanned)
	require.Len(t, cmd.Problems, 1)
	assert.Equal(t, Problem{Field: "yaml", Label: "Workers", Text: "a cluster here has at most 16384 workers"}, cmd.Problems[0])
}
