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
			want:    []string{"compute-0", "controller"},
		},
		{
			name:    "nodes with cluster suffix",
			pattern: "controller.dev,compute-0.dev",
			want:    []string{"compute-0.dev", "controller.dev"},
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

func TestExpand_List(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "simple list",
			pattern: "node-[0,2,5]",
			want:    []string{"node-0", "node-2", "node-5"},
		},
		{
			name:    "single element list",
			pattern: "node-[3]",
			want:    []string{"node-3"},
		},
		{
			name:    "list with suffix",
			pattern: "compute-[0,3].dev",
			want:    []string{"compute-0.dev", "compute-3.dev"},
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

func TestExpand_MixedRangeList(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "range then single",
			pattern: "node-[0-2,5]",
			want:    []string{"node-0", "node-1", "node-2", "node-5"},
		},
		{
			name:    "single then range",
			pattern: "node-[0,3-5]",
			want:    []string{"node-0", "node-3", "node-4", "node-5"},
		},
		{
			name:    "multiple ranges",
			pattern: "node-[0-1,5-6]",
			want:    []string{"node-0", "node-1", "node-5", "node-6"},
		},
		{
			name:    "complex mix",
			pattern: "node-[0,2-4,7,9-10]",
			want:    []string{"node-0", "node-2", "node-3", "node-4", "node-7", "node-9", "node-10"},
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

func TestExpand_WithCluster(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "range with cluster suffix",
			pattern: "compute-[0-1].dev",
			want:    []string{"compute-0.dev", "compute-1.dev"},
		},
		{
			name:    "list with cluster suffix",
			pattern: "compute-[0,2].prod",
			want:    []string{"compute-0.prod", "compute-2.prod"},
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

func TestExpand_MultipleNodesets(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "two nodesets with ranges",
			pattern: "a-[0-1],b-[0-1]",
			want:    []string{"a-0", "a-1", "b-0", "b-1"},
		},
		{
			name:    "nodesets with different clusters",
			pattern: "compute-[0-1].dev,compute-[0-1].prod",
			want:    []string{"compute-0.dev", "compute-1.dev", "compute-0.prod", "compute-1.prod"},
		},
		{
			name:    "mixed simple and range",
			pattern: "controller,compute-[0-2]",
			want:    []string{"compute-0", "compute-1", "compute-2", "controller"},
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

func TestExpand_InvalidRange(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{
			name:    "reversed range",
			pattern: "node-[3-1]",
		},
		{
			name:    "invalid start number",
			pattern: "node-[abc-5]",
		},
		{
			name:    "invalid end number",
			pattern: "node-[0-xyz]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Expand(tt.pattern)
			assert.Error(t, err)
		})
	}
}

func TestExpand_UnclosedBracket(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{
			name:    "missing closing bracket",
			pattern: "node-[0-3",
		},
		{
			name:    "only opening bracket",
			pattern: "node-[",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Expand(tt.pattern)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "unclosed bracket")
		})
	}
}

func TestExpand_EmptyPattern(t *testing.T) {
	_, err := Expand("")
	assert.Error(t, err)
}

// Edge case tests for Step 1.6

func TestExpand_EmptyBracket(t *testing.T) {
	_, err := Expand("node-[]")
	assert.Error(t, err)
}

func TestExpand_NestedBrackets(t *testing.T) {
	_, err := Expand("node-[[0-1]]")
	assert.Error(t, err)
}

func TestExpand_MultipleBracketGroups(t *testing.T) {
	// Multiple bracket groups are not supported
	_, err := Expand("node-[0-1]-[2-3]")
	assert.Error(t, err)
}

func TestExpand_WhitespaceInBrackets(t *testing.T) {
	_, err := Expand("node-[ 0-3 ]")
	assert.Error(t, err)
}

func TestExpand_NoPrefix(t *testing.T) {
	got, err := Expand("[0-3]")
	require.NoError(t, err)
	assert.Equal(t, []string{"0", "1", "2", "3"}, got)
}

func TestExpand_TrailingComma(t *testing.T) {
	_, err := Expand("node-[0,1,]")
	assert.Error(t, err)
}

func TestExpand_LeadingComma(t *testing.T) {
	_, err := Expand("node-[,0,1]")
	assert.Error(t, err)
}

func TestExpand_MixedPaddingWidths(t *testing.T) {
	// When start has padding (00) but range exceeds it (100), padding from start is used
	got, err := Expand("node-[00-02]")
	require.NoError(t, err)
	assert.Equal(t, []string{"node-00", "node-01", "node-02"}, got)
}

func TestExpand_LargeRange(t *testing.T) {
	got, err := Expand("node-[0-999]")
	require.NoError(t, err)
	assert.Len(t, got, 1000)
	assert.Equal(t, "node-0", got[0])
	assert.Equal(t, "node-999", got[999])
}

func TestExpand_NegativeNumbers(t *testing.T) {
	// Negative numbers are not supported in nodeset notation
	// The "-" is interpreted as a range separator
	_, err := Expand("node-[-3--1]")
	assert.Error(t, err)
}

// Step 1.7: Deduplication and Sorting tests

func TestExpand_Deduplication(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "duplicate simple names",
			pattern: "node-0,node-0",
			want:    []string{"node-0"},
		},
		{
			name:    "multiple duplicates",
			pattern: "a,b,a,c,b",
			want:    []string{"a", "b", "c"},
		},
		{
			name:    "duplicate from range and explicit",
			pattern: "node-[0-2],node-1",
			want:    []string{"node-0", "node-1", "node-2"},
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

func TestExpand_Sorting(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "alphabetical sorting",
			pattern: "zulu,alpha,node-2,node-1",
			want:    []string{"alpha", "node-1", "node-2", "zulu"},
		},
		{
			name:    "numeric suffix sorting",
			pattern: "node-10,node-2,node-1",
			want:    []string{"node-1", "node-2", "node-10"},
		},
		{
			name:    "mixed prefixes",
			pattern: "compute-1,alpha-2,compute-0,alpha-1",
			want:    []string{"alpha-1", "alpha-2", "compute-0", "compute-1"},
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

func TestExpand_OverlappingRanges(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			name:    "overlapping ranges",
			pattern: "node-[0-2],node-[1-3]",
			want:    []string{"node-0", "node-1", "node-2", "node-3"},
		},
		{
			name:    "fully overlapping ranges",
			pattern: "node-[0-5],node-[2-3]",
			want:    []string{"node-0", "node-1", "node-2", "node-3", "node-4", "node-5"},
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
