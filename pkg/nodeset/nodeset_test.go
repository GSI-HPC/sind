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
