// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build linux

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// openPTY returns the terminal end of a new pseudo-terminal, and skips the
// test where the system gives none.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close() })
	fd := int(ptmx.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlocking the pseudo-terminal: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("numbering the pseudo-terminal: %v", err)
	}
	pts, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("opening the pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = pts.Close() })
	return pts
}

// sshDockerArgs runs sind with args, stdin and stdout, and returns the
// arguments of the docker command it ran.
func sshDockerArgs(t *testing.T, stdin io.Reader, stdout io.Writer, args ...string) []string {
	t.Helper()
	fakeDockerOnPath(t, "0")
	file := filepath.Join(t.TempDir(), "args")
	t.Setenv(fakeDockerArgsEnv, file)
	cmd := NewRootCommand()
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)

	require.NoError(t, cmd.Execute())
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	return strings.Split(string(data), "\n")
}

// TestSSH_TTYOnlyWithTerminalsAtBothEnds checks that ssh passes -t to docker
// exec only when stdin and stdout are terminals. It passed -t whenever stdin
// was one, so output captured or redirected in an interactive shell came
// through a pseudo-terminal, with CRLF line endings and the remote stderr.
func TestSSH_TTYOnlyWithTerminalsAtBothEnds(t *testing.T) {
	tty := openPTY(t)
	withTTY := []string{"exec", "-i", "-t", "sind-ssh", "ssh", "worker-0.default.sind.sind", "hostname"}
	withoutTTY := []string{"exec", "-i", "sind-ssh", "ssh", "worker-0.default.sind.sind", "hostname"}
	for name, tc := range map[string]struct {
		stdin  io.Reader
		stdout io.Writer
		want   []string
	}{
		"terminal at both ends": {tty, tty, withTTY},
		"output captured":       {tty, new(bytes.Buffer), withoutTTY},
		"output to a file":      {tty, noStdout(t), withoutTTY},
		"input piped":           {strings.NewReader(""), tty, withoutTTY},
	} {
		t.Run(name, func(t *testing.T) {
			got := sshDockerArgs(t, tc.stdin, tc.stdout, "ssh", "worker-0", "--", "hostname")
			assert.Equal(t, tc.want, got)
		})
	}
}

// noStdout returns a file that is not a terminal, to stand for redirected
// output.
func noStdout(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestStdinIsTTY_Terminal(t *testing.T) {
	assert.True(t, stdinIsTTY(openPTY(t)))
}

func TestStdoutIsTTY(t *testing.T) {
	assert.True(t, stdoutIsTTY(openPTY(t)))
	assert.False(t, stdoutIsTTY(new(bytes.Buffer)))
	assert.False(t, stdoutIsTTY(noStdout(t)))
}
