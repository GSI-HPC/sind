// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build linux

package main

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_SendsNoTerminalQueries checks that sind, run on a terminal it
// controls and in its foreground, writes no queries to it: the logger asked
// the terminal for its colours (OSC 10 and 11, each followed by a cursor
// position report) as it was made, and a pseudo-terminal that nobody
// answers, as under script, expect or docker run -t, made every command
// wait 10 seconds and kept the queries in its log.
func TestRun_SendsNoTerminalQueries(t *testing.T) {
	ptmx, pts := openPTYPair(t)
	cmd, _ := child(t, "main", "version")
	// termenv asks no terminal under CI, or with TERM=dumb.
	cmd.Env = slices.DeleteFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "CI=") || strings.HasPrefix(kv, "TERM=")
	})
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pts, pts, pts
	// Setctty makes pts, the child's descriptor 0, the controlling
	// terminal of its new session, whose process group is then in the
	// foreground.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	require.NoError(t, cmd.Start())
	_ = pts.Close()

	out := make(chan string, 1)
	go func() {
		// Reading the master fails with EIO once the child has closed
		// the terminal.
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, ptmx)
		out <- buf.String()
	}()
	require.NoError(t, waitFor(t, cmd, 5*time.Second))
	got := <-out
	assert.Contains(t, got, "sind ")
	assert.NotContains(t, got, "\x1b]", "an OSC query")
	assert.NotContains(t, got, "\x1b[6n", "a cursor position query")
}
