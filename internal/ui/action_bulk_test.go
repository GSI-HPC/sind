// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// actOnceStarter starts the first task and fails the others.
type actOnceStarter struct {
	actFakeStarter
	once sync.Once
}

func (s *actOnceStarter) Start(spec run.Spec) (string, error) {
	started := false
	s.once.Do(func() { started = true })
	if _, err := s.actFakeStarter.Start(spec); err != nil || !started {
		return "", errors.New("no more")
	}
	return "task1", nil
}

func TestActDeleteClusters_Dialog(t *testing.T) {
	f := newActFixture(t)
	w := f.get("/dialogs/delete-clusters?clusters=sind/dev&clusters=sind/default&clusters=sind/gone&back=/clusters?realm=sind")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	// The title, the count to type and the form count the clusters that
	// the dialog keeps; it names the one that it leaves out. The last
	// clusters of a realm take it along, and the title says so.
	assert.Contains(t, body, "<h2 id=\"act-title\">Delete 2 clusters and realm sind</h2>")
	assert.Contains(t, body, "These are the last clusters of realm sind, so the realm goes too. This cannot be undone.")
	assert.Contains(t, body, `<input type="hidden" name="back" value="/clusters?realm=sind">`)
	assert.Contains(t, body, `<input type="hidden" name="clusters" value="sind/dev"><input type="hidden" name="clusters" value="sind/default">`)
	assert.NotContains(t, body, `value="sind/gone"`, "the dialog keeps the clusters that it knows")
	assert.Contains(t, body, `<div class="callout warn">sind ui no longer sees <span class="mono">sind/gone</span>, so the dialog leaves it out.</div>`)
	assert.Contains(t, body, `<form class="modal wide"`, "the table of what goes needs the room")
	assert.Contains(t, body, `Type <b class="mono" id="act-confirm-word">2</b> to confirm`)
	assert.Contains(t, body, `hx-post="/actions/delete-clusters/run"`)

	// One cluster is one.
	body = f.get("/dialogs/delete-clusters?clusters=sind/dev&clusters=sind/gone&clusters=sind/old").Body.String()
	assert.Contains(t, body, "<h2 id=\"act-title\">Delete 1 cluster</h2>")
	assert.Contains(t, body, "<p id=\"act-lead\">This cannot be undone.</p>")
	assert.Contains(t, body, "so the dialog leaves them out.")
	assert.Contains(t, body, `Type <b class="mono" id="act-confirm-word">1</b> to confirm`)

	// Without a cluster it knows, the dialog sends the user to the list,
	// and asks nothing to type: the field hides while its word is empty.
	body = f.get("/dialogs/delete-clusters?clusters=sind/gone").Body.String()
	assert.Contains(t, body, "<h2 id=\"act-title\">Delete clusters</h2>")
	assert.Contains(t, body, `<div class="empty">sind ui no longer sees <span class="mono">sind/gone</span>. No cluster to delete. Tick clusters on <a href="/clusters">the Clusters page</a> and delete them from the bar above the table.</div>`)
	assert.Contains(t, body, `<b class="mono" id="act-confirm-word"></b> to confirm`)
	body = f.get("/dialogs/delete-clusters").Body.String()
	assert.Contains(t, body, "Deletes several clusters with one confirmation.")
	assert.Contains(t, body, `<div class="empty">No cluster to delete.`)
	assert.Contains(t, f.post("/actions/delete-clusters/preview", actForm("clusters", "sind/gone")).Body.String(), "realm sind has no cluster gone")
	assert.Contains(t, f.post("/actions/delete-clusters/preview", actForm()).Body.String(), "Pick the clusters to delete.")
}

func TestActDeleteClusters_Run(t *testing.T) {
	f := newActFixture(t)
	form := actForm("clusters", "sind/dev", "clusters", "sind/default", "back", "/clusters?realm=sind&q=d")
	preview := f.post("/actions/delete-clusters/preview", form).Body.String()
	assert.Contains(t, preview, actCode("sind delete cluster\nsind delete cluster dev"))
	assert.Contains(t, preview, `<tr><td class="act-lc"><b class="mono">realm sind</b><small>no cluster is left</small></td>`)
	assert.Contains(t, preview, `<h2 id="act-title" hx-swap-oob="true">Delete 2 clusters and realm sind</h2>`)
	assert.Contains(t, preview, `>Delete 2 clusters<kbd`, "the button names what it deletes")
	assert.Contains(t, preview, "The button stays off until you type the number.", "a count is no name")
	assert.Contains(t, f.post("/actions/delete-clusters/preview", form+"&confirm=3").Body.String(), "3 is not 2. Type the number as it is shown.")
	shown := f.shown(t, "delete-clusters", form)

	w := f.postHTMX("/actions/delete-clusters/run", form+"&shown="+shown+"&confirm=2 ")
	assert.Equal(t, "/clusters?realm=sind&q=d", w.Header().Get("HX-Redirect"), w.Body.String())
	require.Len(t, f.starter.specs, 2)
	assert.Equal(t, run.Spec{Action: "delete-cluster", Argv: []string{"sind", "delete", "cluster"}, Display: "sind delete cluster", Realm: "sind", Cluster: "default",
		Form: url.Values{"realm": {"sind"}, "cluster": {"default"}}}, f.starter.specs[0])
	assert.Equal(t, "dev", f.starter.specs[1].Cluster)

	// A wrong count runs nothing; a back that is not the Clusters page goes
	// there.
	f.starter.specs = nil
	w = f.postHTMX("/actions/delete-clusters/run", form+"&shown="+shown+"&confirm=3")
	assert.Contains(t, w.Body.String(), "Type 2 to confirm.")
	assert.Empty(t, f.starter.specs)
	for back, want := range map[string]string{"https://evil.example/": "/clusters", "/clustersx": "/clusters", "/clusters": "/clusters", "": "/clusters"} {
		form := actForm("clusters", "sind/dev", "back", back)
		w = f.postHTMX("/actions/delete-clusters/run", form+"&shown="+f.shown(t, "delete-clusters", form)+"&confirm=1")
		assert.Equal(t, want, w.Header().Get("HX-Redirect"), back)
	}
}

func TestActDeleteClusters_StartFails(t *testing.T) {
	f := newActFixture(t)
	s := &actOnceStarter{}
	f.state.starter = s
	form := actForm("clusters", "sind/dev,sind/default")
	w := f.postHTMX("/actions/delete-clusters/run", form+"&shown="+f.shown(t, "delete-clusters", form)+"&confirm=2")
	assert.Empty(t, w.Header().Get("HX-Redirect"))
	assert.Contains(t, w.Body.String(), "started 1 of 2 commands: no more")
	assert.Len(t, s.specs, 2)

	f.state.starter = &actFakeStarter{startErr: errors.New("down")}
	form = actForm("cluster", "dev")
	w = f.postHTMX("/actions/delete-cluster/run", form+"&shown="+f.shown(t, "delete-cluster", form)+"&confirm=dev")
	assert.Contains(t, w.Body.String(), "down")
}

func TestActDeleteClusters_Lock(t *testing.T) {
	f := newActFixture(t)
	f.st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "ci", Clusters: 1}})
	f.st.SetClusters("ci", []cluster.Summary{{Name: "ci1"}})
	f.st.SetLock("ci", &state.LockHolder{Command: "sind create cluster", Host: "h", PID: 1, Since: time.Now()})
	body := f.post("/actions/delete-clusters/preview", actForm("clusters", "sind/dev,ci/ci1")).Body.String()
	assert.Contains(t, body, `Another command holds the lock of realm <b class="mono">ci</b>`)
	body = f.post("/actions/delete-clusters/preview", actForm("clusters", "sind/dev")).Body.String()
	assert.NotContains(t, body, "Another command holds the lock", "the realm of the form is not one of the clusters'")
	f.st.SetLock("sind", &state.LockHolder{Command: "sind create cluster", Host: "h", PID: 1, Since: time.Now()})
	body = f.post("/actions/delete-clusters/preview", actForm("realm", "ci", "clusters", "sind/dev")).Body.String()
	assert.Contains(t, body, `lock of realm <b class="mono">sind</b>`)
}

func TestActDeleteClusters_NamesTheRealmsThatGo(t *testing.T) {
	f := newActFixture(t)
	f.st.SetRealms([]cluster.RealmSummary{{Name: "sind", Clusters: 2}, {Name: "ci", Clusters: 1}})
	f.st.SetClusters("ci", []cluster.Summary{{Name: "ci1"}})
	body := f.get("/dialogs/delete-clusters?clusters=sind/dev&clusters=sind/default&clusters=ci/ci1").Body.String()
	assert.Contains(t, body, "<h2 id=\"act-title\">Delete 3 clusters and realms ci and sind</h2>")
	assert.Contains(t, body, "These are the last clusters of their realms, so the realms go too. This cannot be undone.")

	assert.Empty(t, actPreview{}.AlsoStays())
	assert.Equal(t, "the images, the volumes.", actPreview{Cmd: action.Command{Stays: []string{"the images", "the volumes"}}}.AlsoStays())
}
