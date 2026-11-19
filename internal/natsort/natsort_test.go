// SPDX-License-Identifier: LGPL-3.0-or-later

package natsort_test

import (
	"sort"
	"strings"
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
			// Runs longer than 20 digits, beyond any fixed padding.
			name:  "long digit runs",
			input: []string{"n1" + strings.Repeat("0", 20), "n" + strings.Repeat("9", 20), "n" + strings.Repeat("9", 21), "n9", "n0"},
			want:  []string{"n0", "n9", "n" + strings.Repeat("9", 20), "n1" + strings.Repeat("0", 20), "n" + strings.Repeat("9", 21)},
		},
		{
			name:  "digits before letters, dashes and dots",
			input: []string{"a-b", "a.b", "ab", "a10", "a0"},
			want:  []string{"a-b", "a.b", "a0", "a10", "ab"},
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
	assert.Equal(t, natsort.Key("dev1x"), natsort.Key("dev001x"), "leading zeros are no part of the key")
	assert.Equal(t, natsort.Key("dev0"), natsort.Key("dev00"))
	assert.Equal(t, 1, natsort.Compare("n1"+strings.Repeat("0", 20), "n"+strings.Repeat("9", 20)), "10^20 after 10^20-1")
	assert.Equal(t, -1, natsort.Compare("n"+strings.Repeat("9", 9), "n1"+strings.Repeat("0", 9)), "a length of two digits")
}
