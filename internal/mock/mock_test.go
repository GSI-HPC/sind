// SPDX-License-Identifier: LGPL-3.0-or-later

package mock

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_FIFO(t *testing.T) {
	var m Executor
	m.AddResult("out1", "err1", nil)
	m.AddResult("out2", "", fmt.Errorf("fail"))

	stdout, stderr, err := m.Run(t.Context(), "docker", "ps")
	assert.Equal(t, "out1", stdout)
	assert.Equal(t, "err1", stderr)
	require.NoError(t, err)

	stdout, stderr, err = m.Run(t.Context(), "docker", "inspect", "c1")
	assert.Equal(t, "out2", stdout)
	assert.Equal(t, "", stderr)
	require.Error(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"ps"}, m.Calls[0].Args)
	assert.Equal(t, []string{"inspect", "c1"}, m.Calls[1].Args)
}

func TestExecutor_OnCall(t *testing.T) {
	m := Executor{
		OnCall: func(args []string, _ string) Result {
			if args[0] == "version" {
				return Result{Stdout: "28.0"}
			}
			return Result{Err: fmt.Errorf("unknown")}
		},
	}

	stdout, _, err := m.Run(t.Context(), "docker", "version")
	assert.Equal(t, "28.0", stdout)
	require.NoError(t, err)

	_, _, err = m.Run(t.Context(), "docker", "other")
	require.Error(t, err)
}

func TestExecutor_Exhausted(t *testing.T) {
	var m Executor
	m.AddResult("ok", "", nil)
	_, _, _ = m.Run(t.Context(), "docker", "ps")

	_, _, err := m.Run(t.Context(), "docker", "inspect")
	require.ErrorContains(t, err, "unexpected call")
}

func TestExecutor_RunWithStdin(t *testing.T) {
	var m Executor
	m.AddResult("ok", "", nil)

	stdout, _, err := m.RunWithStdin(t.Context(), strings.NewReader("hello"), "docker", "cp", "-")
	require.NoError(t, err)
	assert.Equal(t, "ok", stdout)
	assert.Equal(t, "hello", m.Calls[0].Stdin)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestExecutor_RunWithStdin_ReadError(t *testing.T) {
	var m Executor
	m.AddResult("ok", "", nil)

	_, _, err := m.RunWithStdin(t.Context(), errReader{}, "docker", "cp")
	require.ErrorContains(t, err, "reading stdin")
}

func TestRecorder_Unit(t *testing.T) {
	rec := NewRecorder()

	assert.False(t, rec.IsIntegration())

	rec.AddResult("v1", "", nil)
	stdout, _, err := rec.Run(t.Context(), "docker", "version")
	require.NoError(t, err)
	assert.Equal(t, "v1", stdout)

	calls := rec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "docker", calls[0].Name)
}

func TestRecorder_Integration(t *testing.T) {
	rec := NewIntegrationRecorder()

	assert.True(t, rec.IsIntegration())

	// AddResult and SetOnCall are no-ops in integration mode.
	rec.AddResult("ignored", "", nil)
	rec.SetOnCall(func([]string, string) Result { return Result{} })
}

func TestRecorder_SetOnCall(t *testing.T) {
	rec := NewRecorder()
	rec.SetOnCall(func(args []string, _ string) Result {
		return Result{Stdout: args[0]}
	})

	stdout, _, err := rec.Run(t.Context(), "docker", "ps")
	require.NoError(t, err)
	assert.Equal(t, "ps", stdout)
}

func TestRecordingExecutor_Dump(t *testing.T) {
	rec := NewRecorder()
	rec.AddResult("out\n", "err\n", nil)
	rec.AddResult("", "", fmt.Errorf("boom"))

	_, _, _ = rec.Run(t.Context(), "docker", "ps")
	_, _, _ = rec.Run(t.Context(), "docker", "fail")

	dump := rec.Dump()
	assert.Contains(t, dump, "[0] docker ps")
	assert.Contains(t, dump, "stdout: out")
	assert.Contains(t, dump, "stderr: err")
	assert.Contains(t, dump, "[1] docker fail")
	assert.Contains(t, dump, "err: boom")
}

func TestRecordingExecutor_Dump_NoTrailingNewline(t *testing.T) {
	rec := NewRecorder()
	rec.AddResult("out", "err", nil)

	_, _, _ = rec.Run(t.Context(), "cmd", "a")

	dump := rec.Dump()
	assert.Contains(t, dump, "stdout: out\n")
	assert.Contains(t, dump, "stderr: err\n")
}

func TestRecordingExecutor_RunWithStdin(t *testing.T) {
	rec := NewRecorder()
	rec.AddResult("ok", "", nil)

	_, _, err := rec.RunWithStdin(t.Context(), strings.NewReader("data"), "docker", "cp")
	require.NoError(t, err)

	calls := rec.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "ok", calls[0].Stdout)
}

func TestExecutor_Start_OnStart(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	m := Executor{
		OnStart: func(args []string) StreamResult {
			if args[0] == "events" {
				return StreamResult{Reader: pr}
			}
			return StreamResult{Err: fmt.Errorf("unknown")}
		},
	}

	proc, err := m.Start(t.Context(), "docker", "events")
	require.NoError(t, err)
	assert.NotNil(t, proc)
	_ = proc.Close()

	_, err = m.Start(t.Context(), "docker", "other")
	require.Error(t, err)
}

func TestExecutor_Start_NoOnStart(t *testing.T) {
	var m Executor
	_, err := m.Start(t.Context(), "docker", "events")
	require.ErrorContains(t, err, "unexpected Start call")
}

func TestPipes(t *testing.T) {
	var p Pipes
	m := Executor{OnStart: p.OnStart}

	proc, err := m.Start(t.Context(), "docker", "events")
	require.NoError(t, err)

	// io.Pipe is unbuffered — write must happen concurrently with read.
	go p.Write(0, "hello\n")

	buf := make([]byte, 6)
	_, err = io.ReadFull(proc.Stdout, buf)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(buf))

	assert.Equal(t, 1, p.Len())

	p.CloseAll()
	_ = proc.Close()
}

func TestPipes_CloseWithError(t *testing.T) {
	var p Pipes
	m := Executor{OnStart: p.OnStart}

	proc, err := m.Start(t.Context(), "docker", "events")
	require.NoError(t, err)

	p.CloseWithError(0, fmt.Errorf("connection reset"))

	buf := make([]byte, 1)
	_, err = proc.Stdout.Read(buf)
	require.ErrorContains(t, err, "connection reset")
	_ = proc.Close()
}

func TestRecordingExecutor_Start(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	rec := NewRecorder()
	rec.mock.OnStart = func(_ []string) StreamResult {
		return StreamResult{Reader: pr}
	}

	proc, err := rec.Start(t.Context(), "docker", "events")
	require.NoError(t, err)
	assert.NotNil(t, proc)
	_ = proc.Close()
}

// TestExecutor_ExitErrorShape checks that a queued *exec.ExitError comes back
// the way the real executor returns it: as a *cmdexec.ExitError carrying the
// result's stderr, in both dispatch modes.
func TestExecutor_ExitErrorShape(t *testing.T) {
	runErr := exec.Command("sh", "-c", "exit 1").Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, runErr, &exitErr)
	const stderr = "Error response from daemon: No such container: x\n"

	var fifo Executor
	fifo.AddResult("", stderr, exitErr)
	var onCall Executor
	onCall.OnCall = func([]string, string) Result { return Result{Stderr: stderr, Err: exitErr} }

	for name, m := range map[string]*Executor{"fifo": &fifo, "OnCall": &onCall} {
		t.Run(name, func(t *testing.T) {
			_, _, err := m.Run(t.Context(), "docker", "inspect", "x")
			var got *cmdexec.ExitError
			require.ErrorAs(t, err, &got)
			assert.Equal(t, stderr, got.Stderr)
			assert.Equal(t, 1, got.ExitCode())
		})
	}
}

// A command run under a line parser (cmdexec.WithLineParser) has the lines
// of its result parsed, as the real executor does while it runs: those of
// standard output, unless that is data, then those of standard error, each
// without its line break; a last line that has none is left out. Without a
// parser nothing is parsed, and the result is as it was.
func TestExecutor_ParsesLines(t *testing.T) {
	type line struct {
		stream progress.Stream
		text   string
	}
	result := Result{Stdout: "a\r\nb\nc", Stderr: "d\n"}
	for _, tc := range []struct {
		name string
		ctx  func(context.Context) context.Context
		want []line
	}{
		{"both", func(ctx context.Context) context.Context { return ctx },
			[]line{{progress.Stdout, "a"}, {progress.Stdout, "b"}, {progress.Stderr, "d"}}},
		{"stdout is data", cmdexec.WithStdoutAsData, []line{{progress.Stderr, "d"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []line
			ctx := cmdexec.WithLineParser(tc.ctx(t.Context()), func(st progress.Stream, text string) { got = append(got, line{st, text}) })
			m := Executor{OnCall: func([]string, string) Result { return result }}

			stdout, stderr, err := m.Run(ctx, "docker", "pull", "img")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, result.Stdout, stdout)
			assert.Equal(t, result.Stderr, stderr)

			got = nil
			_, _, err = m.RunWithStdin(ctx, strings.NewReader("in"), "docker", "pull", "img")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, _, err := (&Executor{OnCall: func([]string, string) Result { return result }}).Run(t.Context(), "docker", "ps")
	require.NoError(t, err)
}
