// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// Add workers without a cluster asks for it first: the clusters by realm,
// the realm of the link first, with their health and workers; one that a
// command creates is off, with why.
func TestActDialog_AddWorkersPicksTheCluster(t *testing.T) {
	f := newActFixture(t)
	ciNode := func(short string, role config.Role) cluster.NodeSummary {
		n := actNode("pr", short, role)
		n.Container, n.FQDN = "ci-pr-"+short, cluster.DNSName(short, "pr", "ci")
		return n
	}
	f.st.SetRealms([]cluster.RealmSummary{{Name: "ci", Clusters: 1}, {Name: "sind", Clusters: 2}})
	f.st.SetClusters("ci", []cluster.Summary{{Name: "pr"}})
	f.st.SetNodes("ci", []cluster.NodeSummary{ciNode("controller", config.RoleController), ciNode("worker-0", config.RoleWorker)})
	f.st.SetSlurm("sind", "default", &store.Slurm{})
	f.st.SetSlurm("ci", "pr", &store.Slurm{Info: &cluster.SlurmInfo{Nodes: []slurm.NodeInfo{{Name: "worker-0", State: "idle", CPUs: 2, MemoryMB: 1024}}}})

	f.st.SetRuns([]store.Run{{ID: "x", Title: "create cluster dev"}})
	w := f.get("/dialogs/create-worker?realm=ci")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `<h2 id="act-title">Add workers</h2>`)
	assert.Contains(t, body, "Pick the cluster first.")
	assert.NotContains(t, body, `name="cluster"`, "no cluster is picked for the user")
	assert.Regexp(t, `(?s)realm ci</span>.*hx-get="/dialogs/create-worker\?cluster=pr&amp;realm=ci" hx-include="#act-count,#act-cpus,#act-memory" hx-target="#layer" hx-swap="innerHTML" data-act-focus><span class="act-crn"><b class="mono">pr</b><small>1 worker, 2 CPUs and 1 GiB</small>.*realm sind</span>`, body)
	// The fixture creates dev now.
	assert.Contains(t, body, `data-act-focus disabled><span class="act-crn"><b class="mono">dev</b><small>sind creates it now. Add workers when the create ends.</small>`)
	assert.Contains(t, body, `id="act-count" name="count" value="1" min="1" readonly aria-describedby="act-err-count">`)
	assert.Contains(t, body, `placeholder="as its newest worker"`)
	assert.Contains(t, f.post("/actions/create-worker/preview", actForm("realm", "ci")).Body.String(), `<span class="hint act-tot">Pick a cluster.</span>`)

	// Picked, the cluster is a row with Change, which keeps the fields.
	body = f.get("/dialogs/create-worker?realm=ci&cluster=pr&count=3").Body.String()
	assert.Contains(t, body, `<h2 id="act-title">Add workers to pr</h2>`)
	assert.Contains(t, body, `<span class="faint">Cluster</span> <b class="mono">pr</b></span><small>realm ci · 1 worker, 2 CPUs and 1 GiB</small>`)
	assert.Contains(t, body, `hx-get="/dialogs/create-worker?realm=ci" hx-include="#act-count,#act-cpus,#act-memory" hx-target="#layer" hx-swap="innerHTML" aria-label="Change the cluster">Change…</button>`)
	assert.Contains(t, body, `id="act-count" name="count" value="3" min="1" aria-describedby="act-err-count">`)

	// With one cluster, there is nothing to change to; a cluster with
	// several workers says each.
	f.st.SetRealms([]cluster.RealmSummary{{Name: "ci", Clusters: 1}})
	f.st.SetClusters("sind", nil)
	f.st.SetNodes("ci", []cluster.NodeSummary{ciNode("worker-0", config.RoleWorker), ciNode("worker-1", config.RoleWorker)})
	f.st.SetSlurm("ci", "pr", &store.Slurm{Info: &cluster.SlurmInfo{Nodes: []slurm.NodeInfo{{Name: "worker-1", State: "idle", CPUs: 1, MemoryMB: 512}}}})
	body = f.get("/dialogs/create-worker?realm=ci&cluster=pr").Body.String()
	assert.NotContains(t, body, "Change…")
	assert.Contains(t, body, "realm ci · 2 workers, 1 CPU and 512 MB each")
	// A cluster that sind ui does not know has no row.
	body = f.get("/dialogs/create-worker?realm=ci&cluster=gone").Body.String()
	assert.NotContains(t, body, "act-ctarget")
}

func TestActClusterRowOf_Deleting(t *testing.T) {
	row := actClusterRowOf(store.Snapshot{}, pgCluster{Realm: "sind", Name: "old", Life: &pgLife{Kind: pgDeleting}})
	assert.Equal(t, "sind deletes it now.", row.Off)
	assert.Equal(t, "0 workers", row.Workers)
}
