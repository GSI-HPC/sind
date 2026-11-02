// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// nodeTarget is the target of a node in a step of a test.
func nodeTarget(node, role string) store.Span {
	return store.Span{Kind: KindTarget, Name: node, Node: node, Role: role}
}

func TestTitleNamesTheNodes(t *testing.T) {
	step := func(name string, targets ...store.Span) store.Span {
		return store.Span{Kind: KindStep, Name: name, Children: targets}
	}
	workers := step("nodes", nodeTarget("worker-0", "worker"), nodeTarget("worker-1", "worker"), nodeTarget("worker-2", "worker"))
	for name, tc := range map[string]struct {
		root store.Span
		want string
	}{
		"a power command names its nodes": {
			store.Span{Name: "power reboot", Node: "dev", Children: []store.Span{workers}}, "power reboot worker-[0-2].dev",
		},
		"create worker names the new one": {
			store.Span{Name: "create worker", Node: "legacy", Children: []store.Span{
				step("nodes", nodeTarget("worker-2", "worker")), step("rollback", nodeTarget("worker-9", "worker")),
			}}, "create worker worker-2.legacy",
		},
		"the default cluster has no suffix": {
			store.Span{Name: "delete worker", Node: "default", Children: []store.Span{workers}}, "delete worker worker-[0-2]",
		},
		"before the log names the nodes": {
			store.Span{Name: "power on", Node: "dev"}, "power on dev",
		},
		"a target without a role is no node": {
			store.Span{Name: "power on", Node: "dev", Children: []store.Span{step("mesh", store.Span{Kind: KindTarget, Name: "dns", Node: "dns"})}}, "power on dev",
		},
		"nodes of several clusters": {
			store.Span{Name: "power on", Node: "dev,legacy", Children: []store.Span{workers}}, "power on dev,legacy",
		},
		"a command of the cluster": {
			store.Span{Name: "create cluster", Node: "dev", Children: []store.Span{workers}}, "create cluster dev",
		},
		"no target": {store.Span{Name: "doctor"}, "doctor"},
	} {
		assert.Equal(t, tc.want, title(tc.root), name)
	}
}

func TestNodeNamesOnce(t *testing.T) {
	root := store.Span{Children: []store.Span{
		{Kind: KindStep, Name: "stopping", Children: []store.Span{nodeTarget("worker-1", "worker")}},
		{Kind: KindStep, Name: "starting", Children: []store.Span{{Kind: KindBatch, Children: []store.Span{nodeTarget("worker-1", "worker"), nodeTarget("worker-0", "worker")}}}},
	}}
	assert.Equal(t, []string{"worker-1", "worker-0"}, NodeNames(root))
}

func TestFoldNodes(t *testing.T) {
	assert.Equal(t, "worker-[0-1].dev", FoldNodes("dev", []string{"worker-1", "worker-0"}))
	assert.Equal(t, "controller.dev,worker-0.dev", FoldNodes("dev", []string{"controller", "worker-0"}))
	assert.Equal(t, "w1,w01", FoldNodes("default", []string{"w1", "w01"}), "names that fold to fewer nodes stay one by one")
	assert.Equal(t, "a[b", FoldNodes("default", []string{"a[b"}), "no node set")
}

func TestReducerTitleAndCommand(t *testing.T) {
	r := NewReducer(5)
	r.Take(ev("start", runA, "2026-01-01T00:00:00Z", `"span":"r1","kind":"command","name":"power reboot","node":"dev"`))
	r.Take(ev("start", runA, "2026-01-01T00:00:01Z", `"span":"s1","parent":"r1","kind":"step","name":"stopping"`))
	r.Take(ev("start", runA, "2026-01-01T00:00:01Z", `"span":"t1","parent":"s1","kind":"target","name":"worker-4","node":"worker-4","role":"worker"`))
	run := r.Runs()[0]
	assert.Equal(t, "power reboot worker-4.dev", run.Title)
	assert.Equal(t, "power reboot dev", run.Command)
}
