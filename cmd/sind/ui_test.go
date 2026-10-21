// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeUI replaces runUI for a test and returns what it was called with.
func fakeUI(t *testing.T, err error) *ui.Options {
	t.Helper()
	var got ui.Options
	saved := runUI
	t.Cleanup(func() { runUI = saved })
	runUI = func(_ context.Context, o ui.Options) error {
		got = o
		_, _ = fmt.Fprintln(o.Stderr, "sind ui: http://127.0.0.1:1/")
		return err
	}
	return &got
}

func TestUICommand(t *testing.T) {
	got := fakeUI(t, nil)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	_, stderr, err := executeCommand("ui", "--port", "8080", "--no-open")
	require.NoError(t, err)
	assert.Equal(t, 8080, got.Port)
	assert.True(t, got.NoOpen)
	assert.Equal(t, "/run/user/1000", got.RuntimeDir)
	assert.Equal(t, "sind ui: http://127.0.0.1:1/\n", stderr, "the URL goes to standard error")
}

func TestUICommand_Defaults(t *testing.T) {
	got := fakeUI(t, nil)
	t.Setenv("XDG_RUNTIME_DIR", "")

	_, _, err := executeCommand("ui")
	require.NoError(t, err)
	assert.Zero(t, got.Port, "a free port")
	assert.False(t, got.NoOpen)
	assert.Empty(t, got.RuntimeDir)
}

func TestUICommand_Error(t *testing.T) {
	fakeUI(t, errors.New("port 80 is in use"))
	_, _, err := executeCommand("ui")
	assert.EqualError(t, err, "port 80 is in use")
}

func TestUICommand_Arguments(t *testing.T) {
	fakeUI(t, nil)
	_, _, err := executeCommand("ui", "extra")
	assert.ErrorContains(t, err, "accepts no arguments")
	_, _, err = executeCommand("ui", "--port", "x")
	assert.Error(t, err)
}

func TestUICommand_Help(t *testing.T) {
	stdout, _, err := executeCommand("ui", "--help")
	require.NoError(t, err)
	assert.Contains(t, stdout, "--no-open")
	assert.Contains(t, stdout, "--port")
}
