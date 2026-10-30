// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/internal/mock"
	sindlog "github.com/GSI-HPC/sind/pkg/log"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger_ErrorOnly(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 0)

	ctx := context.Background()
	l.InfoContext(ctx, "should not appear")
	assert.Empty(t, buf.String())

	l.ErrorContext(ctx, "should appear")
	assert.Contains(t, buf.String(), "should appear")
}

func TestNewLogger_Timestamps(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, 0).ErrorContext(t.Context(), "failed")
	assert.Regexp(t, `^\d\d:\d\d:\d\d\.\d{3} ERRO failed\n$`, buf.String())

	t.Setenv(envvar.LogTimestamps, "false")
	buf.Reset()
	newLogger(&buf, 0).ErrorContext(t.Context(), "failed")
	assert.Equal(t, "ERRO failed\n", buf.String())
}

func TestNewLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 1)

	ctx := context.Background()
	l.InfoContext(ctx, "visible")
	l.DebugContext(ctx, "hidden")

	assert.Contains(t, buf.String(), "visible")
	assert.NotContains(t, buf.String(), "hidden")
}

func TestNewLogger_Debug(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 2)

	ctx := context.Background()
	l.DebugContext(ctx, "visible")
	l.Log(ctx, sindlog.LevelTrace, "hidden")

	assert.Contains(t, buf.String(), "visible")
	assert.NotContains(t, buf.String(), "hidden")
}

func TestNewLogger_Trace(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 3)

	ctx := context.Background()
	l.Log(ctx, sindlog.LevelTrace, "trace msg")

	assert.Contains(t, buf.String(), "trace msg")
	assert.Contains(t, buf.String(), "TRAC")
}

func TestNewLogger_HighVerbosityClamped(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 10) // more than 3

	ctx := context.Background()
	l.Log(ctx, sindlog.LevelTrace, "deep trace")
	assert.Contains(t, buf.String(), "deep trace")
}

// TestNewLogger_Colours checks that the logger draws in the colours its
// writer shows, as colourProfile tells, though it hides from termenv what
// the writer is (terminalQuiet).
func TestNewLogger_Colours(t *testing.T) {
	saved := colourProfile
	t.Cleanup(func() { colourProfile = saved })
	for _, tc := range []struct {
		profile termenv.Profile
		escapes bool
	}{
		{termenv.ANSI256, true},
		{termenv.Ascii, false},
	} {
		t.Run(tc.profile.Name(), func(t *testing.T) {
			var buf bytes.Buffer
			colourProfile = func(w io.Writer) termenv.Profile {
				assert.Same(t, &buf, w)
				return tc.profile
			}
			newLogger(&buf, 0).ErrorContext(context.Background(), "boom")
			assert.Contains(t, buf.String(), "boom")
			assert.Equal(t, tc.escapes, strings.Contains(buf.String(), "\x1b["))
		})
	}
}

func TestVerboseFlag_AcceptedByRoot(t *testing.T) {
	// Verify -v flag is wired to root and doesn't cause "unknown flag".
	stdout, _, err := executeCommand("-v", "--help")
	assert.NoError(t, err)
	assert.Contains(t, stdout, "sind")
}

func TestVerboseFlag_InjectsLogger(t *testing.T) {
	mock := &mock.Executor{}
	// get clusters with empty result
	mock.AddResult("", "", nil)

	cmd := NewRootCommand()
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "get", "clusters"})

	client := docker.NewClient(mock)
	cmd.SetContext(withClient(context.Background(), client))

	err := cmd.Execute()
	assert.NoError(t, err)
	// Logger was injected — once operations emit logs, they'll appear on stderr.
	// For now we just verify the flag is accepted and execution succeeds.
}

// TestVerbosity covers SIND_VERBOSE: the verbosity without -v, which wins
// over it, and a value that is no count, which is ignored with a note.
func TestVerbosity(t *testing.T) {
	note := "sind: SIND_VERBOSE is %q; it takes a number, such as 2 for -vv; it is ignored"
	for _, tc := range []struct {
		env  string
		args []string
		want int
		note string
	}{
		{"", nil, 0, ""},
		{"0", nil, 0, ""},
		{"2", nil, 2, ""},
		{"2", []string{"-v"}, 1, ""},
		{"lots", nil, 0, fmt.Sprintf(note, "lots")},
		{"-1", nil, 0, fmt.Sprintf(note, "-1")},
		{"-vv", nil, 0, fmt.Sprintf(note, "-vv")},
		{"lots", []string{"-vv"}, 2, ""},
	} {
		t.Setenv("SIND_VERBOSE", tc.env)
		root := NewRootCommand()
		require.NoError(t, root.ParseFlags(tc.args))
		v, got := verbosity(root)
		assert.Equal(t, tc.want, v, "SIND_VERBOSE=%s %v", tc.env, tc.args)
		assert.Equal(t, tc.note, got, "SIND_VERBOSE=%s %v", tc.env, tc.args)
	}
}

// TestVerbosity_FromEnv checks that SIND_VERBOSE sets the level of a
// command's logger, and that the command writes the note of an invalid one
// once, and goes on.
func TestVerbosity_FromEnv(t *testing.T) {
	ctx := context.Background()
	for env, level := range map[string]slog.Level{"2": slog.LevelDebug, "lots": slog.LevelError} {
		t.Setenv("SIND_VERBOSE", env)
		var m mock.Executor
		m.AddResult("", "", nil) // get realms: no containers
		root := NewRootCommand()
		var stderr bytes.Buffer
		root.SetOut(io.Discard)
		root.SetErr(&stderr)
		root.SetArgs([]string{"get", "realms"})
		root.SetContext(withClient(ctx, docker.NewClient(&m)))

		require.NoError(t, root.Execute())

		get, _, err := root.Find([]string{"get", "realms"})
		require.NoError(t, err)
		log := sindlog.From(get.Context())
		assert.True(t, log.Enabled(ctx, level), env)
		assert.False(t, log.Enabled(ctx, level-1), env)
		notes := 0
		if env == "lots" {
			notes = 1
		}
		assert.Equal(t, notes, strings.Count(stderr.String(), "SIND_VERBOSE"), stderr.String())
	}
}
