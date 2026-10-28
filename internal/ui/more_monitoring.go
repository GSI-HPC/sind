// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/doctor"
	"github.com/GSI-HPC/go-clikit/localweb/page"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/observability"
)

// moreInterval is the interval at which the monitoring stacks are read;
// Set takes effect at once.
type moreInterval interface {
	Get() time.Duration
	Set(d time.Duration)
}

// moreIntervalChoices are the intervals that the Monitoring page offers.
var moreIntervalChoices = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}

func init() {
	// The picker reads the interval that a POST set, to say it saved it.
	defaultRegistry.AddFragment(Fragment{Name: "more-mon-interval", Params: []string{"interval"}, Load: moreIntervalLoad})
	defaultRegistry.AddRoute(Route{Pattern: "POST /monitoring/interval", Handle: moreIntervalSet})
	defaultRegistry.AddFragment(Fragment{
		Name:     "more-monitoring",
		Topics:   []store.Topic{store.TopicRealms, store.TopicDocker, store.TopicDoctor},
		Prefixes: []string{"monitoring/", "nodes/"},
		Params:   []string{"realm"},
		Load:     moreMonitoringLoad,
	})
	// A list page reads the realm cookie, which only a route can see.
	defaultRegistry.AddRoute(pgRealmRoute(Page{
		Name: "more-monitoring", Pattern: "GET /monitoring", Title: "Monitoring", Nav: "monitoring",
		Params: []string{"realm"}, Fragments: []string{"more-monitoring"},
	}, true, false))
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
	// HostDNS says whether this host resolves the names under .sind,
	// which the Open links use: "ok", "off", or "" when no check says.
	HostDNS string
	// Forward is the address of a Grafana, for the ssh -L example.
	Forward string
}

// moreMonRealm is the monitoring stack of one realm.
type moreMonRealm struct {
	Name     string
	Services []moreMonService
	Clusters []moreMonCluster
}

// moreMonService is a service of a stack, as the table draws it.
type moreMonService struct {
	cluster.MonitoringService
	// Pill is the colour class and Text the words of the state.
	Pill, Text string
	// Open is set when the service has a page to open in a new tab.
	Open bool
	// Addr is the service's address with its port, for a browser that
	// does not resolve the URL's name; empty when the URL has the address.
	Addr string
}

// moreMonCluster is a monitored cluster with its key numbers.
type moreMonCluster struct {
	cluster.MonitoredCluster
	// Running and Total count the cluster's node containers.
	Running, Total int
	// Take is the number of Slurm's nodes that take jobs: neither down nor
	// in drain (drained or draining), as Ready for jobs counts them; only
	// with Slurm. A node can be down and in drain at once, and the counts
	// do not tell how many are both, so it may count too few.
	Take int
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
func moreMonitoringCount(sn store.Snapshot, _ time.Time) NavCount {
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

// moreHostDNS says whether this host resolves the names under .sind, by
// the doctor's DNS policy check: "ok" when it passed, "off" when it did
// not, "" when there is no such check, as without systemd-resolved.
func moreHostDNS(sn store.Snapshot) string {
	if sn.Doctor == nil {
		return ""
	}
	for _, c := range sn.Doctor.Checks {
		if c.Name == "DNS policy" {
			if c.Status == doctor.StatusOK {
				return "ok"
			}
			return "off"
		}
	}
	return ""
}

// moreServiceAddr returns the address and port of a service whose URL
// names it by DNS, "" when its URL has the address or it has none.
func moreServiceAddr(s cluster.MonitoringService) string {
	if s.IP == "" || strings.Contains(s.URL, "//"+s.IP+":") {
		return ""
	}
	return s.IP + ":" + s.Name.Port()
}

// moreMonClusterOf returns a monitored cluster with the count of its node
// containers and of its Slurm nodes that take jobs.
func moreMonClusterOf(sn store.Snapshot, realm string, c cluster.MonitoredCluster) moreMonCluster {
	mc := moreMonCluster{MonitoredCluster: c}
	for _, n := range sn.ClusterNodes(realm, c.Name) {
		mc.Total++
		if n.State == cluster.StateRunning {
			mc.Running++
		}
	}
	if s := c.Slurm; s != nil {
		mc.Take = max(s.Nodes-s.NodesDown-s.NodesDrain, 0)
	}
	return mc
}

// moreMonitoringLoad fills the data of the Monitoring page.
func moreMonitoringLoad(v *View) error {
	realms := moreRealms(v)
	d := moreMonitoringData{Multi: len(realms) > 1, Realm: v.Params.Get("realm"), HostDNS: moreHostDNS(v.Snap)}
	d.NewURL = pgDialog("create-cluster", "realm", d.Realm)
	if len(realms) == 0 {
		d.Notice = moreNoRealms(v.Snap.Docker)
	} else {
		d.DockerErr = moreDockerDown(v.Snap.Docker)
	}
	for _, name := range realms {
		m := v.Snap.Monitorings[name]
		if m == nil {
			d.Without = append(d.Without, name)
			continue
		}
		r := moreMonRealm{Name: name}
		for _, c := range m.Clusters {
			r.Clusters = append(r.Clusters, moreMonClusterOf(v.Snap, name, c))
		}
		for _, s := range m.Services {
			ms := moreMonService{MonitoringService: s, Addr: moreServiceAddr(s)}
			ms.Pill, ms.Text = moreServiceState(s)
			ms.Open = s.State == docker.StateRunning && s.Name != observability.ServiceAlloy &&
				(strings.HasPrefix(s.URL, "http://") || strings.HasPrefix(s.URL, "https://"))
			if s.Name == observability.ServiceGrafana && ms.Addr != "" && d.Forward == "" {
				d.Forward = ms.Addr
			}
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
	// Saved is the interval that the request set, "" when it set none.
	Saved string
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
	iv := moreEnvGet[moreInterval](ColMonitoringIntervalKey)
	if iv == nil {
		return nil
	}
	current := iv.Get()
	var d moreIntervalData
	for _, c := range moreIntervalOptions(current) {
		d.Choices = append(d.Choices, moreIntervalChoice{Value: moreDuration(c), Selected: c == current})
	}
	if v.Params.Get("interval") != "" {
		d.Saved = moreDuration(current)
	}
	v.Data = d
	return nil
}

// moreIntervalSet is POST /monitoring/interval: it sets the interval to
// one that the picker offers, and answers with the picker, or a plain form
// with a 303 to the page.
func moreIntervalSet(s *Server, w http.ResponseWriter, r *http.Request) {
	iv := moreEnvGet[moreInterval](ColMonitoringIntervalKey)
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
