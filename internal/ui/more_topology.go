// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

func init() {
	defaultRegistry.AddFragment(Fragment{
		Name:     "more-topology",
		Topics:   []store.Topic{store.TopicRealms, store.TopicDocker, store.TopicDoctor},
		Prefixes: []string{"clusters/", "nodes/", "networks/", "mesh/", "monitoring/"},
		Params:   []string{"realm", "view"},
		Load:     moreTopologyLoad,
	})
	// A list page reads the realm cookie, which only a route can see.
	defaultRegistry.AddRoute(pgListRoute(Page{
		Name: "more-topology", Pattern: "GET /topology", Title: "Topology", Nav: "topology",
		Params: []string{"realm", "view"}, Fragments: []string{"more-topology"},
	}))
}

// The diagram has four columns in a viewBox 900 wide: this host, the mesh
// network with its services, the cluster networks, the nodes. The page
// scrolls the figure sideways below its natural width, not the page.
const (
	topoWidth  = 900
	topoTop    = 60
	topoMeshDY = 44
	topoNodeDY = 44
	topoMinH   = 220
)

var (
	topoX = [4]int{24, 214, 434, 654}
	topoW = [4]int{160, 180, 180, 230}
)

// moreTopology is one realm, as the diagram draws it and the table lists
// it.
type moreTopology struct {
	Realm  string
	Width  int
	Height int
	// Label is the figure's name.
	Label string
	// Band is the rectangle behind the mesh column.
	BandX, BandY, BandW, BandH int
	// HasMesh is set when the mesh column has boxes, so that the band shows.
	HasMesh bool
	Heads   []moreTopoHead
	Edges   []moreTopoEdge
	Boxes   []moreTopoBox
	// Host, Mesh and Nets are what the table lists: this host, the
	// containers on the mesh, and the cluster networks with their nodes.
	Host moreTopoItem
	Mesh []moreTopoItem
	Nets []moreTopoNet
}

// moreTopoHead is the heading of a column.
type moreTopoHead struct {
	X     int
	Label string
}

// moreTopoEdge is a line between two boxes; Class is "mesh" or "net".
type moreTopoEdge struct {
	D, Class string
}

// moreTopoBox is a box with one or two lines of text.
type moreTopoBox struct {
	X, Y, W, H int
	// TextY and SubY are the baselines of the label and the line below.
	TextY, SubY int
	Label, Sub  string
	// Full is the unclipped text, for the tooltip and the link's name.
	Full string
	// Class is "", "bad", "off" or both.
	Class string
	// Href opens the thing the box stands for, "" for none.
	Href string
}

// TextX is where the box's text starts.
func (b moreTopoBox) TextX() int { return b.X + 10 }

// moreTopoItem is a row of the table: a container or this host.
type moreTopoItem struct {
	Name, Kind, Addr string
	// State is the words of its state, Pill their colour class; State is
	// "" when nothing is known.
	State, Pill string
	Href        string
}

// moreTopoNet is a cluster network of the table, with its nodes.
type moreTopoNet struct {
	Name, Subnet, State, Pill, Href string
	Nodes                           []moreTopoItem
}

// moreTopoData is what the Topology page shows.
type moreTopoData struct {
	Topologies []moreTopology
	// Table is set when the page lists the realms as tables, not as
	// diagrams; DiagramURL and TableURL switch.
	Table                bool
	DiagramURL, TableURL string
	// Empty holds the realms that have nothing to draw yet.
	Empty     []string
	Notice    moreNotice
	DockerErr string
}

// moreClip cuts s to n characters with an ellipsis.
func moreClip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// moreBox returns a box of column col centred on y. The line below the
// label is in a smaller font, so more of it fits.
func moreBox(col, y int, label, sub, class, href string) moreTopoBox {
	b := moreTopoBox{X: topoX[col], Y: y - 16, W: topoW[col], H: 26, TextY: y + 1, Class: class, Href: href}
	chars := (b.W - 20) * 10 / 76
	b.Label, b.Full = moreClip(label, chars), label
	if sub != "" {
		b.H, b.TextY, b.SubY = 36, y, y+14
		b.Sub = moreClip(sub, chars+10)
		b.Full = label + " · " + sub
	}
	return b
}

// moreEdge returns the curve from the right side of box a to the left side
// of box b.
func moreEdge(a, b moreTopoBox, class string) moreTopoEdge {
	x1, y1 := a.X+a.W, a.Y+a.H/2
	x2, y2 := b.X, b.Y+b.H/2
	mx := (x1 + x2) / 2
	return moreTopoEdge{D: fmt.Sprintf("M%d,%d C%d,%d %d,%d %d,%d", x1, y1, mx, y1, mx, y2, x2, y2), Class: class}
}

// moreJoin joins the parts that are not empty with " · ".
func moreJoin(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// moreNodePill is the colour class of a node or cluster state.
func moreNodePill(s cluster.State) string {
	switch s {
	case cluster.StateRunning:
		return "ok"
	case cluster.StateMixed:
		return "warn"
	}
	return "bad"
}

// morePlural returns "1 thing" or "n things".
func morePlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// moreTopoMesh returns the containers on the mesh of a realm: its DNS
// server and SSH relay, the monitoring services and the containers that
// were connected; and the heading of the mesh column.
func moreTopoMesh(sn store.Snapshot, realm string) (string, []moreTopoItem) {
	m, _ := sn.MeshOf(realm)
	q := "?realm=" + url.QueryEscape(realm)
	head := "MESH"
	var items []moreTopoItem
	if m.Info != nil {
		head += " " + strings.ToUpper(m.Info.Network)
		items = append(items,
			moreTopoItem{Name: m.Info.DNSContainer, Kind: "DNS", Addr: m.Info.DNSIP, Href: "/mesh" + q},
			moreTopoItem{Name: m.Info.SSHContainer, Kind: "SSH relay", Href: "/mesh" + q})
	}
	if mon := sn.MonitoringOf(realm); mon != nil {
		for _, s := range mon.Services {
			it := moreTopoItem{Name: string(s.Container), Kind: string(s.Name), Addr: s.IP, Href: "/monitoring" + q}
			it.Pill, it.State = moreServiceState(s)
			items = append(items, it)
		}
	}
	for _, e := range m.External {
		items = append(items, moreTopoItem{Name: e, Kind: "connected container", Href: "/mesh" + q})
	}
	return head, items
}

// moreTopologyOf lays out the diagram of a realm. It draws nothing when
// the realm has neither a mesh nor a cluster.
func moreTopologyOf(sn store.Snapshot, realm string) (moreTopology, bool) {
	clusters := sn.ClustersOf(realm)
	m, hasMesh := sn.MeshOf(realm)
	if len(clusters) == 0 && (!hasMesh || m.Info == nil) {
		return moreTopology{}, false
	}
	head, meshItems := moreTopoMesh(sn, realm)
	subnets := map[string]string{}
	for _, n := range sn.NetworksOf(realm) {
		subnets[n.Name] = n.Subnet
	}

	// Nodes first: they set the height. A cluster without nodes takes one
	// row, so that its network still shows.
	t := moreTopology{Realm: realm, Width: topoWidth, Mesh: meshItems}
	type group struct {
		c     cluster.Summary
		boxes []moreTopoBox
		// y is the middle row of the cluster's nodes.
		y int
	}
	var groups []group
	y := topoTop
	for _, c := range clusters {
		g := group{c: c}
		top := y
		prefix := cluster.ContainerPrefix(realm, c.Name)
		nodes := sn.ClusterNodes(realm, c.Name)
		name := string(cluster.NetworkName(realm, c.Name))
		net := moreTopoNet{Name: name, Subnet: subnets[name], State: string(c.State), Pill: moreNodePill(c.State), Href: "/clusters/" + realm + "/" + c.Name}
		for _, n := range nodes {
			short := strings.TrimPrefix(n.Container, prefix)
			class := ""
			if n.State != cluster.StateRunning {
				class = "bad off"
			}
			href := "/nodes/" + realm + "/" + c.Name + "/" + short
			g.boxes = append(g.boxes, moreBox(3, y, short+"."+c.Name, moreJoin(n.IP, string(n.Role), string(n.State)), class, href))
			net.Nodes = append(net.Nodes, moreTopoItem{Name: short + "." + c.Name, Kind: string(n.Role), Addr: n.IP,
				State: string(n.State), Pill: moreNodePill(n.State), Href: href})
			y += topoNodeDY
		}
		if len(nodes) == 0 {
			y += topoNodeDY
		}
		g.y = (top + y - topoNodeDY) / 2
		groups = append(groups, g)
		t.Nets = append(t.Nets, net)
	}
	meshEnd := topoTop + len(meshItems)*topoMeshDY
	t.Height = max(y+20, meshEnd+20, topoMinH)

	hostSub := "Docker"
	if d := sn.Doctor; d != nil {
		for _, c := range d.Checks {
			if c.Name == "Docker Engine" && c.Detail != "" {
				hostSub = c.Detail
			}
		}
	}
	t.Host = moreTopoItem{Name: "this host", Kind: hostSub, Href: "/doctor"}
	host := moreBox(0, t.Height/2-20, "this host", hostSub, "", "/doctor")
	t.BandX, t.BandY, t.BandW, t.BandH = topoX[1]-12, 36, topoW[1]+24, t.Height-46
	t.Heads = []moreTopoHead{{topoX[0], "HOST"}, {topoX[1], head}, {topoX[2], "CLUSTER NETWORKS"}, {topoX[3], "NODES"}}
	t.HasMesh = len(meshItems) > 0
	t.Boxes = append(t.Boxes, host)

	var mesh []moreTopoBox
	for i, it := range meshItems {
		b := moreBox(1, topoTop+i*topoMeshDY, it.Name, moreJoin(it.Kind, it.Addr), "", it.Href)
		mesh = append(mesh, b)
		t.Edges = append(t.Edges, moreEdge(host, b, "mesh"))
	}
	t.Boxes = append(t.Boxes, mesh...)
	for i, g := range groups {
		net := t.Nets[i]
		class := ""
		if g.c.State == cluster.StateMixed {
			class = "bad"
		}
		nb := moreBox(2, g.y, net.Name, moreJoin(net.Subnet, net.State), class, net.Href)
		t.Boxes = append(t.Boxes, nb)
		if len(mesh) > 0 {
			t.Edges = append(t.Edges, moreEdge(mesh[0], nb, "mesh"))
		}
		for _, b := range g.boxes {
			t.Edges = append(t.Edges, moreEdge(nb, b, "net"))
		}
		t.Boxes = append(t.Boxes, g.boxes...)
	}
	t.Label = fmt.Sprintf("Diagram of realm %s: this host, the mesh network with %s, and %s with the nodes",
		realm, morePlural(len(meshItems), "container", "containers"), morePlural(len(groups), "cluster network", "cluster networks"))
	return t, true
}

// moreTopoURL is the address of the page in a view, with the realm that
// the request named.
func moreTopoURL(realm, view string) string {
	q := url.Values{}
	if realm != "" {
		q.Set("realm", realm)
	}
	if view != "" {
		q.Set("view", view)
	}
	if len(q) == 0 {
		return "/topology"
	}
	return "/topology?" + q.Encode()
}

// moreTopologyLoad fills the data of the Topology page.
func moreTopologyLoad(v *View) error {
	realms := moreRealms(v)
	view := v.Params.Get("view")
	if view != "" && view != "table" && view != "diagram" {
		return fmt.Errorf("view %q: %w", view, ErrBadRequest)
	}
	d := moreTopoData{Table: view == "table", DiagramURL: moreTopoURL(v.Params.Get("realm"), ""), TableURL: moreTopoURL(v.Params.Get("realm"), "table")}
	if len(realms) == 0 {
		d.Notice = moreNoRealms(v.Snap.Docker)
	} else {
		d.DockerErr = moreDockerDown(v.Snap.Docker)
	}
	for _, name := range realms {
		if t, ok := moreTopologyOf(v.Snap, name); ok {
			d.Topologies = append(d.Topologies, t)
		} else {
			d.Empty = append(d.Empty, name)
		}
	}
	v.Data = d
	return nil
}
