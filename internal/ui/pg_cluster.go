// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// pgClusterPage is the page of one cluster, template pg_cluster.tmpl. Its
// head and its body are fragments that watch the cluster's own topics.
var pgClusterPage = Page{
	Name: "cluster", Pattern: "GET /clusters/{realm}/{cluster}", Title: "Cluster", Nav: "clusters",
	Params: []string{"tab"}, Fragments: []string{"pg-cluster-head", "pg-cluster-body"},
	Load: func(v *View) error {
		d, err := pgLoadCluster(v)
		if err != nil {
			return err
		}
		if !d.Found {
			return pgClusterMiss(v.Snap, d.Realm, d.Name, "")
		}
		v.Title = pgClusterTitle(d)
		return nil
	},
}

// pgClusterTitle is the title of a cluster page: the cluster, after its tab
// unless that is the summary, so that each tab has a title of its own.
func pgClusterTitle(d *pgClusterView) string {
	for _, t := range d.Tabs {
		if t.On && t.Key != "summary" {
			return t.Label + " · " + d.Name
		}
	}
	return d.Name
}

// pgClusterTopics returns the topics a cluster page keeps: the cluster's
// own, and the ones that tab shows. The logs tab keeps none, as a new
// render of it would end the stream that it holds.
func pgClusterTopics(p Params, tab string) []store.Topic {
	realm, name := p.Get("realm"), p.Get("cluster")
	switch tab {
	case "", "summary":
		return []store.Topic{store.ClustersTopic(realm), store.NodesTopic(realm), store.StatusTopic(realm, name), store.MonitoringTopic(realm), store.MeshTopic(realm)}
	case "nodes":
		return []store.Topic{store.ClustersTopic(realm), store.NodesTopic(realm), store.StatusTopic(realm, name)}
	case "network":
		return []store.Topic{store.ClustersTopic(realm), store.StatusTopic(realm, name), store.NetworksTopic(realm), store.VolumesTopic(realm)}
	case "connect":
		return []store.Topic{store.ClustersTopic(realm), store.NodesTopic(realm), store.MonitoringTopic(realm)}
	}
	return nil
}

func pgClusterFragments() []Fragment {
	params := []string{"realm", "cluster", "tab"}
	load := func(v *View) (err error) { v.Data, err = pgLoadCluster(v); return }
	return []Fragment{
		{
			Name: "pg-cluster-head", Params: params, Topics: []store.Topic{store.TopicRuns},
			Watch: func(p Params) []store.Topic {
				realm, name := p.Get("realm"), p.Get("cluster")
				return []store.Topic{store.ClustersTopic(realm), store.NodesTopic(realm), store.StatusTopic(realm, name)}
			},
			Load: load,
		},
		{
			// The Logs tab reads its own keys too, so that the body that a
			// stream sends first is the one that the page holds.
			Name: "pg-cluster-body", Params: append(slices.Clone(params), logsTabParams...),
			Watch: func(p Params) []store.Topic { return pgClusterTopics(p, p.Get("tab")) },
			Load:  load,
		},
	}
}

// pgTabLink is a tab of the cluster page.
type pgTabLink struct {
	Key, Label, URL string
	On              bool
	// Count is the number after the label, Bad marks it.
	Count string
	Bad   bool
}

// pgMatrixRow is a node row of the health matrix.
type pgMatrixRow struct {
	Node pgNode
	// Cells hold, for each column of the matrix, "y" for a service that
	// is up, "n" for one that is down, "na" for none or for a node that
	// does not run.
	Cells []pgCell
}

// pgCell is a cell of the health matrix.
type pgCell struct {
	Class string
	Mark  string
}

// pgNetRow is a network of the Network tab.
type pgNetRow struct {
	Name, Driver, Subnet, Gateway string
	Mine                          bool
}

// pgCmd is a command to copy.
type pgCmd struct {
	Label string
	Text  string
}

// pgClusterView is what a cluster page shows.
type pgClusterView struct {
	// Found is false when the snapshot knows no such cluster.
	Found   bool
	Realm   string
	Name    string
	Tab     string
	Cluster pgCluster
	// Status is the cluster's status, nil until the first probe.
	Status  *cluster.Status
	Tabs    []pgTabLink
	Actions []pgAction
	// Services are the columns of the health matrix, Matrix its rows.
	Services []string
	Matrix   []pgMatrixRow
	// Selection holds the dialogs that the nodes tab offers for the
	// selected nodes, WorkerSelection and Delete those it offers while only
	// workers are selected.
	Selection       []pgAction
	WorkerSelection []pgAction
	Delete          pgAction
	// Network tab.
	Networks []pgNetRow
	Mounts   []cluster.MountPoint
	Volumes  []cluster.VolumeSummary
	HostData string
	// Data is the cluster's data mount, when the status has it.
	Data *cluster.MountPoint
	// Connect tab.
	Commands []pgCmd
	API      string
	Grafana  string
	HA       []string
}

// pgLoadCluster builds the view of a cluster page from the view's
// Params (realm, cluster, tab).
func pgLoadCluster(v *View) (*pgClusterView, error) {
	realm, name, tab := v.Params.Get("realm"), v.Params.Get("cluster"), v.Params.Get("tab")
	d := &pgClusterView{Realm: realm, Name: name, Tab: tab}
	if tab == "" {
		d.Tab = "summary"
	}
	sn := v.Snap
	summary, ok := sn.Cluster(realm, name)
	if !ok {
		return d, nil
	}
	if !pgValidTab(d.Tab) {
		return nil, pgTabMiss(realm, name, tab)
	}
	d.Found = true
	d.Cluster = pgClusterOf(sn, realm, summary, sn.MonitoringOf(realm), v.Now)
	d.Status = sn.Status(realm, name)
	d.Tabs = pgTabLinks(d)
	d.Actions = []pgAction{
		pgDialogAction("Add workers…", "", "create-worker", "realm", realm, "cluster", name),
		pgDialogAction("Delete cluster…", "danger", "delete-cluster", "realm", realm, "cluster", name),
	}
	switch d.Tab {
	case "summary":
		pgSummaryTab(d)
	case "nodes":
		pgNodesTab(d)
	case "network":
		pgNetworkTab(d, sn)
	case "connect":
		pgConnectTab(d, sn)
	}
	return d, nil
}

func pgTabLinks(d *pgClusterView) []pgTabLink {
	problems := 0
	for _, n := range d.Cluster.Nodes {
		if n.Problem() {
			problems++
		}
	}
	var tabs []pgTabLink
	for _, t := range pgTabs {
		l := pgTabLink{Key: t.Key, Label: t.Label, URL: pgClusterURL(d.Realm, d.Name, t.Key), On: t.Key == d.Tab}
		if t.Key == "nodes" {
			l.Count = fmt.Sprint(len(d.Cluster.Nodes))
			if problems > 0 {
				l.Count, l.Bad = "!", true
			}
		}
		tabs = append(tabs, l)
	}
	return tabs
}

// pgSummaryTab fills the health matrix, the data mount and the pair of
// controllers.
func pgSummaryTab(d *pgClusterView) {
	for _, s := range pgServiceOrder {
		if slices.ContainsFunc(d.Cluster.Nodes, func(n pgNode) bool { _, ok := n.Service(s); return ok }) {
			d.Services = append(d.Services, s)
		}
	}
	for _, n := range d.Cluster.Nodes {
		row := pgMatrixRow{Node: n}
		for _, s := range d.Services {
			up, ok := n.Service(s)
			switch {
			case !ok:
				row.Cells = append(row.Cells, pgCell{"na", "·"})
			case n.State != cluster.StateRunning:
				row.Cells = append(row.Cells, pgCell{"na", "–"})
			case up:
				row.Cells = append(row.Cells, pgCell{"y", "✓"})
			default:
				row.Cells = append(row.Cells, pgCell{"n", "✗"})
			}
		}
		d.Matrix = append(d.Matrix, row)
		if n.HA != nil {
			role := "standby"
			if n.HA.InControl {
				role = "in control"
			}
			d.HA = append(d.HA, fmt.Sprintf("%s: %s, %s", n.Short, n.HA.Position, role))
		}
	}
	if d.Status != nil {
		for i, m := range d.Status.Mounts {
			if m.Path == "/data" {
				d.Data = &d.Status.Mounts[i]
			}
		}
	}
}

// pgNodesTab prepares the dialogs that the selection of nodes opens. The
// page script appends the selected nodes, as ?nodes=worker-0,worker-1.
func pgNodesTab(d *pgClusterView) {
	power := func(label, class, op string) pgAction {
		return pgDialogAction(label, class, "power", "realm", d.Realm, "cluster", d.Name, "op", op)
	}
	d.Selection = []pgAction{
		power("Start…", "", "start"), power("Shut down…", "", "stop"), power("Reboot…", "", "reboot"),
		power("Freeze…", "", "pause"), power("Unfreeze…", "", "unpause"),
	}
	d.WorkerSelection = []pgAction{power("Drain…", "", "drain"), power("Resume…", "", "resume")}
	// The page script names the selected workers in the label.
	d.Delete = pgDialogAction("Delete workers…", "danger", "delete-worker", "realm", d.Realm, "cluster", d.Name)
}

// pgNetworkTab fills the networks, the mounts and the volumes. The
// status holds the most; the snapshot's lists are what remains before the
// first probe.
func pgNetworkTab(d *pgClusterView, sn store.Snapshot) {
	mine := string(cluster.NetworkName(d.Realm, d.Name))
	mesh := d.Realm + "-mesh"
	for _, n := range sn.NetworksOf(d.Realm) {
		if n.Name == mine || n.Name == mesh {
			d.Networks = append(d.Networks, pgNetRow{n.Name, n.Driver, n.Subnet, n.Gateway, n.Name == mine})
		}
	}
	if h := d.Status; h != nil && h.Network != nil && len(d.Networks) == 0 {
		nh := h.Network
		d.Networks = []pgNetRow{
			{nh.ClusterName, nh.ClusterDriver, nh.ClusterSubnet, nh.ClusterGateway, true},
			{nh.MeshName, nh.MeshDriver, nh.MeshSubnet, nh.MeshGateway, false},
		}
	}
	if d.Status != nil {
		d.Mounts = d.Status.Mounts
		for _, m := range d.Mounts {
			if m.Path == "/data" && m.Type == config.StorageHostPath {
				d.HostData = m.Source
			}
		}
		return
	}
	prefix := cluster.ContainerPrefix(d.Realm, d.Name)
	for _, vol := range sn.VolumesOf(d.Realm) {
		if strings.HasPrefix(vol.Name, prefix) {
			d.Volumes = append(d.Volumes, vol)
		}
	}
}

// pgConnectTab writes the commands that enter, run on and log in to the
// cluster, and the addresses of its services.
func pgConnectTab(d *pgClusterView, sn store.Snapshot) {
	sind, cl := pgSind(d.Realm), pgClusterArg(d.Name)
	entry := "controller"
	var first, api *pgNode
	for i, n := range d.Cluster.Nodes {
		switch n.Role {
		case config.RoleSubmitter:
			entry = "submitter"
		case config.RoleAPI:
			api = &d.Cluster.Nodes[i]
		}
		if first == nil || n.Role == config.RoleWorker && first.Role != config.RoleWorker {
			first = &d.Cluster.Nodes[i]
		}
	}
	d.Commands = []pgCmd{
		{"Open a shell on the " + entry, fmt.Sprintf("%s enter%s", sind, cl)},
		{"Run one command", fmt.Sprintf("%s exec%s -- sinfo", sind, cl)},
	}
	if first != nil {
		d.Commands = append(d.Commands,
			pgCmd{"Log in to a node", fmt.Sprintf("%s ssh %s", sind, first.Arg)},
			pgCmd{"Follow the log of a node", fmt.Sprintf("%s logs %s -f -n 200", sind, first.Arg)},
			pgCmd{"Plain ssh, with the realm's ssh config", fmt.Sprintf("ssh -F \"$(%s get ssh-config)\" %s", sind, first.FQDN)},
		)
	}
	if api != nil {
		d.API = "http://" + api.FQDN + ":6820"
	}
	if m := sn.MonitoringOf(d.Realm); m != nil && d.Cluster.Monitoring {
		for _, s := range m.Services {
			if s.Name == "grafana" {
				d.Grafana = s.URL
			}
		}
	}
}

// NodesText returns "1 node" or "3 nodes".
func (d *pgClusterView) NodesText() string { return pgPlural(len(d.Cluster.Nodes), "node", "nodes") }

// ClusterArg returns the CLUSTER argument of a command: " dev", empty for
// the default cluster.
func (d *pgClusterView) ClusterArg() string { return pgClusterArg(d.Name) }
