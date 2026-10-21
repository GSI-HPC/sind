// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	sindlog "github.com/GSI-HPC/sind/pkg/log"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
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

	t.Setenv(envLogTimestamps, "false")
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
