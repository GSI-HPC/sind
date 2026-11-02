// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/go-clikit/localweb/page"
	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// New cluster is a page of its own, /clusters/new (actPages), not a
// dialog: the start, the name, the nodes, the files and the other options
// at the left, and the plan at the right, which stays in view. It is the
// form of the action create-cluster, which the routes of the dialogs
// preview and run as they do every dialog's; its links are those of the
// dialog, which sends the browser to the page.

// actPages are the addresses of the actions whose form is a page of its
// own, in place of a dialog: a link to the dialog opens the page.
var actPages = map[string]string{"create-cluster": "/clusters/new"}

// actPageRoute serves the page of the action id. A GET opens it, as a link
// fills it in; the script of the dialogs posts the same link with the
// user's draft (actDraftOf), and gets the form back with it.
func actPageRoute(id string) func(*Server, http.ResponseWriter, *http.Request) {
	return func(s *Server, w http.ResponseWriter, r *http.Request) {
		a, _ := action.Lookup(id) // every page is of an action
		d, ok := actOpen(s, w, r, a)
		if !ok {
			return
		}
		pv := d.withData(d)
		if page.IsHTMX(r) {
			s.Write(w, r, http.StatusOK, "act-page-form", pv)
			return
		}
		pv.Title, pv.Nav, pv.Content = d.Title, "clusters", "act-page"
		pv.Events = s.eventsURL(nil, pv)
		s.Write(w, r, http.StatusOK, "layout", pv)
	}
}

// actPreset is a start of a new cluster: the nodes that a card of the page
// puts into the form, which the user can change from there.
type actPreset struct {
	ID, Title, Text string
	// Roles are the checkbox roles that it ticks; the others it clears.
	Roles []string
	// Workers is the number of workers, in one group.
	Workers int
}

// actPresets are the cards of the start of a new cluster. The first is
// that of a form without nodes of its own.
var actPresets = []actPreset{
	{ID: "quick", Title: "Quick test", Text: "A controller and two workers. Enough to test a batch script.", Workers: 2},
	{ID: "history", Title: "With job history", Text: "Adds a db node for sacct and a login node for sind enter.", Roles: []string{"db", "submitter"}, Workers: 2},
	{ID: "site", Title: "Like a site", Text: "A backup controller, job history, a login node and four workers.", Roles: []string{"backup", "db", "submitter"}, Workers: 4},
}

// actCopyPrefix starts the start of a form that copies the nodes of a
// cluster: copy:REALM/NAME.
const actCopyPrefix = "copy:"

// actPresetOf returns the start of the id: a card of actPresets, or the
// nodes of a cluster that the snapshot has, for copy:REALM/NAME.
func actPresetOf(sn store.Snapshot, id string) (actPreset, bool) {
	for _, s := range actPresets {
		if s.ID == id {
			return s, true
		}
	}
	ref, ok := strings.CutPrefix(id, actCopyPrefix)
	realm, name, _ := strings.Cut(ref, "/")
	c, found := sn.Cluster(realm, name)
	if !ok || !found {
		return actPreset{}, false
	}
	// A cluster needs a worker: one whose workers all went, or whose
	// summary has no count, starts with one.
	s := actPreset{ID: id, Title: c.Name, Workers: max(c.Workers, 1)}
	for _, r := range []struct {
		name string
		has  bool
	}{{"backup", c.Controllers > 1}, {"db", c.DBs > 0}, {"api", c.APIs > 0}, {"submitter", c.Submitters > 0}} {
		if r.has {
			s.Roles = append(s.Roles, r.name)
		}
	}
	return s, true
}

// has reports whether the start ticks the role.
func (s actPreset) has(role string) bool { return slices.Contains(s.Roles, role) }

// Set is what the card puts into the form, as a query: each checkbox role,
// on or empty, and the workers.
func (s actPreset) Set() string {
	q := url.Values{}
	for _, r := range actRoles {
		v := ""
		if s.has(r.Name) {
			v = "on"
		}
		q.Set(r.Name, v)
	}
	q.Set("workers", strconv.Itoa(s.Workers))
	return q.Encode()
}

// Nodes counts the nodes of the start.
func (s actPreset) Nodes() int { return 1 + len(s.Roles) + s.Workers }

// Squares are the classes of the squares that draw the nodes of the start
// on its card, one for each node, at most eight.
func (s actPreset) Squares() []string {
	out := []string{"controller"}
	for _, r := range actRoles {
		if s.has(r.Name) {
			out = append(out, r.Name)
		}
	}
	for range s.Workers {
		out = append(out, "worker")
	}
	return out[:min(len(out), 8)]
}

// fits reports whether the form has the nodes of the start: its roles,
// and one group of its workers.
func (s actPreset) fits(f url.Values) bool {
	for _, r := range actRoles {
		if actOn(f.Get(r.Name)) != s.has(r.Name) {
			return false
		}
	}
	groups := action.WorkerGroups(f)
	return len(groups) == 1 && strings.TrimSpace(f.Get(groups[0]+"-count")) == strconv.Itoa(s.Workers)
}

// actStartOn is the start whose card the page marks: the one that the
// form names in its field start while the form still has its nodes, else
// the first card whose nodes it has, else none.
func actStartOn(sn store.Snapshot, f url.Values) string {
	if s, ok := actPresetOf(sn, f.Get("start")); ok && s.fits(f) {
		if strings.HasPrefix(s.ID, actCopyPrefix) {
			return "copy"
		}
		return s.ID
	}
	for _, s := range actPresets {
		if s.fits(f) {
			return s.ID
		}
	}
	return ""
}

// startDefaults puts the nodes of the start of the link into a form that
// has none of its own.
func (d *actDialog) startDefaults() {
	s, ok := actPresetOf(d.Snap, d.Form.Get("start"))
	if !ok {
		return
	}
	for _, k := range []string{"backup", "db", "api", "submitter", "wg", "workers"} {
		if d.Form.Has(k) {
			return
		}
	}
	for _, r := range actRoles {
		if s.has(r.Name) {
			d.Form.Set(r.Name, "on")
		}
	}
	d.Form.Set("workers", strconv.Itoa(s.Workers))
}

// actStartCard is a card of the start of the page.
type actStartCard struct {
	actPreset
	On bool
}

// Starts are the cards of the start, the one that the form has on.
func (d *actDialog) Starts() []actStartCard {
	on := actStartOn(d.Snap, d.Form)
	out := make([]actStartCard, len(actPresets))
	for i, s := range actPresets {
		out[i] = actStartCard{s, s.ID == on}
	}
	return out
}

// StartOn is the start whose card the page marks, "copy" for a copy.
func (d *actDialog) StartOn() string { return actStartOn(d.Snap, d.Form) }

// StartOn is the start whose card the page marks, as the form now has it.
func (p actPreview) StartOn() string { return actStartOn(p.Snap, p.Form) }

// actCopyChoice is a cluster whose nodes the start can copy.
type actCopyChoice struct {
	actPreset
	Realm, Name string
	// Text names its nodes.
	Text string
}

// Copies are the clusters whose nodes the start can copy, by realm and
// name.
func (d *actDialog) Copies() []actCopyChoice {
	var out []actCopyChoice
	for _, realm := range pgRealmNames(d.Snap, "") {
		cs := d.Snap.Clusters[realm]
		names := make([]string, len(cs))
		for i, c := range cs {
			names[i] = c.Name
		}
		slices.SortFunc(names, natsort.Compare)
		for _, name := range names {
			s, _ := actPresetOf(d.Snap, actCopyPrefix+realm+"/"+name) // a cluster of the snapshot
			parts := append([]string{"controller"}, s.Roles...)
			parts = append(parts, pgPlural(s.Workers, "worker", "workers"))
			out = append(out, actCopyChoice{actPreset: s, Realm: realm, Name: name, Text: strings.Join(parts, " · ")})
		}
	}
	return out
}

// CopyNames names the first clusters that the start can copy, for its
// card: "dev, default, legacy …".
func (d *actDialog) CopyNames() string {
	cs := d.Copies()
	var names []string
	for _, c := range cs[:min(len(cs), 3)] {
		names = append(names, c.Name)
	}
	text := strings.Join(names, ", ")
	if len(cs) > 3 {
		text += fmt.Sprintf(" and %d more", len(cs)-3)
	}
	return text
}

// CopyFoot is the foot of the card of Copy a cluster: the cluster that the
// form copies, or what to do.
func (d *actDialog) CopyFoot() string { return actCopyFoot(d.Snap, d.Form) }

// CopyFoot is the foot of the card of Copy a cluster, as the form now has
// it.
func (p actPreview) CopyFoot() string { return actCopyFoot(p.Snap, p.Form) }

// actCopyFoot names the cluster whose nodes the form has from Copy a
// cluster, "dev · realm sind", or says to pick one.
func actCopyFoot(sn store.Snapshot, f url.Values) string {
	if actStartOn(sn, f) != "copy" {
		return "pick one"
	}
	realm, name, _ := strings.Cut(strings.TrimPrefix(f.Get("start"), actCopyPrefix), "/")
	return name + " · realm " + realm
}

// actRealmOption is a realm that the realm field of the page offers.
type actRealmOption struct {
	Name, Label string
	On          bool
}

// RealmOptions are the realms that sind ui sees, each with its clusters,
// for the realm field of the page; the realm of the form is on.
func (d *actDialog) RealmOptions() []actRealmOption {
	var out []actRealmOption
	for _, r := range pgRealmNames(d.Snap, "") {
		out = append(out, actRealmOption{Name: r, Label: r + " · " + pgPlural(len(d.Snap.Clusters[r]), "cluster", "clusters"), On: r == d.Realm})
	}
	return out
}

// NewRealm reports whether the realm of the form is one that sind ui does
// not see, which the cluster then makes.
func (d *actDialog) NewRealm() bool { return !pgRealmKnown(d.Snap, d.Realm) }

// actRealmNote is what the page says of a realm that sind ui does not
// see: a create makes it, with its mesh network, DNS and SSH relay. Mean
// is a realm of a close name, which the user may have meant.
type actRealmNote struct{ Name, Mean string }

// RealmNote says that the cluster makes a new realm, nil for one that sind
// ui sees, or for the first realm of all, which is no surprise, and in a
// dialog whose realm is no field.
func (p actPreview) RealmNote() *actRealmNote {
	if !p.Spec.RealmField {
		return nil
	}
	realm := action.Realm(p.Form)
	names := pgRealmNames(p.Snap, "")
	if len(names) == 0 || pgRealmKnown(p.Snap, realm) || config.CheckName("realm", realm) != nil {
		return nil
	}
	n := &actRealmNote{Name: realm}
	if c := missClosest(realm, names, 1); len(c) > 0 {
		n.Mean = c[0]
	}
	return n
}

// NameOK says that the name of the new cluster is free, "" while it is
// wrong or taken, or the realm is.
func (p actPreview) NameOK() string {
	name := strings.TrimSpace(p.Form.Get("name"))
	if name == "" || p.Cmd.Config == nil || p.ProblemOf("name") != "" || p.ProblemOf("realm") != "" {
		return ""
	}
	return "Free in realm " + action.Realm(p.Form) + "."
}

// NodeCount counts the containers of the plan, for the head of Nodes.
func (p actPreview) NodeCount() string {
	if p.Cmd.Config == nil {
		return ""
	}
	return pgPlural(p.Cmd.Containers, "container", "containers")
}

// MoreNodes says which nodes under More nodes the form has: "backup, api",
// or "none".
func (p actPreview) MoreNodes() string { return actMoreNodes(p.Form) }

// MoreNodes says which nodes under More nodes the form has.
func (d *actDialog) MoreNodes() string { return actMoreNodes(d.Form) }

// actMoreNodes says which roles under More nodes the form has, and its
// worker groups beyond the first.
func actMoreNodes(f url.Values) string {
	var on []string
	for _, r := range actRoles {
		if r.More && actOn(f.Get(r.Name)) {
			on = append(on, r.Label)
		}
	}
	if n := len(action.WorkerGroups(f)) - 1; n > 0 {
		on = append(on, pgPlural(n, "more worker group", "more worker groups"))
	}
	if len(on) == 0 {
		return "none"
	}
	return strings.Join(on, ", ")
}

// Files is where the files of the jobs of the new cluster are, from the
// fields of its data.
func (p actPreview) Files() pgFiles { return actFilesOf(p.Form) }

// Files is where the files of the jobs of the new cluster are.
func (d *actDialog) Files() pgFiles { return actFilesOf(d.Form) }

// actFilesOf returns where the files of the jobs of the cluster of the
// form are: its host directory as typed, or its volume, which a copy
// reaches through its submitter or, without one, its controller.
func actFilesOf(f url.Values) pgFiles {
	realm, name := action.Realm(f), strings.TrimSpace(f.Get("name"))
	if config.CheckName("cluster", name) != nil {
		name = "NAME"
	}
	m := cluster.MountPoint{Path: strings.TrimSpace(f.Get("mount")), Type: config.StorageHostPath, Source: strings.TrimSpace(f.Get("dir")), OK: true}
	if f.Get("dtype") == "volume" {
		m.Type, m.Source = config.StorageVolume, string(cluster.VolumeName(realm, name, cluster.VolumeData))
	}
	via := "controller"
	if actOn(f.Get("submitter")) {
		via = "submitter"
	}
	return pgFilesOf(m, string(cluster.ContainerName(realm, name, via)))
}

// CopySquares draw the card of Copy a cluster: a cluster of a few nodes.
func (d *actDialog) CopySquares() []string {
	return []string{"controller", "db", "worker", "worker", "worker"}
}
