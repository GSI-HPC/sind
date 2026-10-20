// SPDX-License-Identifier: LGPL-3.0-or-later

package natsort_test

import (
	"sort"
	"testing"

	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/stretchr/testify/assert"
)

func TestKey(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "worker numeric suffix",
			input: []string{"worker-10", "worker-2", "worker-1", "worker-0", "worker-11"},
			want:  []string{"worker-0", "worker-1", "worker-2", "worker-10", "worker-11"},
		},
		{
			name:  "plain strings unchanged",
			input: []string{"controller", "beta", "alpha"},
			want:  []string{"alpha", "beta", "controller"},
		},
		{
			name:  "mixed digit runs",
			input: []string{"a1b20c3", "a1b3c30", "a1b3c3"},
			want:  []string{"a1b3c3", "a1b3c30", "a1b20c3"},
		},
		{
			name:  "empty string",
			input: []string{"b", "", "a"},
			want:  []string{"", "a", "b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := append([]string(nil), tc.input...)
			sort.Slice(got, func(i, j int) bool {
				return natsort.Key(got[i]) < natsort.Key(got[j])
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCompare(t *testing.T) {
	assert.Equal(t, -1, natsort.Compare("dev2", "dev10"))
	assert.Equal(t, 1, natsort.Compare("dev10", "dev2"))
	assert.Equal(t, 0, natsort.Compare("dev", "dev"))
	assert.Equal(t, -1, natsort.Compare("dev01", "dev1"), "leading zeros: byte order")
}
