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
  - role: compute
    count: 3
    cpus: 4
    memory: 8g
  - role: compute
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

	// managed compute
	assert.Equal(t, "compute", cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
	assert.Equal(t, 4, cfg.Nodes[2].CPUs)
	assert.Equal(t, "8g", cfg.Nodes[2].Memory)
	assert.Nil(t, cfg.Nodes[2].Managed)

	// unmanaged compute
	assert.Equal(t, "compute", cfg.Nodes[3].Role)
	assert.Equal(t, 2, cfg.Nodes[3].Count)
	require.NotNil(t, cfg.Nodes[3].Managed)
	assert.False(t, *cfg.Nodes[3].Managed)
}

func TestParse_NodesShorthand(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - submitter
  - compute: 3`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 3)

	assert.Equal(t, "controller", cfg.Nodes[0].Role)
	assert.Equal(t, 0, cfg.Nodes[0].Count)

	assert.Equal(t, "submitter", cfg.Nodes[1].Role)

	assert.Equal(t, "compute", cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
}

func TestParse_NodesMixed(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - role: compute
    count: 3
    cpus: 4
  - compute: 2`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 3)

	assert.Equal(t, "controller", cfg.Nodes[0].Role)

	assert.Equal(t, "compute", cfg.Nodes[1].Role)
	assert.Equal(t, 3, cfg.Nodes[1].Count)
	assert.Equal(t, 4, cfg.Nodes[1].CPUs)

	assert.Equal(t, "compute", cfg.Nodes[2].Role)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
