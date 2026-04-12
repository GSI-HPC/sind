// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec_test

import (
	"bufio"
	"io"
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
