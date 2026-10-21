// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/observability"
)

func init() {
	RegisterFragment(Fragment{
		Name:     "more-monitoring",
		Topics:   []store.Topic{store.TopicRealms, store.TopicDocker},
		Prefixes: []string{"monitoring/"},
		Params:   []string{"realm"},
		Load:     moreMonitoringLoad,
	})
	// A list page reads the realm cookie, which only a route can see.
	RegisterRoute(pgListRoute(Page{
		Name: "more-monitoring", Pattern: "GET /monitoring", Title: "Monitoring", Nav: "monitoring",
		Params: []string{"realm"}, Fragments: []string{"more-monitoring"},
	}))
}

// moreMonitoringData is what the Monitoring page shows.
type moreMonitoringData struct {
	// Realms are the realms that have a stack; Without those that have
	// none.
	Realms  []moreMonRealm
	Without []string
	Multi   bool
	// Empty is set when no realm shown has a stack: the page then says
	// how to get one.
	Empty bool
	// Realm is the realm that the empty state is about, "" for all.
	Realm string
	// NewURL opens the New cluster dialog.
	NewURL string
	// Notice is set when there is no realm to show.
	Notice    moreNotice
	DockerErr string
}

// moreMonRealm is the monitoring stack of one realm.
type moreMonRealm struct {
	Name     string
	Services []moreMonService
	Clusters []cluster.MonitoredCluster
}

// moreMonService is a service of a stack, as the table draws it.
type moreMonService struct {
	cluster.MonitoringService
	// Pill is the colour class and Text the words of the state.
	Pill, Text string
	// Open is set when the service has a page to open in a new tab.
	Open bool
}

// moreServiceState says how a service stands. A service that runs and
// that was checked is ready or starting; others show their container state.
func moreServiceState(s cluster.MonitoringService) (class, text string) {
	switch s.State {
	case docker.StateRunning:
		switch {
		case !s.Checked:
			return "ok", "running"
		case s.Ready:
			return "ok", "ready"
		}
		return "warn", "starting"
	case cluster.ServiceMissing, docker.StateExited, docker.StateDead:
		return "bad", string(s.State)
	case docker.StatePaused:
		return "warn", "paused"
	case docker.StateCreated:
		return "run", string(s.State)
	}
	return "", string(s.State)
}

// moreMonitoringCount is the count behind the rail entry: the services of
// all stacks.
func moreMonitoringCount(sn store.Snapshot) NavCount {
	n := 0
	for _, m := range sn.Monitorings {
		n += len(m.Services)
	}
	if n == 0 {
		return NavCount{Text: "–"}
	}
	return NavCount{Text: strconv.Itoa(n)}
}

// moreMonitoringLoad fills the data of the Monitoring page.
func moreMonitoringLoad(v *View) error {
	realms, err := moreRealms(v)
	if err != nil {
		return err
	}
	d := moreMonitoringData{Multi: len(realms) > 1, Realm: v.Params.Get("realm")}
	d.NewURL = moreDialogURL("create-cluster", "realm", d.Realm)
	if len(realms) == 0 {
		d.Notice = moreNoRealms(v.Snap.Docker)
	} else {
		d.DockerErr = moreDockerDown(v.Snap.Docker)
	}
	for _, name := range realms {
		m := v.Snap.MonitoringOf(name)
		if m == nil {
			d.Without = append(d.Without, name)
			continue
		}
		r := moreMonRealm{Name: name, Clusters: m.Clusters}
		for _, s := range m.Services {
			ms := moreMonService{MonitoringService: s}
			ms.Pill, ms.Text = moreServiceState(s)
			ms.Open = s.State == docker.StateRunning && s.Name != observability.ServiceAlloy &&
				(strings.HasPrefix(s.URL, "http://") || strings.HasPrefix(s.URL, "https://"))
			r.Services = append(r.Services, ms)
		}
		d.Realms = append(d.Realms, r)
	}
	d.Empty = len(d.Realms) == 0 && len(realms) > 0
	v.Data = d
	return nil
}
