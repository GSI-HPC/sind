// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_SyntaxErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"typo shows line, path and valid keys", "kind: Cluster\nnodes:\n  - controller\n  - role: worker\n    cpu: 4\n", "line 5: nodes[1].cpu: unknown key (valid: role, count, image, cpus,"},
		{"key case", "kind: Cluster\nNAME: x\n", `line 2: NAME: unknown key (valid: kind, name,`},
		{"key case hint", "kind: Cluster\nNAME: x\n", `did you mean "name"? keys are case-sensitive`},
		{"comment only", "# nothing here\n", "empty configuration"},
		{"null document", "~\n", "empty configuration"},
		{"yaml syntax", "kind: [Cluster\n", "line 1: did not find expected"},
		{"mis-indented list item", "kind: Cluster\nnodes:\n  - controller\n    - worker\n", `line 3: nodes[0]: "controller - worker" is not a role; check the indentation of the list`},
		{"shorthand with options", "kind: Cluster\nnodes:\n  - worker: 2\n    cpus: 4\n", `line 3: nodes[0]: a node object needs "role: NAME"`},
		{"shorthand count", "kind: Cluster\nnodes:\n  - worker: [2]\n", "line 3: nodes[0].worker: expected a whole number, got a list"},
		{"nodes not a list", "kind: Cluster\nnodes: {worker: 2}\n", "line 2: nodes: expected a list, got an object"},
		{"defaults not an object", "kind: Cluster\ndefaults: fast\n", "line 2: defaults: expected an object, got a string"},
		{"cpus not a number", "kind: Cluster\ndefaults:\n  cpus: 2.5\n", "line 3: defaults.cpus: expected a whole number, got a number"},
		{"cpus a boolean", "kind: Cluster\ndefaults:\n  cpus: true\n", "defaults.cpus: expected a whole number, got a boolean"},
		{"image an object", "kind: Cluster\ndefaults:\n  image: {a: b}\n", "defaults.image: expected a string, got an object"},
		{"limits a list", "kind: Cluster\naccounts:\n  - name: a\n    limits: [x]\n", "expected an object, got a list"},
		{"managed a number", "kind: Cluster\nnodes:\n  - role: worker\n    managed: 1\n", "line 4: nodes[0].managed: expected true or false, got a number"},
		{"section a list", "kind: Cluster\nslurm:\n  main: [a]\n", "line 3: slurm.main: expected an object, got a list"},
		{"fragment a list", "kind: Cluster\nslurm:\n  cgroup:\n    a.conf: [b]\n", "line 4: slurm.cgroup.a.conf: expected a string, got a list"},
		{"duplicate key", "kind: Cluster\nname: a\nname: b\n", "line 3: name: duplicate key (first on line 2)"},
		{"duplicate node key", "kind: Cluster\nnodes:\n  - role: worker\n    cpus: 1\n    cpus: 2\n", "line 5: nodes[0].cpus: duplicate key (first on line 4)"},
		{"duplicate limit", "kind: Cluster\naccounts:\n  - name: a\n    limits: {MaxJobs: 1, MaxJobs: 2}\n", "line 4: accounts[0].limits.MaxJobs: duplicate key (first on line 4)"},
		{"duplicate fragment", "kind: Cluster\nslurm:\n  cgroup: {a: b, a: c}\n", "line 3: slurm.cgroup.a: duplicate key (first on line 3)"},
		{"duplicate key in a merged object", "kind: Cluster\nnodes:\n  - role: worker\n    <<: {cpus: 1,\n      cpus: 2}\n", "line 5: nodes[0].cpus: duplicate key (first on line 4)"},
		{"merged key unknown", "kind: Cluster\nnodes:\n  - &w {role: worker, cpu: 2}\n  - {<<: *w}\n", "line 3: nodes[0].cpu: unknown key"},
		{"merge of a string", "kind: Cluster\nnodes:\n  - role: worker\n    <<: x\n", "line 4: nodes[0].<<: expected an object or a list of objects, got a string"},
		{"merge of a list of strings", "kind: Cluster\nslurm:\n  cgroup: {<<: [{a: b}, c]}\n", "line 3: slurm.cgroup.<<: expected an object or a list of objects, got a string"},
		{"merge of itself", "kind: Cluster\ndefaults: &d {<<: *d}\n", "line 2: defaults.<<: an object cannot merge itself"},
		{"nested merge of itself", "kind: Cluster\nnodes:\n  - &a {role: worker, <<: {<<: *a}}\n", "line 3: nodes[0].<<: an object cannot merge itself"},
		{"merged shorthand count", "kind: Cluster\nnodes:\n  - <<: {worker: x}\n", "line 3: nodes[0].worker: expected a whole number, got a string"},
		{"count a string", "kind: Cluster\nnodes:\n  - role: worker\n    count: many\n", "nodes[0].count: expected a whole number, got a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			require.ErrorContains(t, err, tt.want)
			assert.NotContains(t, err.Error(), "JSON")
			assert.NotContains(t, err.Error(), "Go ")
		})
	}
}

func TestParse_SyntaxAccepts(t *testing.T) {
	c, err := Parse([]byte("kind: Cluster\ndefaults: &d\n  cpus: 2\nslurm:\n  cgroup:\n    a.conf: b\nnodes:\n  - controller\n  - worker: 3\n  - worker:\n  - role: worker\n    cpus: 2\n"))
	require.NoError(t, err)
	assert.Len(t, c.Nodes, 4)

	c, err = Parse([]byte("kind: Cluster\ndefaults: &d\n  cpus: 2\nnodes:\n  - role: worker\n    <<: *d\n"))
	require.NoError(t, err)
	assert.Equal(t, 2, c.Nodes[0].CPUs)

	c, err = Parse([]byte("kind: Cluster\nnodes:\n  - role: controller\n    managed: false\n"))
	require.NoError(t, err)
	assert.False(t, *c.Nodes[0].Managed)

	_, err = Parse([]byte("kind: Cluster\ndefaults:\nnodes:\n"))
	require.NoError(t, err)

	// A string field takes any scalar as written; YAML 1.2 has no yes/no
	// booleans, but bool fields still take them.
	c, err = Parse([]byte("kind: Cluster\nname: 123\nrealm: no\nnodes:\n  - role: worker\n    memory: 1024\n    managed: yes\n"))
	require.NoError(t, err)
	assert.Equal(t, "123", c.Name)
	assert.Equal(t, "no", c.Realm)
	assert.Equal(t, "1024", c.Nodes[0].Memory)
	assert.True(t, *c.Nodes[0].Managed)

	// A merge key (<<) adds the keys of the objects it names, which may
	// give a node its role, through an alias and a list of objects, and
	// recursively.
	c, err = Parse([]byte("kind: Cluster\nnodes: [controller, &w {role: worker, cpus: 2}, {<<: *w, memory: 4g}]\n"))
	require.NoError(t, err)
	assert.Equal(t, Node{Role: RoleWorker, CPUs: 2, Memory: "4g"}, c.Nodes[2])
	c, err = Parse([]byte("kind: Cluster\nnodes:\n  - controller\n  - <<: {role: worker}\n"))
	require.NoError(t, err)
	assert.Equal(t, RoleWorker, c.Nodes[1].Role)
	c, err = Parse([]byte("kind: Cluster\nnodes:\n  - controller\n  - &a {role: worker}\n  - &b {<<: *a, cpus: 2}\n  - <<: [*b, {memory: 1g}]\n    count: 2\n"))
	require.NoError(t, err)
	assert.Equal(t, Node{Role: RoleWorker, Count: 2, CPUs: 2, Memory: "1g"}, c.Nodes[3])
	c, err = Parse([]byte("kind: Cluster\nnodes:\n  - <<: {worker: 3}\n"))
	require.NoError(t, err)
	assert.Equal(t, Node{Role: RoleWorker, Count: 3}, c.Nodes[0])
	c, err = Parse([]byte("kind: Cluster\nslurm:\n  cgroup: &f {a.conf: x}\n  gres: {<<: *f, b.conf: y}\n"))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.conf": "x", "b.conf": "y"}, c.Slurm.Gres.Fragments)

	// A key of the mapping overrides the same key of a merged object.
	c, err = Parse([]byte("kind: Cluster\nnodes:\n  - <<: {role: worker, cpus: 1}\n    cpus: 2\n"))
	require.NoError(t, err)
	assert.Equal(t, 2, c.Nodes[0].CPUs)

	// An alias of a node is followed.
	_, err = Parse([]byte("kind: Cluster\nnodes:\n  - &n {role: worker, cpu: 1}\n  - *n\n"))
	require.ErrorContains(t, err, "nodes[0].cpu: unknown key")
	_, err = Parse([]byte("kind: Cluster\nname: &n x\nrealm: *n\n"))
	require.NoError(t, err)
}
