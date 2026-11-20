// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec_test

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcess_ReadStdout(t *testing.T) {
	pr, pw := io.Pipe()
	proc := &cmdexec.Process{Stdout: pr}

	go func() {
		_, _ = pw.Write([]byte("hello\n"))
		_ = pw.Close()
	}()

	scanner := bufio.NewScanner(proc.Stdout)
	require.True(t, scanner.Scan(), "expected output line")
	assert.Equal(t, "hello", scanner.Text())
	_ = proc.Close()
}

func TestProcess_Close_NilCmd(t *testing.T) {
	pr, pw := io.Pipe()
	proc := &cmdexec.Process{Stdout: pr}
	_ = pw.Close()
	err := proc.Close()
	assert.NoError(t, err)
}

// exitingStdout is the Stdout of a Process that a custom Executor started:
// its Close ends the command and reports how it exited.
type exitingStdout struct {
	io.Reader
	err error
}

func (s *exitingStdout) Close() error { return s.err }

// TestProcess_Close_NilCmdReportsExit checks that a Process without a
// command returns what its Stdout's Close returns, so that a custom
// Executor can report a stream that failed with NewExitError.
func TestProcess_Close_NilCmdReportsExit(t *testing.T) {
	proc := &cmdexec.Process{Stdout: &exitingStdout{Reader: strings.NewReader(""), err: cmdexec.NewExitError(2, "boom\n")}}
	err := proc.Close()
	var exitErr *cmdexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.Equal(t, "boom\n", exitErr.Stderr)
}
