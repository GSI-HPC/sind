// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"time"
)

// nodeSet returns the nodes of a cluster of a controller and workers, each
// with the time it gets ready; a ready of 0 never gets ready.
func nodeSet(cluster string, controller time.Duration, workers ...time.Duration) []node {
	nodes := []node{{name: "controller", cluster: cluster, role: "controller", ready: controller}}
	for i, w := range workers {
		nodes = append(nodes, node{name: "worker-" + string(rune('0'+i)), cluster: cluster, role: "worker", ready: w})
	}
	return nodes
}

// history returns the event logs of the runs that came before the demo
// started, oldest first: a few that ended ok, one that failed after its
// nodes ran out of time and rolled back, and one that was interrupted.
func history(now time.Time) [][]byte {
	const (
		node26 = "ghcr.io/gsi-hpc/sind-node:26.05.4"
		node25 = "ghcr.io/gsi-hpc/sind-node:25.11.8"
	)
	devNodes := nodeSet("dev", 14*time.Second, 19*time.Second, 22*time.Second, 25*time.Second, 28*time.Second, 31*time.Second, 34*time.Second)
	devNodes = append(devNodes,
		node{name: "controller-backup", cluster: "dev", role: "controller", ready: 16 * time.Second},
		node{name: "db", cluster: "dev", role: "db", ready: 18 * time.Second},
		node{name: "api", cluster: "dev", role: "api", ready: 17 * time.Second},
		node{name: "submitter", cluster: "dev", role: "submitter", ready: 15 * time.Second})
	runs := []recording{
		{title: "create cluster", target: "legacy", start: now.Add(-72 * time.Hour),
			work: create(createSpec{image: node25, mesh: true, nodes: nodeSet("legacy", 14*time.Second, 19*time.Second, 22*time.Second)})},
		{title: "create cluster", target: "dev", start: now.Add(-27 * time.Hour),
			work: create(createSpec{image: node26, mesh: true, nodes: devNodes})},
		{title: "power reboot", target: "dev", start: now.Add(-21 * time.Hour),
			work: func(ctx context.Context, p play) error { return p.power(ctx, "reboot", nodeSet("dev", 0, 0, 0)[1:]) }},
		{title: "delete cluster", target: "scratch", start: now.Add(-20 * time.Hour),
			work: func(ctx context.Context, p play) error {
				return p.remove(ctx, nodeSet("scratch", 0, 0, 0), true, false)
			}},
		{title: "create cluster", target: "bench", start: now.Add(-3 * time.Hour),
			work: create(createSpec{image: node26, mesh: true, nodes: nodeSet("bench", 14*time.Second, 21*time.Second, 0, 340*time.Second)})},
		{title: "create worker", target: "legacy", start: now.Add(-75 * time.Minute), cancelAfter: 9 * time.Second,
			work: create(createSpec{nodes: nodeSet("legacy", 0, 0, 0, 19*time.Second)[3:]})},
		{title: "create cluster", target: "pr-145", start: now.Add(-40 * time.Minute), ui: true,
			work: create(createSpec{image: node26, mesh: true, nodes: nodeSet("pr-145", 14*time.Second, 19*time.Second, 22*time.Second)})},
	}
	logs := make([][]byte, len(runs))
	for i, r := range runs {
		r.id, r.trace = runID(i+1), traceID(i+1)
		if r.ui {
			r.trace = uiTrace(i + 1)
		}
		logs[i] = record(r)
	}
	return logs
}

// The run that goes on in a still demo: create cluster nightly in the realm
// ci, which started midRunAge before now.
const (
	midRunAge   = 31 * time.Second
	midRunTask  = "7a5c0000000000a1"
	midRunIndex = 99
)

// midRun returns the event log of the run that goes on at now.
func midRun(now time.Time) []byte {
	start := now.Add(-midRunAge)
	log := record(recording{
		id: runID(midRunIndex), trace: uiTrace(midRunIndex), title: "create cluster", target: "nightly", start: start,
		work: create(createSpec{image: "ghcr.io/gsi-hpc/sind-node:26.05.4", mesh: true, nodes: nodeSet("nightly", 14*time.Second, 19*time.Second, 22*time.Second)}),
	})
	return cutAfter(log, now)
}
