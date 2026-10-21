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
		{"unclosed flow sequence", "kind: Cluster\nname: t\nnodes: [controller, worker\nrealm: r\n", "line 3: did not find expected ',' or ']'"},
		{"unclosed flow mapping", "kind: Cluster\ndefaults: {cpus: 2\n", "line 2: did not find expected ',' or '}'"},
		{"under-indented key", "kind: Cluster\ndefaults:\n  cpus: 2\n memory: 1g\n", "line 4: did not find expected key"},
		{"under-indented list item", "kind: Cluster\nnodes:\n  - controller\n - worker\n", "line 4: did not find expected key"},
		{"over-indented key", "kind: Cluster\ndefaults:\n  cpus: 2\n   memory: 1g\n", "line 4: mapping values are not allowed in this context"},
		{"list after a key", "kind: Cluster\n- worker\n", "line 2: did not find expected key"},
		{"key after a list", "- worker\nkind: Cluster\n", "line 2: did not find expected '-' indicator"},
		{"tab after a key", "kind: Cluster\nname: t\n\trealm: r\n", "line 3: found a tab character that violates indentation"},
		{"tab after a list item", "kind: Cluster\nnodes:\n  - controller\n  - worker\n  \t- db\n", "line 5: found a tab character that violates indentation"},
		{"tab in a block scalar", "kind: Cluster\nslurm:\n  main: |\n    a\n\tb\n", "line 5: found a tab character where an indentation space is expected"},
		{"tab before a list item", "kind: Cluster\nnodes:\n\t- worker\n", "line 3: found character that cannot start any token"},
		{"unclosed quote", "kind: Cluster\nname: \"t\n", "line 2: found unexpected end of stream"},
		{"unknown anchor", "kind: Cluster\nname: *x\n", "unknown anchor 'x' referenced"},
		{"mis-indented list item", "kind: Cluster\nnodes:\n  - controller\n    - worker\n", `line 3: nodes[0]: "controller - worker" is not a role; check the indentation of the list`},
		{"shorthand with options", "kind: Cluster\nnodes:\n  - worker: 2\n    cpus: 4\n", `line 3: nodes[0]: a node object needs "role: NAME"`},
		{"role key case", "kind: Cluster\nnodes:\n  - Role: controller\n", `line 3: nodes[0].Role: unknown key (valid: role, count,`},
		{"role key case hint", "kind: Cluster\nnodes:\n  - Role: controller\n    cpus: 2\n", `did you mean "role"? keys are case-sensitive`},
		{"role key misspelled", "kind: Cluster\nnodes:\n  - rol: controller\n", `line 3: nodes[0]: a node object needs "role: NAME"`},
		{"node object without role", "kind: Cluster\nnodes:\n  - image: foo\n", `line 3: nodes[0]: a node object needs "role: NAME"`},
		{"node count without role", "kind: Cluster\nnodes:\n  - count: 3\n", `line 3: nodes[0]: a node object needs "role: NAME"`},
		{"shorthand role case", "kind: Cluster\nnodes:\n  - Worker: 3\n", `line 3: nodes[0]: invalid role "Worker" (valid: controller, db, api, submitter, worker); did you mean "worker"?`},
		{"shorthand role case and count", "kind: Cluster\nnodes:\n  - Worker: x\n", `line 3: nodes[0]: invalid role "Worker"`},
		{"shorthand unknown role", "kind: Cluster\nnodes:\n  - gpu: 3\n", `line 3: nodes[0]: invalid role "gpu"`},
		{"shorthand count", "kind: Cluster\nnodes:\n  - worker: [2]\n", "line 3: nodes[0].worker: expected a whole number, got a list"},
		{"nodes not a list", "kind: Cluster\nnodes: {worker: 2}\n", "line 2: nodes: expected a list, got an object"},
		{"defaults not an object", "kind: Cluster\ndefaults: fast\n", "line 2: defaults: expected an object, got a string"},
		{"cpus not a whole number", "kind: Cluster\ndefaults:\n  cpus: 2.5\n", "line 3: defaults.cpus: expected a whole number, got a decimal number"},
		{"cpus too large", "kind: Cluster\ndefaults:\n  cpus: 99999999999999999999\n", "line 3: defaults.cpus: 99999999999999999999 is out of range"},
		{"cpus too small", "kind: Cluster\ndefaults:\n  cpus: -99999999999999999999\n", "line 3: defaults.cpus: -99999999999999999999 is out of range"},
		{"count too large", "kind: Cluster\nnodes:\n  - role: worker\n    count: 18446744073709551615\n", "line 4: nodes[0].count: 18446744073709551615 is out of range"},
		{"uid 0", "kind: Cluster\nusers:\n  - name: alice\n    uid: 0\n", "line 4: users[0].uid: must be between 1000 and 2147483647, got 0; leave it out for the lowest free ID"},
		{"gid 0", "kind: Cluster\ngroups:\n  - name: hpc\n    gid: 0x0\n", "line 4: groups[0].gid: must be between 1000 and 2147483647, got 0"},
		{"role a number", "kind: Cluster\nnodes: [controller, worker, 3]\n", `line 2: nodes[2]: invalid role "3" (valid: controller, db, api, submitter, worker)`},
		{"role case", "kind: Cluster\nnodes: [Controller, worker]\n", `line 2: nodes[0]: invalid role "Controller" (valid: controller, db, api, submitter, worker); did you mean "controller"? roles are case-sensitive`},
		{"role key a number", "kind: Cluster\nnodes:\n  - role: 1\n", `line 3: nodes[0].role: invalid role "1" (valid:`},
		{"identity mode", "kind: Cluster\nidentity:\n  mode: 1\n", `line 3: identity.mode: invalid identity mode "1" (valid: local, nssSlurm, clientIds)`},
		{"identity case", "kind: Cluster\nidentity: nssslurm\n", `line 2: identity: invalid identity mode "nssslurm" (valid: local, nssSlurm, clientIds); did you mean "nssSlurm"? identity modes are case-sensitive`},
		{"admin level", "kind: Cluster\nusers:\n  - name: alice\n    adminLevel: 1\n", `line 4: users[0].adminLevel: invalid admin level "1" (valid: operator, admin)`},
		{"storage type", "kind: Cluster\nstorage:\n  dataStorage:\n    type: Volume\n", `line 4: storage.dataStorage.type: invalid storage type "Volume" (valid: volume, hostPath); did you mean "volume"?`},
		{"quoted boolean", "kind: Cluster\nnodes:\n  - role: controller\n    backupController: \"yes\"\n", "line 4: nodes[0].backupController: expected true or false, got a string"},
		{"quoted false", "kind: Cluster\nmonitoring: 'false'\n", "line 2: monitoring: expected true or false, got a string"},
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

func TestParse_SyntaxErrorOnFirstLine(t *testing.T) {
	// The parser gives no line for a mark on its line 0, nor for a
	// character it cannot decode.
	_, err := Parse([]byte("]\n"))
	require.ErrorContains(t, err, "line 1: did not find expected node content")
	_, err = Parse([]byte("!x!y z\n"))
	require.ErrorContains(t, err, "line 1: found undefined tag handle")
	_, err = Parse([]byte("kind: [Cluster\n"))
	require.EqualError(t, err, "line 1: did not find expected ',' or ']'")
	_, err = Parse([]byte("a: b: c\n"))
	require.EqualError(t, err, "line 1: mapping values are not allowed in this context")
	_, err = Parse([]byte("kind: Cluster\nname: \x01\n"))
	require.EqualError(t, err, "control characters are not allowed")
}

func TestParse_ErrorsNameNoSource(t *testing.T) {
	// The caller names the source.
	_, err := Parse([]byte("# nothing\n"))
	require.EqualError(t, err, "empty configuration")
	_, err = Parse([]byte("kind: Cluster\nname: !!int x\n"))
	require.EqualError(t, err, "cannot decode !!str `x` as a !!int")
}

func TestTabLine(t *testing.T) {
	assert.Equal(t, 3, tabLine([]byte("a\n b\n\tc\n"), 1))
	assert.Equal(t, 2, tabLine([]byte("a\nb\n"), 2))
	assert.Equal(t, 9, tabLine([]byte("a\n"), 9))
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

	// A uid or gid left out, or null, is assigned.
	c, err = Parse([]byte("kind: Cluster\nusers:\n  - name: alice\n    uid:\n  - name: bob\n    uid: 2000\ngroups:\n  - hpc\n"))
	require.NoError(t, err)
	assert.Equal(t, []int{0, 2000}, []int{c.Users[0].UID, c.Users[1].UID})
	assert.Zero(t, c.Groups[0].GID)

	// An empty value is left to Validate, which reports a role but takes
	// no admin level.
	c, err = Parse([]byte("kind: Cluster\nnodes:\n  - role: ''\nusers:\n  - name: a\n    adminLevel: ''\n"))
	require.NoError(t, err)
	assert.Empty(t, c.Nodes[0].Role)
	assert.Empty(t, c.Users[0].AdminLevel)

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
