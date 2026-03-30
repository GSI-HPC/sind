// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	sindlog "github.com/GSI-HPC/sind/pkg/log"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
)

func TestNewLogger_Silent(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 0)
	l.InfoContext(context.Background(), "should not appear")
	assert.Empty(t, buf.String())
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

func TestNewLogger_NoTimestamp(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 1)
	l.InfoContext(context.Background(), "test")
	assert.NotContains(t, buf.String(), slog.TimeKey)
}

func TestNewLogger_HighVerbosityClamped(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, 10) // more than 3

	ctx := context.Background()
	l.Log(ctx, sindlog.LevelTrace, "deep trace")
	assert.Contains(t, buf.String(), "deep trace")
}

func TestVerboseFlag_AcceptedByRoot(t *testing.T) {
	// Verify -v flag is wired to root and doesn't cause "unknown flag".
	stdout, _, err := executeCommand("-v", "--help")
	assert.NoError(t, err)
	assert.Contains(t, stdout, "sind")
}

func TestVerboseFlag_InjectsLogger(t *testing.T) {
	mock := &docker.MockExecutor{}
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
