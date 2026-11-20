// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cmdexec_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/execopt"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamParentEnv makes the test binary a process that starts a stream
// and waits to be killed, in place of the tests. Its value says whether the
// stream dies with the process.
const streamParentEnv = "SIND_TEST_STREAM_PARENT"

// init runs the test binary as the process that TestStart_DiesWithParent
// kills: it starts a sleep as a stream, prints its PID and waits.
func init() {
	mode := os.Getenv(streamParentEnv)
	if mode == "" {
		return
	}
	ctx := context.Background()
	if mode == "tied" {
		ctx = execopt.WithDiesWithParent(ctx)
	}
	// The shell prints its PID and becomes the sleep, which reads no
	// standard input, as docker events.
	proc, err := cmdexec.Start(ctx, "sh", "-c", "echo $$; exec sleep 300")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	pid, err := bufio.NewReader(proc.Stdout).ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("stream %s", pid)
	time.Sleep(5 * time.Minute)
	os.Exit(1)
}

// runs reports whether the process pid still runs: a process that has
// ended and that nobody has waited for yet does not.
func runs(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the command name, which is in parentheses.
	text := string(stat)
	fields := strings.Fields(text[strings.LastIndexByte(text, ')')+1:])
	return len(fields) > 0 && fields[0] != "Z"
}

// startStreamParent starts the helper process and returns it with the PID
// of the stream that it started.
func startStreamParent(t *testing.T, mode string) (*exec.Cmd, int) {
	t.Helper()
	parent := exec.Command(os.Args[0])
	parent.Env = append(os.Environ(), streamParentEnv+"="+mode)
	out, err := parent.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, parent.Start())
	t.Cleanup(func() {
		_ = parent.Process.Kill()
		_ = parent.Wait()
	})
	lines := bufio.NewScanner(out)
	for lines.Scan() {
		if text, ok := strings.CutPrefix(lines.Text(), "stream "); ok {
			pid, err := strconv.Atoi(text)
			require.NoError(t, err)
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			return parent, pid
		}
	}
	require.FailNow(t, "the helper printed no PID of its stream")
	return nil, 0
}

// A stream under execopt.WithDiesWithParent ends when the process that
// started it is killed with SIGKILL, which lets it do nothing itself. A
// stream without the option stays: nothing tells a command that reads no
// standard input that its reader is gone.
func TestStart_DiesWithParent(t *testing.T) {
	t.Run("with the option", func(t *testing.T) {
		parent, stream := startStreamParent(t, "tied")
		require.True(t, runs(stream))
		require.NoError(t, parent.Process.Kill())
		assert.Eventually(t, func() bool { return !runs(stream) }, 10*time.Second, 20*time.Millisecond,
			"the stream still runs after its parent was killed")
	})
	t.Run("without the option", func(t *testing.T) {
		parent, stream := startStreamParent(t, "plain")
		require.NoError(t, parent.Process.Kill())
		_, _ = parent.Process.Wait()
		time.Sleep(500 * time.Millisecond)
		assert.True(t, runs(stream), "the stream ended with its parent")
	})
}

// A stream with the option starts and closes as one without it, and Close
// lets go of the thread that started it: the goroutine check of the
// package fails for a thread that is kept.
func TestStart_DiesWithParent_Close(t *testing.T) {
	ctx := execopt.WithDiesWithParent(t.Context())
	proc, err := cmdexec.Start(ctx, "sleep", "300")
	require.NoError(t, err)
	require.NoError(t, proc.Close())

	_, err = cmdexec.Start(ctx, "/nonexistent/sind-test-command")
	require.Error(t, err)

	merged, err := cmdexec.Start(execopt.WithStderrMerged(ctx), "sh", "-c", "echo out; echo err >&2")
	require.NoError(t, err)
	out, err := io.ReadAll(merged.Stdout)
	require.NoError(t, err)
	assert.Equal(t, "out\nerr\n", string(out))
	require.NoError(t, merged.Close())
}
