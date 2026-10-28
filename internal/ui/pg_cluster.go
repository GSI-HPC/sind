// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/slurm"
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
// own, the runs, which say what a command creates or deletes, and the ones
// that tab shows. The logs tab keeps none, as a new render of it would end
// the stream that it holds.
func pgClusterTopics(p Params, tab string) []store.Topic {
	realm := p.Get("realm")
	topics := []store.Topic{store.ClustersTopic(realm), store.NodesTopic(realm), store.StatusTopic(realm, p.Get("cluster")), store.TopicRuns}
	switch tab {
	case "", "summary":
		return append(topics, store.MonitoringTopic(realm), store.MeshTopic(realm))
	case "nodes", "slurm":
		return topics
	case "network":
		return append(topics, store.NetworksTopic(realm), store.VolumesTopic(realm), store.MeshTopic(realm))
	case "connect":
		return append(topics[:2], store.MonitoringTopic(realm))
	}
	return nil
}

// pgClusterWatch returns the topics of the head of a cluster or node page:
// those of the cluster, its nodes, its status and the runs.
func pgClusterWatch(p Params) []store.Topic { return pgClusterTopics(p, "nodes") }

func pgClusterFragments() []Fragment {
	params := []string{"realm", "cluster", "tab"}
	load := func(v *View) (err error) { v.Data, err = pgLoadCluster(v); return }
	return []Fragment{
		{
			Name: "pg-cluster-head", Params: params, Watch: pgClusterWatch, Load: load,
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
	// Count is the number after the label, CountLabel the same in words
	// for screen readers, as "9 nodes"; Look says how many of them need a
	// look, as "1 needs a look", in the class LookClass (pgLook).
	Count, CountLabel string
	Look, LookClass   string
}

// pgMatrixRow is a node row of the health matrix.
type pgMatrixRow struct {
	Node pgNode
	// Cells hold, for each column of the matrix, "y" for a service that
	// is up, "n" for one that is down, "na" for none or for a node that
	// does not run.
	Cells []pgCell
}

// pgCell is a cell of the health matrix: the class and sign of its state,
// and the state in words, for screen readers and the tooltip.
type pgCell struct {
	Class string
	Mark  string
	Text  string
}

// pgNetRow is a network of the Network tab.
type pgNetRow struct {
	Name, Driver, Subnet, Gateway string
	Mine                          bool
}

// pgMount is a mount of the Network tab, with its kind and how the nodes
// mount it in words.
type pgMount struct {
	cluster.MountPoint
	Kind string
	Mode string
}

// Checked reports whether sind checks that the source of the mount is
// there: a Docker volume, not a directory of the host.
func (m pgMount) Checked() bool { return m.Type != config.StorageHostPath }

// pgNameRow is a row of the names and keys of the Network tab: a value to
// copy, and a note on it.
type pgNameRow struct {
	Label, Value, Note string
	// Copy is set when Value is a thing to copy, not a pattern.
	Copy bool
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
	Status *cluster.Status
	// Slurm is what Slurm says about the cluster.
	Slurm   *pgSlurmView
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
	Mounts   []pgMount
	Names    []pgNameRow
	Volumes  []cluster.VolumeSummary
	HostData string
	// Data is the cluster's data mount, when the status has it.
	Data *cluster.MountPoint
	// Ready is the "Ready for jobs" card of the Summary.
	Ready *pgReady
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
	d.Cluster = pgClusterOf(sn, realm, summary, sn.Monitorings[realm], v.Now)
	d.Status = sn.Status(realm, name)
	d.Slurm = pgSlurmOf(sn.Slurm(realm, name), d.Cluster.Nodes)
	d.Tabs = pgTabLinks(d)
	d.Actions = pgClusterActions(d, v.CSRF)
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

// pgClusterActions returns the buttons of the head of a cluster page: Add
// workers and Delete cluster. While a command creates or deletes the
// cluster, they are off, with why, and a command of sind ui that creates
// it has its Stop button.
func pgClusterActions(d *pgClusterView, csrf string) []pgAction {
	add := pgDialogAction("Add workers…", "", "create-worker", "realm", d.Realm, "cluster", d.Name)
	del := pgDialogAction("Delete cluster…", "danger", "delete-cluster", "realm", d.Realm, "cluster", d.Name)
	l := d.Cluster.Life
	if l == nil {
		return []pgAction{add, del}
	}
	if l.Stop != nil {
		l.Stop.CSRF = csrf
	}
	if l.Kind == pgDeleting {
		add.Off = "sind deletes the cluster."
		del.Off = "sind deletes the cluster already."
		return []pgAction{add, del}
	}
	add.Off = "sind creates the cluster. Add workers once it is ready."
	if l.Stop != nil {
		return []pgAction{add}
	}
	del.Off = "sind creates the cluster in a terminal. Stop it there, or delete the cluster once the create ends."
	return []pgAction{add, del}
}

func pgTabLinks(d *pgClusterView) []pgTabLink {
	look, class := pgLook(d.Cluster.Nodes)
	var tabs []pgTabLink
	for _, t := range pgTabs {
		l := pgTabLink{Key: t.Key, Label: t.Label, URL: pgClusterURL(d.Realm, d.Name, t.Key), On: t.Key == d.Tab}
		if t.Key == "nodes" {
			l.Count, l.CountLabel = fmt.Sprint(len(d.Cluster.Nodes)), d.Cluster.NodesText()
			if look > 0 {
				l.Look, l.LookClass = pgLookShort(look), class
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
				row.Cells = append(row.Cells, pgCell{"na", "·", s + ": not on this node"})
			case n.Life() && !up:
				// A service of a node that a command creates or deletes is
				// starting or stopping, not down.
				row.Cells = append(row.Cells, pgCell{"wait", "○", s + ": " + n.ServiceWord()})
			case n.State != cluster.StateRunning:
				row.Cells = append(row.Cells, pgCell{"na", "–", s + ": node not running"})
			case up:
				row.Cells = append(row.Cells, pgCell{"y", "✓", s + ": active"})
			default:
				row.Cells = append(row.Cells, pgCell{"n", "✗", s + ": down"})
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
	d.Data = pgDataMount(d.Status)
	d.Ready = pgReadyOf(d)
}

// pgDataMount returns the cluster's data mount, whatever its path, nil
// before the first probe.
func pgDataMount(st *cluster.Status) *cluster.MountPoint {
	if st == nil {
		return nil
	}
	for i, m := range st.Mounts {
		if m.Data {
			return &st.Mounts[i]
		}
	}
	return nil
}

// pgNodesTab prepares the dialogs that the selection of nodes opens. The
// page script shows those that fit a selected node, and appends the
// selected nodes that each fits, as ?nodes=worker-0,worker-1.
func pgNodesTab(d *pgClusterView) {
	power := func(verb, op, fit string) pgAction {
		a := pgDialogAction(verb+"…", "", "power", "realm", d.Realm, "cluster", d.Name, "op", op)
		a.Verb, a.Fit = verb, fit
		return a
	}
	notRunning := string(cluster.StateStopped) + " " + string(cluster.StateUnknown)
	running := string(cluster.StateRunning)
	d.Selection = []pgAction{
		power("Start", "start", notRunning), power("Shut down", "stop", running), power("Reboot", "reboot", running),
		power("Freeze", "pause", running), power("Unfreeze", "unpause", string(cluster.StatePaused)),
	}
	// Drain and Resume act on workers, as Delete does, which names the
	// workers that it deletes; the page script counts them in each label.
	worker := string(config.RoleWorker)
	d.WorkerSelection = []pgAction{power("Drain", "drain", worker), power("Resume", "resume", worker)}
	d.Delete = pgDialogAction("Delete…", "danger", "delete-worker", "realm", d.Realm, "cluster", d.Name)
	d.Delete.Verb, d.Delete.Fit = "Delete", worker
}

// pgNetworkTab fills the networks, the mounts and the volumes. The
// status holds the most; the snapshot's lists are what remains before the
// first probe.
func pgNetworkTab(d *pgClusterView, sn store.Snapshot) {
	mine := string(cluster.NetworkName(d.Realm, d.Name))
	mesh := d.Realm + "-mesh"
	for _, n := range sn.Networks[d.Realm] {
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
	d.Names = pgNames(d, sn)
	if d.Status != nil {
		for _, m := range d.Status.Mounts {
			d.Mounts = append(d.Mounts, pgMount{m, pgMountKind(m), pgMountMode(m)})
			if m.Data && m.Type == config.StorageHostPath {
				d.HostData = m.Source
			}
		}
		return
	}
	prefix := cluster.ContainerPrefix(d.Realm, d.Name)
	for _, vol := range sn.Volumes[d.Realm] {
		if strings.HasPrefix(vol.Name, prefix) {
			d.Volumes = append(d.Volumes, vol)
		}
	}
}

// pgMountKind says how a mount reaches the nodes: "Docker volume", "host
// directory (bind mount)", or the directory of a volume that each node
// mounts its own part of.
func pgMountKind(m cluster.MountPoint) string {
	switch {
	case m.Type == config.StorageHostPath:
		return "host directory (bind mount)"
	case m.Subpath != "":
		return "Docker volume, directory " + m.Subpath
	}
	return "Docker volume"
}

// pgMountMode says how the nodes mount m, as sind creates them: the
// controllers write the Slurm config, the other nodes only read it, and
// the munge key and CVMFS are read-only on every node.
func pgMountMode(m cluster.MountPoint) string {
	switch m.Path {
	case slurm.ConfDir:
		return "rw on controllers, ro elsewhere"
	case slurm.MungeDir, cluster.CVMFSPath:
		return "read-only"
	}
	return "read-write"
}

// pgNames returns the names and keys of the Network tab: the DNS zone and
// how nodes are named in it, the DNS server and SSH relay of the realm,
// and where the munge key is.
func pgNames(d *pgClusterView, sn store.Snapshot) []pgNameRow {
	zone := d.Realm + ".sind"
	m := sn.Meshes[d.Realm]
	if m.Info != nil && m.Info.DNSZone != "" {
		zone = m.Info.DNSZone
	}
	rows := []pgNameRow{
		{Label: "DNS zone", Value: zone, Note: "The mesh DNS answers for it, on the nodes and, with the host DNS set up, on this host.", Copy: true},
		{Label: "node names", Value: "NODE." + d.Name + "." + zone, Note: "Such as controller." + d.Name + "." + zone + "."},
	}
	if m.Info != nil {
		rows = append(rows,
			pgNameRow{Label: "DNS server", Value: m.Info.DNSIP, Note: "The container " + m.Info.DNSContainer + " on the mesh network.", Copy: true},
			pgNameRow{Label: "SSH relay", Value: m.Info.SSHContainer, Note: "sind ssh goes through it.", Copy: true},
		)
	}
	munge := pgNameRow{Label: "munge key", Value: slurm.MungeKeyPath, Copy: true,
		Note: "In the volume " + string(cluster.VolumeName(d.Realm, d.Name, cluster.VolumeMunge)) + ", on every node. sind ui does not show the key."}
	if d.Status != nil {
		munge.Note = "None: the nodes use auth/slurm (identity clientIds), not munge."
		munge.Value, munge.Copy = "none", false
		for _, mp := range d.Status.Mounts {
			if mp.Path == slurm.MungeDir {
				munge.Value, munge.Copy = slurm.MungeKeyPath, true
				munge.Note = "In the volume " + mp.Source + ", on every node. sind ui does not show the key."
			}
		}
	}
	return append(rows, munge)
}

// pgConnectTab writes the commands that enter, run on and log in to the
// cluster, and the addresses of its services.
func pgConnectTab(d *pgClusterView, sn store.Snapshot) {
	sind, cl := pgSind(d.Realm), pgClusterArg(d.Name)
	var first, api *pgNode
	for i, n := range d.Cluster.Nodes {
		if n.Role == config.RoleAPI {
			api = &d.Cluster.Nodes[i]
		}
		if first == nil || n.Role == config.RoleWorker && first.Role != config.RoleWorker {
			first = &d.Cluster.Nodes[i]
		}
	}
	d.Commands = []pgCmd{
		pgEnterCmd(d),
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
	if m := sn.Monitorings[d.Realm]; m != nil && d.Cluster.Monitoring {
		for _, s := range m.Services {
			if s.Name == "grafana" {
				d.Grafana = s.URL
			}
		}
	}
}

// ConnectLink returns the address of the Connect tab.
func (d *pgClusterView) ConnectLink() string { return pgClusterURL(d.Realm, d.Name, "connect") }

// SlurmConfLink returns the address of slurm.conf on the Slurm tab.
func (d *pgClusterView) SlurmConfLink() string {
	return pgClusterURL(d.Realm, d.Name, "slurm") + "#slurm-file-slurm-conf"
}

// ClusterArg returns the CLUSTER argument of a command: " dev", empty for
// the default cluster.
func (d *pgClusterView) ClusterArg() string { return pgClusterArg(d.Name) }
