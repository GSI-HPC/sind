// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_MinimalYAML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
	}{
		{
			name:     "kind only",
			input:    "kind: Cluster",
			wantName: "default",
		},
		{
			name:     "kind with explicit name",
			input:    "kind: Cluster\nname: dev",
			wantName: "dev",
		},
		{
			name:     "kind with explicit default name",
			input:    "kind: Cluster\nname: default",
			wantName: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.input))
			require.NoError(t, err)
			assert.Equal(t, "Cluster", cfg.Kind)
			assert.Equal(t, tt.wantName, cfg.Name)
		})
	}
}

func TestParse_WithDefaults(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantImage string
		wantCPUs  int
		wantMem   string
		wantTmp   string
	}{
		{
			name: "all defaults specified",
			input: `kind: Cluster
defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11.2
  cpus: 4
  memory: 8g
  tmpSize: 2g`,
			wantImage: "ghcr.io/gsi-hpc/sind-node:25.11.2",
			wantCPUs:  4,
			wantMem:   "8g",
			wantTmp:   "2g",
		},
		{
			name: "partial defaults",
			input: `kind: Cluster
defaults:
  image: custom:latest`,
			wantImage: "custom:latest",
		},
		{
			name:  "no defaults section",
			input: "kind: Cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.input))
			require.NoError(t, err)
			if tt.wantImage != "" {
				assert.Equal(t, tt.wantImage, cfg.Defaults.Image)
			}
			if tt.wantCPUs != 0 {
				assert.Equal(t, tt.wantCPUs, cfg.Defaults.CPUs)
			}
			if tt.wantMem != "" {
				assert.Equal(t, tt.wantMem, cfg.Defaults.Memory)
			}
			if tt.wantTmp != "" {
				assert.Equal(t, tt.wantTmp, cfg.Defaults.TmpSize)
			}
		})
	}
}

func TestParse_NodesFullForm(t *testing.T) {
	input := `kind: Cluster
nodes:
  - role: controller
    cpus: 2
    memory: 4g
    tmpSize: 2g
  - role: submitter
  - role: worker
    count: 3
    cpus: 4
    memory: 8g
  - role: worker
    count: 2
    managed: false`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 4)

	// controller
	assert.Equal(t, "controller", cfg.Nodes[0].Role)
	assert.Equal(t, 2, cfg.Nodes[0].CPUs)
	assert.Equal(t, "4g", cfg.Nodes[0].Memory)
	assert.Equal(t, "2g", cfg.Nodes[0].TmpSize)

	// submitter
	assert.Equal(t, "submitter", cfg.Nodes[1].Role)

	// managed worker
	assert.Equal(t, "worker", cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
	assert.Equal(t, 4, cfg.Nodes[2].CPUs)
	assert.Equal(t, "8g", cfg.Nodes[2].Memory)
	assert.Nil(t, cfg.Nodes[2].Managed)

	// unmanaged worker
	assert.Equal(t, "worker", cfg.Nodes[3].Role)
	assert.Equal(t, 2, cfg.Nodes[3].Count)
	require.NotNil(t, cfg.Nodes[3].Managed)
	assert.False(t, *cfg.Nodes[3].Managed)
}

func TestParse_NodesShorthand(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - submitter
  - worker: 3`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 3)

	assert.Equal(t, "controller", cfg.Nodes[0].Role)
	assert.Equal(t, 0, cfg.Nodes[0].Count)

	assert.Equal(t, "submitter", cfg.Nodes[1].Role)

	assert.Equal(t, "worker", cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
}

func TestParse_NodesMixed(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - role: worker
    count: 3
    cpus: 4
  - worker: 2`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 3)

	assert.Equal(t, "controller", cfg.Nodes[0].Role)

	assert.Equal(t, "worker", cfg.Nodes[1].Role)
	assert.Equal(t, 3, cfg.Nodes[1].Count)
	assert.Equal(t, 4, cfg.Nodes[1].CPUs)

	assert.Equal(t, "worker", cfg.Nodes[2].Role)
	assert.Equal(t, 2, cfg.Nodes[2].Count)
}

func TestParse_Storage(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantType      string
		wantHostPath  string
		wantMountPath string
	}{
		{
			name: "volume type",
			input: `kind: Cluster
storage:
  dataStorage:
    type: volume`,
			wantType: "volume",
		},
		{
			name: "hostPath type",
			input: `kind: Cluster
storage:
  dataStorage:
    type: hostPath
    hostPath: ./data
    mountPath: /data`,
			wantType:      "hostPath",
			wantHostPath:  "./data",
			wantMountPath: "/data",
		},
		{
			name:  "no storage section",
			input: "kind: Cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.input))
			require.NoError(t, err)
			if tt.wantType != "" {
				assert.Equal(t, tt.wantType, cfg.Storage.DataStorage.Type)
			}
			if tt.wantHostPath != "" {
				assert.Equal(t, tt.wantHostPath, cfg.Storage.DataStorage.HostPath)
			}
			if tt.wantMountPath != "" {
				assert.Equal(t, tt.wantMountPath, cfg.Storage.DataStorage.MountPath)
			}
		})
	}
}

func TestApplyDefaults_MinimalConfig(t *testing.T) {
	cfg := &Cluster{Kind: "Cluster", Name: "default"}
	cfg.ApplyDefaults()

	require.Len(t, cfg.Nodes, 2)
	assert.Equal(t, "controller", cfg.Nodes[0].Role)
	assert.Equal(t, "worker", cfg.Nodes[1].Role)
}

func TestApplyDefaults_InheritsGlobalDefaults(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Defaults: Defaults{
			Image:   "custom:latest",
			CPUs:    4,
			Memory:  "8g",
			TmpSize: "2g",
		},
		Nodes: []Node{
			{Role: "controller"},
			{Role: "worker"},
		},
	}
	cfg.ApplyDefaults()

	for _, n := range cfg.Nodes {
		assert.Equal(t, "custom:latest", n.Image, "node %s", n.Role)
		assert.Equal(t, 4, n.CPUs, "node %s", n.Role)
		assert.Equal(t, "8g", n.Memory, "node %s", n.Role)
		assert.Equal(t, "2g", n.TmpSize, "node %s", n.Role)
	}
}

func TestApplyDefaults_NodeOverridesGlobal(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Defaults: Defaults{
			Image:  "default:latest",
			CPUs:   2,
			Memory: "4g",
		},
		Nodes: []Node{
			{Role: "controller", CPUs: 8},
			{Role: "worker", Image: "special:latest"},
		},
	}
	cfg.ApplyDefaults()

	// controller: overrides CPUs, inherits rest
	assert.Equal(t, "default:latest", cfg.Nodes[0].Image)
	assert.Equal(t, 8, cfg.Nodes[0].CPUs)
	assert.Equal(t, "4g", cfg.Nodes[0].Memory)

	// worker: overrides image, inherits rest
	assert.Equal(t, "special:latest", cfg.Nodes[1].Image)
	assert.Equal(t, 2, cfg.Nodes[1].CPUs)
	assert.Equal(t, "4g", cfg.Nodes[1].Memory)
}

func TestApplyDefaults_BuiltinDefaults(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Nodes: []Node{
			{Role: "controller"},
			{Role: "worker"},
		},
	}
	cfg.ApplyDefaults()

	for _, n := range cfg.Nodes {
		assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:latest", n.Image, "node %s", n.Role)
		assert.Equal(t, 1, n.CPUs, "node %s", n.Role)
		assert.Equal(t, "512m", n.Memory, "node %s", n.Role)
		assert.Equal(t, "256m", n.TmpSize, "node %s", n.Role)
	}
}

func TestValidate_Valid(t *testing.T) {
	tests := []struct {
		name  string
		nodes []Node
	}{
		{
			name: "controller and worker",
			nodes: []Node{
				{Role: "controller"},
				{Role: "worker"},
			},
		},
		{
			name: "controller, submitter, and worker",
			nodes: []Node{
				{Role: "controller"},
				{Role: "submitter"},
				{Role: "worker"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{
				Kind:  "Cluster",
				Name:  "default",
				Nodes: tt.nodes,
			}
			require.NoError(t, cfg.Validate())
		})
	}
}

func TestValidate_RequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []Node
		wantErr string
	}{
		{
			name: "missing controller",
			nodes: []Node{
				{Role: "worker"},
			},
			wantErr: "exactly one controller",
		},
		{
			name: "multiple controllers",
			nodes: []Node{
				{Role: "controller"},
				{Role: "controller"},
				{Role: "worker"},
			},
			wantErr: "exactly one controller",
		},
		{
			name: "no worker",
			nodes: []Node{
				{Role: "controller"},
			},
			wantErr: "at least one worker",
		},
		{
			name:    "empty nodes",
			nodes:   []Node{},
			wantErr: "exactly one controller",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{
				Kind:  "Cluster",
				Name:  "default",
				Nodes: tt.nodes,
			}
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidate_Constraints(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []Node
		wantErr string
	}{
		{
			name: "multiple submitters",
			nodes: []Node{
				{Role: "controller"},
				{Role: "submitter"},
				{Role: "submitter"},
				{Role: "worker"},
			},
			wantErr: "at most one submitter",
		},
		{
			name: "count on controller",
			nodes: []Node{
				{Role: "controller", Count: 2},
				{Role: "worker"},
			},
			wantErr: "count is only valid for worker",
		},
		{
			name: "count on submitter",
			nodes: []Node{
				{Role: "controller"},
				{Role: "submitter", Count: 2},
				{Role: "worker"},
			},
			wantErr: "count is only valid for worker",
		},
		{
			name: "invalid role",
			nodes: []Node{
				{Role: "controller"},
				{Role: "compute"},
				{Role: "worker"},
			},
			wantErr: `invalid role "compute"`,
		},
		{
			name: "managed false on non-worker",
			nodes: []Node{
				{Role: "controller", Managed: boolPtr(false)},
				{Role: "worker"},
			},
			wantErr: "managed is only valid for worker",
		},
		{
			name: "managed true on non-worker",
			nodes: []Node{
				{Role: "controller", Managed: boolPtr(true)},
				{Role: "worker"},
			},
			wantErr: "managed is only valid for worker",
		},
		{
			name: "negative count",
			nodes: []Node{
				{Role: "controller"},
				{Role: "worker", Count: -1},
			},
			wantErr: "count must not be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{
				Kind:  "Cluster",
				Name:  "default",
				Nodes: tt.nodes,
			}
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "empty input",
			input:   "",
			wantErr: "empty configuration",
		},
		{
			name:    "invalid YAML",
			input:   "{{invalid",
			wantErr: "parsing config",
		},
		{
			name:    "missing kind",
			input:   "name: dev",
			wantErr: `kind must be "Cluster"`,
		},
		{
			name:    "wrong kind",
			input:   "kind: Pod",
			wantErr: `kind must be "Cluster"`,
		},
		{
			name: "node is neither string nor object",
			input: `kind: Cluster
nodes:
  - [1, 2]`,
			wantErr: "node must be a string",
		},
		{
			name: "node full form with invalid field type",
			input: `kind: Cluster
nodes:
  - role: controller
    cpus: not-a-number`,
			wantErr: "parsing config",
		},
		{
			name: "shorthand node with multiple keys",
			input: `kind: Cluster
nodes:
  - worker: 3
    memory: 4g`,
			wantErr: "shorthand node must have exactly one key",
		},
		{
			name: "shorthand node with non-integer count",
			input: `kind: Cluster
nodes:
  - worker: abc`,
			wantErr: "shorthand node count",
		},
		{
			name:    "unknown top-level field",
			input:   "kind: Cluster\nunknownField: value",
			wantErr: "unknown field",
		},
		{
			name: "unknown node field",
			input: `kind: Cluster
nodes:
  - role: controller
    unknownField: value`,
			wantErr: "unknown field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParse_ShorthandZeroCount(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - worker: 0`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 2)
	assert.Equal(t, "worker", cfg.Nodes[1].Role)
	assert.Equal(t, 0, cfg.Nodes[1].Count)
}

func TestParse_Pipeline(t *testing.T) {
	input := `kind: Cluster
name: test
defaults:
  image: custom:latest
  cpus: 4
nodes:
  - controller
  - submitter
  - worker: 3`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)

	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	assert.Equal(t, "test", cfg.Name)
	require.Len(t, cfg.Nodes, 3)
	assert.Equal(t, "controller", cfg.Nodes[0].Role)
	assert.Equal(t, "custom:latest", cfg.Nodes[0].Image)
	assert.Equal(t, 4, cfg.Nodes[0].CPUs)
	assert.Equal(t, "512m", cfg.Nodes[0].Memory)
	assert.Equal(t, "submitter", cfg.Nodes[1].Role)
	assert.Equal(t, "worker", cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
}

func TestParse_Realm(t *testing.T) {
	t.Run("omitted defaults to empty", func(t *testing.T) {
		cfg, err := Parse([]byte("kind: Cluster"))
		require.NoError(t, err)
		assert.Equal(t, "", cfg.Realm)
	})

	t.Run("explicit realm", func(t *testing.T) {
		cfg, err := Parse([]byte("kind: Cluster\nrealm: myrealm"))
		require.NoError(t, err)
		assert.Equal(t, "myrealm", cfg.Realm)
	})

	t.Run("realm with name", func(t *testing.T) {
		cfg, err := Parse([]byte("kind: Cluster\nname: dev\nrealm: testrealm"))
		require.NoError(t, err)
		assert.Equal(t, "dev", cfg.Name)
		assert.Equal(t, "testrealm", cfg.Realm)
	})
}
