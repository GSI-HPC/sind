// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb/page"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/observability"
)

// moreIntervalKey is the Env key of the interval at which the monitoring
// stacks are read, a moreInterval.
const moreIntervalKey = "collect.monitoring-interval"

// moreInterval is the interval at which the monitoring stacks are read;
// Set takes effect at once.
type moreInterval interface {
	Get() time.Duration
	Set(d time.Duration)
}

// moreIntervalOf returns the interval at which the monitoring stacks are
// read, nil when no service provides it.
func moreIntervalOf() moreInterval {
	env := moreEnv.Load()
	if env == nil {
		return nil
	}
	iv, _ := env.Get(moreIntervalKey).(moreInterval)
	return iv
}

// moreIntervalChoices are the intervals that the Monitoring page offers.
var moreIntervalChoices = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}

func init() {
	RegisterFragment(Fragment{Name: "more-mon-interval", Load: moreIntervalLoad})
	RegisterRoute(Route{Pattern: "POST /monitoring/interval", Handle: moreIntervalSet})
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

// moreMonitoringCount is the badge behind the rail entry: the services of
// all stacks that are not ready, as "1 down" (or "1 starting" when none
// is down), or a dot when all are ready. Without a stack it shows none.
func moreMonitoringCount(sn store.Snapshot) NavCount {
	n, down, starting := 0, 0, 0
	for _, m := range sn.Monitorings {
		for _, s := range m.Services {
			n++
			switch class, _ := moreServiceState(s); class {
			case "ok":
			case "bad":
				down++
			default:
				starting++
			}
		}
	}
	switch {
	case down > 0:
		return NavCount{Text: strconv.Itoa(down) + " down", Class: "bad", Label: pgPlural(down, "monitoring service is", "monitoring services are") + " down"}
	case starting > 0:
		return NavCount{Text: strconv.Itoa(starting) + " starting", Class: "warn", Label: pgPlural(starting, "monitoring service is", "monitoring services are") + " not ready yet"}
	case n > 0:
		return NavCount{Class: "ok", Label: pgPlural(n, "monitoring service is", "monitoring services are") + " ready"}
	}
	return NavCount{}
}

// moreMonitoringLoad fills the data of the Monitoring page.
func moreMonitoringLoad(v *View) error {
	realms := moreRealms(v)
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

// moreIntervalData is the interval picker of the Monitoring page.
type moreIntervalData struct {
	Choices []moreIntervalChoice
}

// moreIntervalChoice is an option of the picker.
type moreIntervalChoice struct {
	Value    string
	Selected bool
}

// moreIntervalOptions returns the intervals to offer: the choices, and the
// current one if it is none of them.
func moreIntervalOptions(current time.Duration) []time.Duration {
	out := slices.Clone(moreIntervalChoices)
	if !slices.Contains(out, current) {
		out = append(out, current)
		slices.Sort(out)
	}
	return out
}

// moreDuration writes d as "30s", "1m" or "1h30m", without the zero units
// that time.Duration.String adds.
func moreDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// moreIntervalLoad fills the picker; with no interval to change, it draws
// nothing.
func moreIntervalLoad(v *View) error {
	iv := moreIntervalOf()
	if iv == nil {
		return nil
	}
	current := iv.Get()
	var d moreIntervalData
	for _, c := range moreIntervalOptions(current) {
		d.Choices = append(d.Choices, moreIntervalChoice{Value: moreDuration(c), Selected: c == current})
	}
	v.Data = d
	return nil
}

// moreIntervalSet is POST /monitoring/interval: it sets the interval to
// one that the picker offers, and answers with the picker, or a plain form
// with a 303 to the page.
func moreIntervalSet(s *Server, w http.ResponseWriter, r *http.Request) {
	iv := moreIntervalOf()
	if iv == nil {
		s.Fail(w, r, ErrNotFound)
		return
	}
	v, err := s.View(r, "interval")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	d, err := time.ParseDuration(v.Params.Get("interval"))
	if err != nil || !slices.Contains(moreIntervalOptions(iv.Get()), d) {
		s.Fail(w, r, fmt.Errorf("interval %q is not offered: %w", v.Params.Get("interval"), ErrBadRequest))
		return
	}
	iv.Set(d)
	if !page.IsHTMX(r) {
		http.Redirect(w, r, "/monitoring", http.StatusSeeOther)
		return
	}
	s.Fragment(w, r, "more-mon-interval")
}
