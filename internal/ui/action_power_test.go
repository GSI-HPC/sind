// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// actValues is the form of key and value pairs.
func actValues(kv ...string) url.Values {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Add(kv[i], kv[i+1])
	}
	return q
}

// An action that fits none of the ticked nodes says why in a few words:
// the reason of the one node, the reason that all share, each node's
// reason for a few nodes, or how many nodes it fits none of.
func TestActOff_Why(t *testing.T) {
	for _, c := range []struct {
		off       actOff
		why, text string
	}{
		{actOff{[]string{"worker-0"}, []string{"It runs already"}}, "worker-0 runs already", "worker-0 runs already"},
		{actOff{[]string{"controller"}, []string{"Only workers take jobs"}}, "controller: only workers take jobs", ""},
		{actOff{[]string{"worker-0", "worker-1"}, []string{"It runs already", "It runs already"}}, "each runs already", ""},
		{actOff{[]string{"controller", "db"}, []string{"Only workers take jobs", "Only workers take jobs"}}, "only workers take jobs", ""},
		{actOff{[]string{"controller", "worker-0"}, []string{"Only workers take jobs", "It is drained already"}}, "controller: only workers take jobs; worker-0 is drained already", ""},
		{actOff{[]string{"a", "b", "c", "d"}, []string{"It does not run", "It is frozen", "It does not run", "It does not run"}}, "", "fits none of the 4 ticked nodes"},
	} {
		assert.Equal(t, c.why, c.off.Why(), c.off.Names)
		if c.text != "" {
			assert.Equal(t, c.text, c.off.Text(), c.off.Names)
		}
	}
	assert.Empty(t, actLowerFirst(""))
}

// actPowerTestView is a view of cluster dev: a running controller and
// worker-0, and worker-1, which is stopped.
func actPowerTestView() *View {
	st := store.New()
	st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	w1 := actNode("dev", "worker-1", config.RoleWorker)
	w1.State = cluster.StateStopped
	st.SetNodes("sind", []cluster.NodeSummary{actNode("dev", "controller", config.RoleController), actNode("dev", "worker-0", config.RoleWorker), w1})
	return &View{Snap: st.Snapshot(), Now: time.Unix(0, 0)}
}

func TestActPowerOffs(t *testing.T) {
	v := actPowerTestView()
	form := func(kv ...string) action.Form { return actValues(kv...) }

	assert.Empty(t, actPowerOffs(v, form("cluster", "dev")), "no node is ticked")
	assert.Empty(t, actPowerOffs(v, form("cluster", "gone", "nodes", "worker-0")), "a cluster that sind ui does not see")

	offs := actPowerOffs(v, form("cluster", "dev", "nodes", "worker-0,worker-1"))
	assert.NotContains(t, offs, "on", "Start fits worker-1")
	assert.NotContains(t, offs, "reboot", "Reboot fits worker-0")
	assert.Equal(t, actOff{[]string{"worker-0", "worker-1"}, []string{"It is not frozen", "It is not frozen"}}, offs["unfreeze"])

	p := actPreview{View: v, Form: form("cluster", "dev", "op", "reboot", "nodes", "worker-0")}
	assert.Equal(t, "Off for the ticked nodes: Drain (worker-0: sinfo does not list it) · Resume (worker-0: sinfo does not list it) · Start (worker-0 runs already) · Unfreeze (worker-0 is not frozen).", p.PowerOffs())
	p.Form = form("cluster", "dev", "op", "reboot")
	assert.Empty(t, p.PowerOffs())

	assert.Empty(t, actPowerMisfit(v, form("cluster", "dev", "op", "reboot", "nodes", "worker-0")))
	assert.Empty(t, actPowerMisfit(v, form("cluster", "dev", "nodes", "worker-0")), "no action yet")
	assert.Equal(t, "Start fits none of the ticked nodes: worker-0 runs already. Pick another action or other nodes.",
		actPowerMisfit(v, form("cluster", "dev", "op", "on", "nodes", "worker-0")))
}

func TestActPowerMisfit_ManyNodes(t *testing.T) {
	st := store.New()
	st.SetClusters("sind", []cluster.Summary{{Name: "dev"}})
	var nodes []cluster.NodeSummary
	for _, n := range []string{"worker-0", "worker-1", "worker-2"} {
		nodes = append(nodes, actNode("dev", n, config.RoleWorker))
	}
	paused := actNode("dev", "worker-3", config.RoleWorker)
	paused.State = cluster.StatePaused
	st.SetNodes("sind", append(nodes, paused))
	v := &View{Snap: st.Snapshot(), Now: time.Unix(0, 0)}
	f := actValues("cluster", "dev", "op", "on", "nodes", "worker-0,worker-1,worker-2,worker-3")
	assert.Equal(t, "Start fits none of the ticked nodes. Pick another action or other nodes.", actPowerMisfit(v, f))
}

// The button of the Power dialog names the action and the nodes before the
// form is complete.
func TestActPowerOK(t *testing.T) {
	sn := actPowerTestView().Snap
	ok := func(kv ...string) string { return actPowerOK(sn, "sind", "dev", actValues(kv...)) }
	assert.Empty(t, ok())
	assert.Equal(t, "Reboot nodes", ok("op", "reboot"))
	assert.Equal(t, "Drain worker-0", ok("op", "drain", "nodes", "worker-0"))
	assert.Equal(t, "Cut power to 2 workers", ok("op", "cut", "nodes", "worker-0,worker-1"))
	assert.Equal(t, "Reboot 2 nodes", ok("op", "reboot", "nodes", "worker-0,controller"))
	assert.Empty(t, actPendingOK("connect", sn, "sind", "dev", actValues("op", "reboot")))
}
