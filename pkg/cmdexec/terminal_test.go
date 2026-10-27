// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminal(t *testing.T) {
	assert.False(t, Terminal(t.Context()))
	assert.True(t, Terminal(WithTerminal(t.Context())))
}

func TestTerminalOpened(t *testing.T) {
	assert.Nil(t, TerminalOpened(t.Context()))
	called := false
	TerminalOpened(WithTerminalOpened(t.Context(), func() { called = true }))()
	assert.True(t, called)
}

func TestTerminalLines(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{
			name: "plain lines",
			in:   []string{"latest: Pulling from library/busybox\r\n", "Digest: sha256:abc\n"},
			want: "latest: Pulling from library/busybox\nDigest: sha256:abc\n",
		},
		{
			// docker pull redraws the line of a layer in place: up to it,
			// erase it, write it, back down.
			name: "redrawn lines",
			in: []string{
				"\n\x1b[2K\r2fbc8ae9b1d6: Pulling fs layer \r\x1b[1B",
				"\x1b[1A\x1b[2K\r2fbc8ae9b1d6: Downloading  1.05MB/45.6MB 3s\r\x1b[1B",
				"\x1b[1A\x1b[2K\r2fbc8ae9b1d6: Pull complete \r\x1b[1B",
			},
			want: "2fbc8ae9b1d6: Pulling fs layer\n2fbc8ae9b1d6: Downloading  1.05MB/45.6MB 3s\n2fbc8ae9b1d6: Pull complete\n",
		},
		{
			name: "sequences cut between writes",
			in:   []string{"a: x\x1b", "[1", "0A", "b: y\x1b", "(B\r"},
			want: "a: x\nb: y\n",
		},
		{
			name: "control characters and tabs",
			in:   []string{"a\x07\x7fb\tc\n"},
			want: "ab\tc\n",
		},
		{
			name: "erase in a line does not end it",
			in:   []string{"a\x1b[Kb\n"},
			want: "ab\n",
		},
		{
			name: "blank lines left out",
			in:   []string{"\r\n  \r\n\x1b[2K\r"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tl := NewTerminalLines(&buf)
			for _, p := range tt.in {
				n, err := tl.Write([]byte(p))
				require.NoError(t, err)
				assert.Equal(t, len(p), n)
			}
			assert.Equal(t, tt.want, buf.String())
		})
	}
}

func TestTerminalLinesFlush(t *testing.T) {
	var buf bytes.Buffer
	tl := NewTerminalLines(&buf)
	require.NoError(t, tl.Flush())
	_, err := tl.Write([]byte("Status: Downloaded newer image "))
	require.NoError(t, err)
	assert.Empty(t, buf.String())
	require.NoError(t, tl.Flush())
	assert.Equal(t, "Status: Downloaded newer image\n", buf.String())
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestTerminalLinesWriteFails(t *testing.T) {
	tl := NewTerminalLines(failWriter{})
	n, err := tl.Write([]byte("a\n"))
	require.EqualError(t, err, "closed")
	assert.Zero(t, n)
	_, err = tl.Write([]byte("b"))
	require.NoError(t, err)
	require.EqualError(t, tl.Flush(), "closed")
}
