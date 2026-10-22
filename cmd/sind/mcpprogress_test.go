// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/GSI-HPC/go-clikit/fanout"
	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logRun runs the targets under a Bus whose event log is appended to path,
// in the trace tc.
func logRun(t *testing.T, path string, tc progress.TraceContext, targets ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	log := progress.NewLog(f, progress.LogOptions{Program: "sind"})
	bus := progress.NewBus(progress.BusOptions{Sinks: []progress.Sink{log}, Program: "sind", Trace: tc})
	ctx, span := progress.Start(progress.WithBus(t.Context(), bus), progress.KindCommand, "create cluster")
	_, err = fanout.Map(ctx, targets, fanout.MapOptions[string]{Step: "start the nodes"},
		func(context.Context, string) (struct{}, error) { return struct{}{}, nil })
	require.NoError(t, err)
	span.End(nil)
	bus.Close()
	require.NoError(t, log.Close())
}

func newTrace(t *testing.T) progress.TraceContext {
	t.Helper()
	tc, err := progress.NewTraceContext(rand.Reader)
	require.NoError(t, err)
	return tc
}

func TestFollowToolProgress_OnlyTheCallsTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	mine, other := newTrace(t), newTrace(t)
	// What the log held before the call is not sent.
	logRun(t, path, other, "old1", "old2")

	var mu sync.Mutex
	var got []string
	finish := followToolProgress(path, mine.Trace, func(done, total float64, message string) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, fmt.Sprintf("%v/%v %s", done, total, message))
		return nil
	})
	logRun(t, path, other, "other1")
	logRun(t, path, mine, "n1", "n2", "n3")
	finish()
	finish()

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, got)
	assert.Equal(t, "3/3 start the nodes: 3/3 done", got[len(got)-1])
	for _, g := range got {
		assert.Contains(t, g, "start the nodes", "nothing of the other runs")
	}
}

func TestFollowToolProgress_MissingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone", "activity.jsonl")
	finish := followToolProgress(path, newTrace(t).Trace, func(float64, float64, string) error {
		t.Error("sent progress for a log that is not there")
		return nil
	})
	finish()
}

func TestWithToolProgress_NoToken(t *testing.T) {
	ctx := t.Context()
	got, finish := withToolProgress(ctx, &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "sind_get_nodes"}})
	finish()
	assert.Equal(t, ctx, got, "a call without a token runs as before")

	params := &mcp.CallToolParamsRaw{Name: "sind_get_nodes"}
	params.SetProgressToken("tok")
	got, finish = withToolProgress(ctx, &mcp.CallToolRequest{Params: params})
	finish()
	assert.Equal(t, ctx, got, "so does one that has no session to send on")
}

func TestToolProgressRelay(t *testing.T) {
	var r toolProgressRelay
	params := &mcp.CallToolParamsRaw{Name: "sind_create_cluster"}
	params.SetProgressToken("outer")
	req := &mcp.CallToolRequest{Params: params}
	token, forget := r.add(req)
	other, forgetOther := r.add(req)
	assert.NotEqual(t, token, other)
	defer forgetOther()

	// None of these reach a client: they must be dropped, not panic.
	note := func(token any) *mcp.ProgressNotificationClientRequest {
		return &mcp.ProgressNotificationClientRequest{Params: &mcp.ProgressNotificationParams{ProgressToken: token, Progress: 1}}
	}
	r.handle(t.Context(), note("not mine"))
	r.handle(t.Context(), note(float64(token)))
	r.handle(t.Context(), note(token))
	forget()
	r.handle(t.Context(), note(float64(token)))
	r.handle(t.Context(), note(float64(999)))
}
