// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/stretchr/testify/assert"
)

func TestRunStoppedWho(t *testing.T) {
	run := runOf(9.9, "canceled", runSp("step", "nodes", runEndedAt(0, 9.9, "canceled")))
	rc := runCtx{Run: run, Found: true, Now: runAt(30)}
	assert.Equal(t, "Stopped from the terminal", runStoppedTitle(rc))
	assert.Equal(t, "Someone stopped the command in its terminal (Ctrl-C) after 9.9s.", runStoppedLead(rc))

	rc.Run.Parent = "00f067aa0ba902b7"
	assert.Equal(t, "Stopped by another program", runStoppedTitle(rc))
	assert.Equal(t, "The program that started the run stopped it after 9.9s.", runStoppedLead(rc))

	rc.Run.FromUI = true
	assert.Equal(t, "Stopped from sind ui", runStoppedTitle(rc))
	assert.Equal(t, "Someone stopped the run from another sind ui after 9.9s.", runStoppedLead(rc))
	rc.Task = &store.Task{ID: "t1"}
	assert.Equal(t, "You stopped the run from sind ui after 9.9s.", runStoppedLead(rc))

	// While the run waited for the lock of its realm, nothing changed.
	rc.Run = runOf(4, "canceled", runSp("wait", runLockWait, runEndedAt(0, 4, "canceled")), runSp("step", "nodes"))
	assert.Equal(t, "You stopped the run from sind ui while it waited for the realm lock. Nothing changed.", runStoppedLead(rc))
	rc.Run = runOf(4, "canceled", runSp("wait", runLockWait, runEndedAt(0, 1, "ok")), runSp("step", "nodes", runStartedAt(1)))
	assert.Equal(t, "You stopped the run from sind ui after 4.0s.", runStoppedLead(rc), "the lock was got, and a step began")
	rc.Run = runOf(4, "canceled", runSp("wait", runLockWait, runEndedAt(0, 4, "canceled")), runSp("step", "nodes", runStartedAt(1)))
	assert.False(t, runStoppedInLockWait(rc.Run))
}

func TestRunStoppedUndo(t *testing.T) {
	snap := store.Snapshot{Clusters: map[string][]cluster.Summary{"sind": {{Name: "legacy", NodeCount: 3}}}}
	worker := runSp("target", "worker-2", runEndedAt(0, 9, "canceled"), runWith(func(s *store.Span) { s.Node, s.Role = "worker-2", "worker" }))
	run := runOf(9.9, "canceled", runSp("step", "nodes", runEndedAt(0, 9, "canceled"), runKids(worker)), runSp("step", "rollback", runEndedAt(9, 9.8, "ok")))
	run.Root.Name, run.Root.Node = "create worker", "legacy"
	assert.Equal(t, []string{"sind removed worker-2 again (rollback, 0.8s).", "legacy is as it was before the run: 3 nodes."},
		runStoppedUndo(runCtx{Run: run, Snap: snap}))

	// No node made, and a rollback without an end.
	bare := runOf(9.9, "canceled", runSp("step", "rollback", runWith(func(s *store.Span) {
		s.State, s.Status, s.Started = "ended", "ok", runAt(9)
	})))
	assert.Equal(t, []string{"sind removed what the command had made (rollback)."}, runStoppedUndo(runCtx{Run: bare, Snap: snap}))
	failed := runOf(9.9, "canceled", runSp("step", "rollback", runEndedAt(9, 9.8, "failed")))
	assert.Equal(t, []string{"The rollback failed, so some of what the command created may be left behind."}, runStoppedUndo(runCtx{Run: failed}))
	assert.Nil(t, runStoppedUndo(runCtx{Run: runOf(9, "canceled")}))
}

func TestRunHeadOfAStoppedCreateWorker(t *testing.T) {
	snap := store.Snapshot{Clusters: map[string][]cluster.Summary{"sind": {{Name: "legacy"}}}}
	nodes := func(names ...string) store.Span {
		var kids []store.Span
		for _, n := range names {
			kids = append(kids, runSp("target", n, runEndedAt(0, 9, "canceled"), runWith(func(s *store.Span) { s.Node, s.Role = n, "worker" })))
		}
		return runSp("step", "nodes", runEndedAt(0, 9, "canceled"), runKids(kids...))
	}
	run := runOf(9, "canceled", nodes("worker-2"))
	run.Title, run.Root.Name, run.Root.Node = "create worker worker-2.legacy", "create worker", "legacy"
	h := runHeadOf(runCtx{Run: run, Snap: snap})
	assert.Equal(t, &runLink{Label: "Add a worker again…", Href: "/dialogs/create-worker?cluster=legacy&realm=sind"}, h.Edit)
	assert.Equal(t, &runLink{Label: "Open legacy", Href: "/clusters/sind/legacy", Primary: true}, h.Back, "the cluster, as it was, is the button to press")
	run.Root.Children = []store.Span{nodes("worker-2", "worker-3")}
	h = runHeadOf(runCtx{Run: run, Snap: snap})
	assert.Equal(t, &runLink{Label: "Add workers again…", Href: "/dialogs/create-worker?cluster=legacy&count=2&realm=sind"}, h.Edit)
}
