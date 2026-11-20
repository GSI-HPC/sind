// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"bytes"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/execopt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputLimit(t *testing.T) {
	assert.IsType(t, &bytes.Buffer{}, newOutput(t.Context()))
	assert.Equal(t, &headTail{half: 32}, newOutput(execopt.WithOutputLimit(t.Context(), 64)))
	assert.Equal(t, &headTail{half: 1}, newOutput(execopt.WithOutputLimit(t.Context(), 1)))
}

func TestHeadTail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
		want   string
	}{
		{"nothing", nil, ""},
		{"within the head", []string{"ab"}, "ab"},
		{"head and tail", []string{"abcd", "ef"}, "abcdef"},
		{"one byte too many", []string{"abcdefg"}, "abc\n[1 bytes truncated]\nefg"},
		{"long", []string{"abc", strings.Repeat("x", 100), "yz"}, "abc\n[99 bytes truncated]\nxyz"},
		{"many writes", strings.Split("abcdefghijklmnopqrstuvwxyz", ""), "abc\n[20 bytes truncated]\nxyz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &headTail{half: 3}
			for _, w := range tc.writes {
				n, err := b.Write([]byte(w))
				require.NoError(t, err)
				assert.Equal(t, len(w), n)
			}
			assert.Equal(t, tc.want, b.String())
			assert.LessOrEqual(t, len(b.tail), 2*b.half)
		})
	}
}
