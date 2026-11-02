// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHeadNamesTheHolderOfTheLock(t *testing.T) {
	wait := runSp("wait", runLockWait, runStartedAt(0), runWith(func(s *store.Span) { s.Message = "create cluster (pid 4) on zeta" }))
	waiting := runOf(0, "", wait)
	waiting.ID, waiting.Title, waiting.Root.Node = "rw", "create worker dev", "dev"
	holder := runOf(0, "", runSp("step", "nodes", runStartedAt(0)))
	holder.ID, holder.Title, holder.Root.Node = "rh", "create cluster lab", "lab"
	holder.Start = runAt(-5)
	younger := holder
	younger.ID, younger.Start = "ry", runAt(-1)
	other := runOf(0, "", runSp("step", "nodes", runStartedAt(0)))
	other.ID, other.Root.Node = "ro", "far"
	ended := runOf(3, "ok")
	ended.ID, ended.Root.Node = "re", "lab"
	snap := store.Snapshot{
		Clusters: map[string][]cluster.Summary{"sind": {{Name: "dev"}, {Name: "lab"}}, "ci": {{Name: "far"}}},
		Runs:     []store.Run{waiting, younger, holder, other, ended},
	}
	h := runHeadOf(runCtx{Run: waiting, Found: true, Snap: snap, Now: runAt(10)})
	assert.True(t, h.Waits)
	assert.Equal(t, &runLink{Label: "create cluster lab", Href: "/runs/rh"}, h.Holder, "the oldest run of the realm")
	assert.Empty(t, h.HolderText)

	// The log names another command: no run that sind ui sees holds it,
	// as a run from a terminal that was killed never ends in the log.
	named := func(msg string) *runHead {
		w := waiting
		w.Root.Children = []store.Span{runSp("wait", runLockWait, runStartedAt(0), runWith(func(s *store.Span) { s.Message = msg }))}
		return runHeadOf(runCtx{Run: w, Found: true, Snap: snap, Now: runAt(10)})
	}
	h = named("sind create cluster zeta (pid 4 on box, since 2026-01-01 10:00:00)")
	assert.Nil(t, h.Holder)
	assert.Equal(t, "sind create cluster zeta (pid 4 on box, since 2026-01-01 10:00:00)", h.HolderText)
	assert.Nil(t, named("sind delete cluster lab (pid 4 on box, since …)").Holder)
	for _, line := range []string{"sind create cluster lab", "sind create cluster --config /home/me/lab.yaml --pull", "sind --realm sind create cluster worker-1.lab"} {
		assert.Equal(t, "/runs/rh", named(line+" (pid 4 on box, since …)").Holder.Href, line)
	}
	def := holder
	def.Root.Name, def.Root.Node = "power reboot", "default"
	assert.True(t, runNamedBy(def, "sind power reboot worker-1"))
	assert.False(t, runNamedBy(def, "sind power reboot worker-1.dev"))
	assert.False(t, runNamedBy(store.Run{}, "sind"))

	// No run of sind ui holds it: what the log names.
	snap.Runs = []store.Run{waiting, other, ended}
	h = runHeadOf(runCtx{Run: waiting, Found: true, Snap: snap, Now: runAt(10)})
	assert.Nil(t, h.Holder)
	assert.Equal(t, "create cluster (pid 4) on zeta", h.HolderText)
	// A run whose realm sind ui does not know.
	snap.Clusters = nil
	h = runHeadOf(runCtx{Run: waiting, Found: true, Snap: snap, Now: runAt(10)})
	assert.Nil(t, h.Holder)
	holder2, text := runLockHolder(runCtx{Run: runOf(0, ""), Snap: snap})
	assert.Nil(t, holder2)
	assert.Empty(t, text, "no wait")
	assert.False(t, runHeadOf(runCtx{Run: holder, Snap: snap}).Waits)
}

func TestRunPageLinksTheHolderOfTheLock(t *testing.T) {
	ts := runTestServer(t, runNow)
	ts.st.SetClusters("sind", []cluster.Summary{{Name: "dev"}, {Name: "lab"}})
	waiting := runOf(0, "", runSp("wait", runLockWait, runStartedAt(0)))
	waiting.ID, waiting.Title, waiting.Root.Node = "rw", "create worker dev", "dev"
	holder := runOf(0, "", runSp("step", "nodes", runStartedAt(0)))
	holder.ID, holder.Title, holder.Root.Node = "rh", "create cluster lab", "lab"
	ts.st.SetRuns([]store.Run{waiting, holder})
	body := ts.get("/runs/rw").Body.String()
	require.True(t, strings.Contains(body, `<div class="callout run-lock">This run waits for the lock of its realm, which <a href="/runs/rh">create cluster lab</a> holds. It goes on once the lock is free.</div>`), "the page links the holder")
	ts.st.SetRuns([]store.Run{waiting})
	assert.Contains(t, ts.get("/runs/rw").Body.String(), `<div class="callout run-lock">This run waits for the lock of its realm. It goes on once the lock is free.</div>`)
}
