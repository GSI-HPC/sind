// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The Result column of Activity says what came of each run in a few
// words: "3 nodes ready for jobs", "worker-1 timed out · rolled back",
// "stopped in setup of worker-2 · nothing left".

// runNodeVerbs say what a command that ended ok did to its nodes; gantt.js
// has the same words for its counts.
var runNodeVerbs = map[string]string{
	"create cluster": "ready for jobs", "create worker": "ready for jobs",
	"delete cluster": "removed", "delete worker": "removed",
	"power on": "powered on", "power unfreeze": "unfrozen", "power cut": "cut off", "power cycle": "power-cycled",
	"power freeze": "frozen", "power reboot": "rebooted", "power shutdown": "shut down",
}

// runResult says what came of a run, and the class of the words: bad for
// a failure, warn for a run that was stopped. A run that goes on says what
// it does now (runLine).
func runResult(run store.Run, now time.Time) (string, string) {
	if !run.Done() {
		return runLine(run, nil, now), ""
	}
	var parts []string
	class := ""
	switch run.Status {
	case store.StatusOK:
		names := activity.NodeNames(run.Root)
		verb, ok := runNodeVerbs[run.Root.Name]
		if !ok || len(names) == 0 {
			return "done", ""
		}
		return pgPlural(len(names), "node", "nodes") + " " + verb, ""
	case store.StatusFailed:
		class = "bad"
		if tos := runTimeouts(run); len(tos) > 0 {
			parts = append(parts, runFoldNodes(runTimeoutNodes(tos))+" timed out")
		} else if f := activity.FailedTargets(run.Root); len(f) > 0 {
			var nodes []string
			for _, g := range f {
				nodes = append(nodes, g.Nodes...)
			}
			parts = append(parts, runFoldNodes(nodes)+" failed")
		} else {
			parts = append(parts, "failed")
		}
	case store.StatusCanceled:
		class = "warn"
		parts = append(parts, runStoppedWhere(run))
	default:
		return runLine(run, nil, now), ""
	}
	switch runRollbackWord(run) {
	case "sind rolled back · nothing is left":
		if class == "warn" {
			parts = append(parts, "nothing left")
		} else {
			parts = append(parts, "rolled back")
		}
	case "the rollback failed":
		parts = append(parts, "rollback failed")
	case "the rollback did not finish":
		parts = append(parts, "rollback did not finish")
	}
	return strings.Join(parts, " · "), class
}

// runStoppedWhere says where a stopped run was, as its outcome does
// (runStoppedNodes): "stopped in setup of worker-2", or while it waited
// for the realm lock.
func runStoppedWhere(run store.Run) string {
	if runStoppedInLockWait(run) {
		return "stopped while it waited for the lock · nothing changed"
	}
	order, nodes, _ := runStoppedNodes(run)
	if len(order) == 0 {
		return "stopped"
	}
	// The nodes in the phase of the first one; the outcome names the rest.
	var names []string
	for _, ph := range order {
		if ph.Noun == order[0].Noun {
			names = append(names, nodes[ph]...)
		}
	}
	return "stopped in " + order[0].Noun + " of " + runFoldNodes(names)
}
