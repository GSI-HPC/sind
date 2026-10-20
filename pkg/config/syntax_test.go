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
		{"duplicate limit", "kind: Cluster\naccounts:\n  - name: a\n    limits: {MaxJobs: 1, MaxJobs: 2}\n", `mapping key "MaxJobs" already defined`},
		{"duplicate fragment", "kind: Cluster\nslurm:\n  cgroup: {a: b, a: c}\n", `mapping key "a" already defined`},
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

	// An alias of a node is followed.
	_, err = Parse([]byte("kind: Cluster\nnodes:\n  - &n {role: worker, cpu: 1}\n  - *n\n"))
	require.ErrorContains(t, err, "nodes[0].cpu: unknown key")
	_, err = Parse([]byte("kind: Cluster\nname: &n x\nrealm: *n\n"))
	require.NoError(t, err)
}
