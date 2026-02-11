// SPDX-License-Identifier: LGPL-3.0-or-later

package nodeset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpand_SimpleName(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "single simple name",
			pattern: "compute-0",
			want:    []string{"compute-0"},
		},
		{
			name:    "single name without number",
			pattern: "controller",
			want:    []string{"controller"},
		},
		{
			name:    "name with dots",
			pattern: "compute-0.dev",
			want:    []string{"compute-0.dev"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.pattern)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExpand_MultipleSimple(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "comma-separated names",
			pattern: "a,b,c",
			want:    []string{"a", "b", "c"},
		},
		{
			name:    "two nodes",
			pattern: "controller,compute-0",
			want:    []string{"controller", "compute-0"},
		},
		{
			name:    "nodes with cluster suffix",
			pattern: "controller.dev,compute-0.dev",
			want:    []string{"controller.dev", "compute-0.dev"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.pattern)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExpand_Range(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "simple range",
			pattern: "node-[0-3]",
			want:    []string{"node-0", "node-1", "node-2", "node-3"},
		},
		{
			name:    "single element range",
			pattern: "node-[5-5]",
			want:    []string{"node-5"},
		},
		{
			name:    "range with prefix and suffix",
			pattern: "compute-[0-2].dev",
			want:    []string{"compute-0.dev", "compute-1.dev", "compute-2.dev"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.pattern)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExpand_RangeWithPadding(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "zero-padded range",
			pattern: "node-[00-03]",
			want:    []string{"node-00", "node-01", "node-02", "node-03"},
		},
		{
			name:    "three-digit padding",
			pattern: "node-[008-012]",
			want:    []string{"node-008", "node-009", "node-010", "node-011", "node-012"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.pattern)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
