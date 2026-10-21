// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/pkg/cluster"
)

func testExec(every time.Duration) *Exec {
	return &Exec{Now: func() time.Time { return t0 }, Every: every}
}

// readLines reads n lines of a stream.
func readLines(ctx context.Context, t *testing.T, e *Exec, n int, args ...string) []string {
	t.Helper()
	p, err := e.Start(ctx, "docker", args...)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	sc := bufio.NewScanner(p.Stdout)
	var out []string
	for len(out) < n && sc.Scan() {
		out = append(out, sc.Text())
	}
	require.Len(t, out, n)
	return out
}

func TestExec_NoCommands(t *testing.T) {
	e := testExec(0)
	_, _, err := e.Run(context.Background(), "docker", "ps")
	assert.ErrorIs(t, err, errNoCommands)
	_, _, err = e.RunWithStdin(context.Background(), strings.NewReader(""), "docker", "ps")
	assert.ErrorIs(t, err, errNoCommands)
	for _, c := range [][]string{{"ls"}, {"docker"}, {"docker", "ps", "-a"}} {
		_, err = e.Start(context.Background(), c[0], c[1:]...)
		assert.ErrorIs(t, err, errNoCommands, "%v", c)
	}
}

func TestExec_DockerLogs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := cluster.ContainerLogArgs("sind", "worker-1", "dev", cluster.LogOptions{Follow: true})
	lines := readLines(ctx, t, testExec(0), head, args...)
	assert.Contains(t, lines[0], "[09:27:00]")
	assert.Contains(t, lines[0], "slurmd: ")
	assert.Contains(t, lines[head-1], "[09:29:57]", "three seconds apart, up to now")
}

func TestExec_Journal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := cluster.ServiceLogArgs("sind", "controller", "dev", "slurmctld", cluster.LogOptions{Follow: true})
	lines := readLines(ctx, t, testExec(0), 3, args...)
	assert.Regexp(t, `^Oct 14 09:2\d:\d\d controller slurmctld\[\d+\]: `, lines[0])

	all := readLines(ctx, t, testExec(0), 1, "exec", "sind-dev-worker-0", "journalctl", "--follow", "-n", "200")
	assert.Contains(t, all[0], " worker-0 slurmd[")
}

func TestExec_StillSendsNoMore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := testExec(0).Start(ctx, "docker", "logs", "--follow", "sind-dev-db")
	require.NoError(t, err)
	sc := bufio.NewScanner(p.Stdout)
	for range head {
		require.True(t, sc.Scan())
	}
	cancel() // the stream stays open until its context ends
	assert.False(t, sc.Scan(), "then it ends")
	require.NoError(t, p.Close())
}

func TestExec_Follows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines := readLines(ctx, t, testExec(time.Millisecond), head+5, "logs", "--follow", "sind-dev-api")
	assert.Len(t, lines, head+5)
}

func TestExec_ReaderGone(t *testing.T) {
	// A reader that goes ends the stream, in the first lines and later.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := testExec(time.Millisecond).Start(ctx, "docker", "logs", "sind-dev-api")
	require.NoError(t, err)
	require.NoError(t, p.Close())

	p, err = testExec(time.Millisecond).Start(ctx, "docker", "logs", "sind-dev-api")
	require.NoError(t, err)
	sc := bufio.NewScanner(p.Stdout)
	for range head + 2 {
		require.True(t, sc.Scan())
	}
	require.NoError(t, p.Close())
	time.Sleep(20 * time.Millisecond) // the writer sees the closed pipe
}

func TestShortName(t *testing.T) {
	for in, want := range map[string]string{
		"sind-dev-worker-12":         "worker-12",
		"sind-dev-controller-backup": "controller-backup",
		"sind-dev-controller":        "controller",
		"sind-dev-db":                "db",
		"db":                         "db",
	} {
		assert.Equal(t, want, shortName(in), in)
	}
}

func TestDaemonOf(t *testing.T) {
	for in, want := range map[string]string{
		"controller": "slurmctld", "controller-backup": "slurmctld", "db": "slurmdbd", "api": "slurmrestd",
		"submitter": "sackd", "worker-3": "slurmd",
	} {
		assert.Equal(t, want, daemonOf(in), in)
	}
}

func TestLogLine(t *testing.T) {
	at := time.Date(2026, 10, 2, 9, 5, 7, 0, time.UTC)
	assert.Equal(t, "Oct  2 09:05:07 db slurmdbd[100]: slurmdbd version 26.05.4 started", logLine(true, "db", "slurmdbd", 0, at))
	assert.Equal(t, "[09:05:07] slurmdbd: slurmdbd version 26.05.4 started", logLine(false, "db", "slurmdbd", 0, at))
	assert.Contains(t, logLine(true, "w", "slurmd", 0, at), "launch task StepId=4000.0")
	assert.Contains(t, logLine(true, "w", "chronyd", 1, at), "chronyd[101]: reloading configuration", "a unit with no texts of its own")
	for daemon := range messages {
		for i := range 8 {
			assert.NotContains(t, logLine(true, "x", daemon, i, at), "%d")
		}
	}
}
