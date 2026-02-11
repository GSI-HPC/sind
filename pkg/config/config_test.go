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
