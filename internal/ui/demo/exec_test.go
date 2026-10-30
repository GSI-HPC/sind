// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"bufio"
	"context"
	"strconv"
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

func TestExec_FollowEndsWithItsContext(t *testing.T) {
	// The next line is an hour away, so only the context ends the stream.
	ctx, cancel := context.WithCancel(context.Background())
	p, err := testExec(time.Hour).Start(ctx, "docker", "logs", "--follow", "sind-dev-api")
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	sc := bufio.NewScanner(p.Stdout)
	for range head {
		require.True(t, sc.Scan())
	}
	cancel()
	assert.False(t, sc.Scan())
	assert.NoError(t, sc.Err())
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
	line := func(journal bool, short, daemon string, i int) string {
		return logLine(journal, short, daemon, messagesOf(daemon, Facts{}), i, at)
	}
	pid := strconv.Itoa(pidOf("slurmdbd"))
	assert.Equal(t, "Oct  2 09:05:07 db slurmdbd["+pid+"]: slurmdbd version 26.05.4 started", line(true, "db", "slurmdbd", 0))
	assert.Equal(t, "[09:05:07] slurmdbd: slurmdbd version 26.05.4 started", line(false, "db", "slurmdbd", 0))
	assert.Contains(t, line(true, "w", "slurmd", 0), "launch task StepId=4000.0")
	assert.Contains(t, line(true, "w", "chronyd", 1), "chronyd["+strconv.Itoa(pidOf("chronyd"))+"]: reloading configuration", "a unit with no texts of its own")
	for _, daemon := range []string{"slurmctld", "slurmd", "slurmdbd", "slurmrestd", "sackd"} {
		for i := range 8 {
			assert.NotContains(t, logLine(true, "x", daemon, messagesOf(daemon, Facts{Up: []string{"worker-0"}, Down: []string{"worker-1"}}), i, at), "%d")
		}
	}
}

func TestPidOf(t *testing.T) {
	assert.Equal(t, pidOf("slurmd"), pidOf("slurmd"), "one process ID for each daemon")
	assert.NotEqual(t, pidOf("slurmd"), pidOf("slurmctld"))
	lines := readLines(context.Background(), t, testExec(0), head, "exec", "sind-dev-worker-0", "journalctl", "--follow", "-n", "200")
	for _, l := range lines {
		assert.Contains(t, l, "slurmd["+strconv.Itoa(pidOf("slurmd"))+"]: ")
	}
}

func TestControllerMessages(t *testing.T) {
	all := strings.Join(controllerMessages(Facts{Up: []string{"worker-0", "worker-1"}, Down: []string{"worker-3", "worker-4"}}), "\n")
	assert.Contains(t, all, "in all on worker-0", "the partition of the cluster")
	assert.Contains(t, all, "registration complete for worker-1")
	assert.Contains(t, all, "Nodes worker-[3-4] not responding", "only the nodes that are down")
	assert.NotContains(t, all, "debug")

	none := strings.Join(controllerMessages(Facts{Up: []string{"worker-0"}}), "\n")
	assert.NotContains(t, none, "not responding", "no node is down")
	idle := strings.Join(controllerMessages(Facts{Down: []string{"worker-0"}}), "\n")
	assert.NotContains(t, idle, "Started JobId", "no node to start on")
	assert.Contains(t, idle, "worker-0 not responding")
}

func TestExec_ControllerLogFollowsTheFacts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := testExec(0)
	e.Facts = func(string) Facts {
		return Facts{Up: []string{"worker-1"}, Down: []string{"worker-4"}}
	}
	lines := readLines(ctx, t, e, head, cluster.ServiceLogArgs("sind", "controller", "dev", "slurmctld", cluster.LogOptions{Follow: true})...)
	all := strings.Join(lines, "\n")
	assert.Contains(t, all, "Nodes worker-4 not responding")
	assert.NotContains(t, all, "worker-3")
}

func TestExec_JournalOfAFailedUnit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	at := t0.Add(-13 * time.Minute)
	e := testExec(time.Millisecond)
	e.Failed = func(container, unit string) (time.Time, bool) {
		return at, container == "sind-dev-worker-4" && unit == "slurmd"
	}
	args := cluster.ServiceLogArgs("sind", "worker-4", "dev", "slurmd", cluster.LogOptions{Follow: true})
	p, err := e.Start(ctx, "docker", args...)
	require.NoError(t, err)
	defer func() { _ = p.Close() }()
	sc := bufio.NewScanner(p.Stdout)
	var lines []string
	for len(lines) < head+2 && sc.Scan() {
		lines = append(lines, sc.Text())
	}
	require.Len(t, lines, head+2)
	assert.Equal(t, "Oct 14 09:17:00 worker-4 systemd[1]: slurmd.service: Failed with result 'exit-code'.", lines[head+1])
	assert.Contains(t, lines[head-1], "fatal: Unable to register")
	// And then nothing: the unit is down.
	cancel()
	assert.False(t, sc.Scan())

	// Other units, and the container's log, go on as ever.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	other := readLines(ctx, t, e, head+3, cluster.ServiceLogArgs("sind", "worker-4", "dev", "munge", cluster.LogOptions{Follow: true})...)
	assert.NotContains(t, strings.Join(other, "\n"), "FAILURE")
	other = readLines(ctx, t, e, head+3, cluster.ContainerLogArgs("sind", "worker-4", "dev", cluster.LogOptions{Follow: true})...)
	assert.NotContains(t, strings.Join(other, "\n"), "FAILURE")
}

func TestExec_JournalOfAFailedUnit_ReaderGone(t *testing.T) {
	e := testExec(0)
	e.Failed = func(string, string) (time.Time, bool) { return t0, true }
	for _, n := range []int{0, head - 2} {
		p, err := e.Start(context.Background(), "docker", cluster.ServiceLogArgs("sind", "worker-4", "dev", "slurmd", cluster.LogOptions{})...)
		require.NoError(t, err)
		sc := bufio.NewScanner(p.Stdout)
		for range n {
			require.True(t, sc.Scan())
		}
		require.NoError(t, p.Close())
	}
	time.Sleep(20 * time.Millisecond)
}

func TestFailureLines(t *testing.T) {
	assert.Contains(t, failureLines("slurmd")[1], "Unable to contact slurm controller")
	assert.Equal(t, "fatal: sshd cannot listen", failureLines("sshd")[2])
	j := failureJournal("sshd", t0)
	require.Len(t, j, 5)
	assert.True(t, j[0].At.Before(j[1].At))
	assert.Equal(t, t0, j[4].At)
}

func TestExec_StoppedContainer(t *testing.T) {
	at := t0.Add(-43 * time.Minute)
	e := testExec(time.Millisecond)
	e.Stopped = func(container string) (time.Time, bool) { return at, container == "sind-legacy-worker-1" }
	ctx := context.Background()
	p, err := e.Start(ctx, "docker", cluster.ContainerLogArgs("sind", "worker-1", "legacy", cluster.LogOptions{Follow: true})...)
	require.NoError(t, err)
	sc := bufio.NewScanner(p.Stdout)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	require.Len(t, lines, head, "the output ends where the container stopped")
	assert.Contains(t, lines[head-1], "[08:47:00]")
	require.NoError(t, p.Close())

	p, err = e.Start(ctx, "docker", cluster.ServiceLogArgs("sind", "worker-1", "legacy", "slurmd", cluster.LogOptions{Follow: true})...)
	require.NoError(t, err)
	sc = bufio.NewScanner(p.Stdout)
	require.True(t, sc.Scan())
	assert.Equal(t, "Error response from daemon: container sind-legacy-worker-1 is not running", sc.Text())
	assert.False(t, sc.Scan())
	require.NoError(t, p.Close())

	// A reader that goes ends it too.
	for _, args := range [][]string{
		cluster.ContainerLogArgs("sind", "worker-1", "legacy", cluster.LogOptions{}),
		cluster.ServiceLogArgs("sind", "worker-1", "legacy", "slurmd", cluster.LogOptions{}),
	} {
		p, err = e.Start(ctx, "docker", args...)
		require.NoError(t, err)
		require.NoError(t, p.Close())
	}
	time.Sleep(20 * time.Millisecond)
}
