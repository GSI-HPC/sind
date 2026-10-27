// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
)

func TestRunFacts(t *testing.T) {
	run := runFixture(t, "create-failed.jsonl", -1)[0]
	f := runFactsOf(runCtxOf(run, runAt(60)))
	facts := map[string]string{}
	for _, x := range f.Facts {
		facts[x.Key] = x.Val
	}
	assert.Equal(t, "5d3c0a17b94e82f6", facts["Run"])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", facts["Trace"])
	assert.Equal(t, "00f067aa0ba902b7", facts["Caller span"])
	assert.Equal(t, "sind v0.11.0", facts["Program"])
	assert.Equal(t, "failed", facts["Status"])
	assert.Equal(t, "timeout", facts["Class"])
	assert.Contains(t, facts["Error"], "worker-1: timed out")
	assert.Contains(t, facts["Started"], "2026-10-14")
	assert.Contains(t, facts["Ended"], "2026-10-14")
	assert.Equal(t, "37 below the command, 21 of them hidden", facts["Spans"])
	assert.Equal(t, "0, 0 not sent to the sinks", facts["Line events"])
	assert.NotContains(t, facts, "Exit code")

	// A run that goes on has no end; the exit code and the line counts show.
	live := runOf(0, "", runSp("step", "s", runStartedAt(0), runWith(func(s *store.Span) { s.Lines, s.Dropped = 4, 1 }), runKids(
		runSp("call", "c", runStartedAt(0), runWith(func(s *store.Span) { s.Lines, s.Dropped = 2, 0 })))))
	live.Root.Exit = new(int)
	live.Root.Lines, live.Root.Dropped = 1, 3
	facts = map[string]string{}
	for _, x := range runFactsOf(runCtxOf(live, runAt(1))).Facts {
		facts[x.Key] = x.Val
	}
	assert.NotContains(t, facts, "Ended")
	assert.Equal(t, "0", facts["Exit code"])
	assert.Equal(t, "7, 4 not sent to the sinks", facts["Line events"])
}

func TestRunTitleNamesTheClusterOfTheTask(t *testing.T) {
	task := &store.Task{Cluster: "dev"}
	assert.Equal(t, "create cluster dev", runTitle(store.Run{Title: "create cluster"}, task))
	assert.Equal(t, "create cluster dev", runTitle(store.Run{Title: "create cluster dev"}, task))
	assert.Equal(t, "delete realm", runTitle(store.Run{Title: "delete realm"}, &store.Task{}))
	assert.Empty(t, runTitle(store.Run{}, task))
}
