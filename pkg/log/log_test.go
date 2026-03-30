// SPDX-License-Identifier: LGPL-3.0-or-later

package log

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrom_NoLogger(t *testing.T) {
	l := From(context.Background())
	require.NotNil(t, l)
	// Must be a no-op: Enabled returns false for all levels.
	assert.False(t, l.Enabled(context.Background(), slog.LevelDebug))
	assert.False(t, l.Enabled(context.Background(), slog.LevelInfo))
}

func TestWithFrom_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx := With(context.Background(), logger)
	got := From(ctx)

	got.InfoContext(ctx, "hello", "key", "val")
	assert.Contains(t, buf.String(), "hello")
	assert.Contains(t, buf.String(), "key=val")
}

func TestFrom_NoOp_ProducesNoOutput(_ *testing.T) {
	l := From(context.Background())
	// Calling log methods on the no-op logger must not panic.
	l.InfoContext(context.Background(), "should be silent")
}

func TestLevelTrace(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: LevelTrace}))

	ctx := With(context.Background(), logger)
	l := From(ctx)
	l.Log(ctx, LevelTrace, "trace message")
	assert.Contains(t, buf.String(), "trace message")
}

func TestDiscardHandler_WithAttrs(t *testing.T) {
	h := discardHandler{}
	got := h.WithAttrs([]slog.Attr{slog.String("key", "val")})
	assert.Equal(t, h, got)
}

func TestDiscardHandler_WithGroup(t *testing.T) {
	h := discardHandler{}
	got := h.WithGroup("group")
	assert.Equal(t, h, got)
}

func TestDiscardHandler_Handle(_ *testing.T) {
	h := discardHandler{}
	// Handle must not panic and must return nil.
	_ = h.Handle(context.Background(), slog.Record{})
}

func TestNop(t *testing.T) {
	l := Nop()
	require.NotNil(t, l)
	assert.False(t, l.Enabled(context.Background(), slog.LevelInfo))
}

func TestLevelTrace_FilteredAtDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx := With(context.Background(), logger)
	l := From(ctx)
	l.Log(ctx, LevelTrace, "should not appear")
	assert.Empty(t, buf.String())
}
