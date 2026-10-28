// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
)

func TestRunResult(t *testing.T) {
	node := func(name, status string, kids ...store.Span) store.Span {
		return runSp("target", name, runEndedAt(0, 9, status), runKids(kids...), runWith(func(s *store.Span) { s.Node, s.Role = name, "worker" }))
	}
	nodes := func(status string, kids ...store.Span) store.Span {
		return runSp("step", "nodes", runEndedAt(0, 9, status), runKids(kids...))
	}
	rolledBack := runSp("step", "rollback", runEndedAt(9, 9.5, "ok"))
	of := func(name, status string, kids ...store.Span) store.Run {
		run := runOf(10, status, kids...)
		run.Root.Name = name
		return run
	}
	for name, tc := range map[string]struct {
		run         store.Run
		text, class string
	}{
		"created":  {of("create cluster", "ok", nodes("ok", node("worker-0", "ok"), node("worker-1", "ok"), node("worker-2", "ok"))), "3 nodes ready for jobs", ""},
		"rebooted": {of("power reboot", "ok", nodes("ok", node("worker-0", "ok"))), "1 node rebooted", ""},
		"no nodes": {of("create cluster", "ok"), "done", ""},
		"other":    {of("connect container", "ok", nodes("ok", node("w", "ok"))), "done", ""},
		"timed out": {of("create cluster", "failed", nodes("failed", timedOut("worker-1", "munge", 5*time.Minute)), rolledBack),
			"worker-1 timed out · rolled back", "bad"},
		"failed": {of("power on", "failed", nodes("failed", node("worker-1", "failed"), node("worker-2", "failed"))), "worker-[1,2] failed", "bad"},
		"bare":   {of("delete cluster", "failed", runSp("step", "rollback", runEndedAt(9, 9.5, "failed"))), "failed · rollback failed", "bad"},
		"left":   {of("delete cluster", "failed", runSp("step", "rollback", runStartedAt(9))), "failed · rollback did not finish", "bad"},
		"stopped in setup": {of("create worker", "canceled", nodes("canceled", node("worker-2", "canceled", runSp("call", "setup", runEndedAt(1, 9, "canceled")))), rolledBack),
			"stopped in setup of worker-2 · nothing left", "warn"},
		"stopped in container": {of("create worker", "canceled", nodes("canceled",
			node("worker-1", "ok"), node("worker-2", "canceled", runSp("call", "docker create", runEndedAt(1, 2, "ok")), runSp("call", "docker start", runEndedAt(2, 9, "canceled"))))),
			"stopped in container of worker-2", "warn"},
		"stopped in services": {of("create worker", "canceled", nodes("canceled", node("worker-2", "canceled", runSp("wait", "ready", runEndedAt(1, 9, "canceled"), runWith(func(s *store.Span) { s.Message = "munge" }))))),
			"stopped in services of worker-2", "warn"},
		// systemd is part of the container, as the outcome says; nodes in
		// the same phase fold, and a phase that had not begun does not
		// count.
		"stopped for systemd": {of("create worker", "canceled", nodes("canceled",
			node("worker-2", "canceled", runSp("wait", "ready", runEndedAt(1, 9, "canceled"), runWith(func(s *store.Span) { s.Message = "systemd" })), runSp("wait", "ready")),
			node("worker-3", "canceled", runSp("wait", "ready", runEndedAt(1, 9, "canceled"))),
			node("worker-4", "canceled", runSp("wait", "ready", runEndedAt(1, 9, "canceled"), runWith(func(s *store.Span) { s.Message = "munge" }))))),
			"stopped in container of worker-[2,3]", "warn"},
		"stopped between phases": {of("create worker", "canceled", nodes("canceled", node("worker-2", "canceled", runSp("call", "docker create", runEndedAt(1, 2, "ok"))))),
			"stopped in setup of worker-2", "warn"},
		"stopped before a node": {of("create worker", "canceled", nodes("canceled")), "stopped", "warn"},
		"stopped for the lock": {of("create worker", "canceled", runSp("wait", runLockWait, runEndedAt(0, 9, "canceled"))),
			"stopped while it waited for the lock · nothing changed", "warn"},
		"skipped": {of("create worker", "skipped"), "skipped in 10s", ""},
	} {
		text, class := runResult(tc.run, runAt(20))
		assert.Equal(t, tc.text, text, name)
		assert.Equal(t, tc.class, class, name)
	}
	text, class := runResult(runOf(0, "", runSp("step", "nodes", runStartedAt(0))), runAt(5))
	assert.Equal(t, "nodes", text, "a run that goes on says what it does")
	assert.Empty(t, class)
}
