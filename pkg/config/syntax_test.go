// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"encoding/json"
	"errors"
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
		{"memory number in full node", "kind: Cluster\nnodes:\n  - role: worker\n    memory: 1024\n", `"memory" must be a string, got a number (quote it)`},
		{"count a string", "kind: Cluster\nnodes:\n  - role: worker\n    count: many\n", "nodes[0].count: expected a whole number, got a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
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

	// An alias of a node is followed.
	_, err = Parse([]byte("kind: Cluster\nnodes:\n  - &n {role: worker, cpu: 1}\n  - *n\n"))
	require.ErrorContains(t, err, "nodes[0].cpu: unknown key")
	_, err = Parse([]byte("kind: Cluster\nname: &n x\nrealm: *n\n"))
	require.NoError(t, err)
}

func TestFriendlyType(t *testing.T) {
	assert.Equal(t, "a list or an object", friendlyType("[]string"))
	assert.Equal(t, "a list or an object", friendlyType("map[string]string"))
	assert.Equal(t, "a string", friendlyType("string"))
	assert.Equal(t, "a whole number", friendlyType("int"))
	assert.Equal(t, "a whole number", friendlyType("uint16"))
	assert.Equal(t, "true or false", friendlyType("bool"))
	assert.Equal(t, "an object", friendlyType("config.Node"))
	assert.Equal(t, "a list", jsonKind("array"))
	assert.Equal(t, "a boolean", jsonKind("bool"))
}

func TestPlainError(t *testing.T) {
	err := plainError(errors.New("error unmarshaling JSON: while decoding JSON: json: cannot unmarshal object into Go struct field .nodes of type []config.Node"))
	assert.EqualError(t, err, `"nodes" must be a list or an object, got an object`)

	err = plainError(errors.New("json: cannot unmarshal string into Go value of type int"))
	assert.EqualError(t, err, "expected a whole number, got a string")

	err = plainError(errors.New("json: unknown field \"x\""))
	assert.EqualError(t, err, `unknown field "x"`)
}

// The decoders keep their own errors for callers that decode a value
// without Parse, which checks the syntax first.
func TestUnmarshalJSON_Errors(t *testing.T) {
	tests := []struct {
		name string
		into json.Unmarshaler
		data string
		want string
	}{
		{"node", &Node{}, `[1]`, "node must be a string"},
		{"node unknown field", &Node{}, `{"role":"worker","x":1}`, "unknown field"},
		{"shorthand keys", &Node{}, `{"a":1,"b":2}`, "exactly one key"},
		{"shorthand count", &Node{}, `{"worker":"x"}`, "shorthand node count"},
		{"limits", &Limits{}, `[1]`, "limits must be a map"},
		{"identity", &Identity{}, `[1]`, "identity must be"},
		{"user", &User{}, `[1]`, "user must be"},
		{"group", &Group{}, `[1]`, "group must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.into.UnmarshalJSON([]byte(tt.data)), tt.want)
		})
	}
}
