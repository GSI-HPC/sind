// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

func init() {
	RegisterFragment(Fragment{
		Name:     "more-topology",
		Topics:   []store.Topic{store.TopicRealms, store.TopicDocker, store.TopicDoctor},
		Prefixes: []string{"clusters/", "nodes/", "networks/", "mesh/", "monitoring/"},
		Params:   []string{"realm"},
		Load:     moreTopologyLoad,
	})
	// A list page reads the realm cookie, which only a route can see.
	RegisterRoute(pgListRoute(Page{
		Name: "more-topology", Pattern: "GET /topology", Title: "Topology", Nav: "topology",
		Params: []string{"realm"}, Fragments: []string{"more-topology"},
	}))
}

// The diagram has four columns in a viewBox 900 wide: this host, the mesh
// network with its services, the cluster networks, the nodes. The page
// scrolls the figure sideways below its natural width, not the page.
const (
	topoWidth  = 900
	topoTop    = 60
	topoMeshDY = 44
	topoNodeDY = 30
	topoMinH   = 220
)

var (
	topoX = [4]int{24, 214, 434, 654}
	topoW = [4]int{160, 180, 180, 200}
)

// moreTopology is the diagram of one realm, as the template draws it.
type moreTopology struct {
	Realm  string
	Width  int
	Height int
	// Label is the figure's text alternative.
	Label string
	// Band is the rectangle behind the mesh column.
	BandX, BandY, BandW, BandH int
	// HasMesh is set when the mesh column has boxes, so that the band shows.
	HasMesh bool
	Heads   []moreTopoHead
	Edges   []moreTopoEdge
	Boxes   []moreTopoBox
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
	// Full is the unclipped text, for the tooltip.
	Full string
	// Class is "", "bad", "off" or both.
	Class string
	// Href opens the thing the box stands for, "" for none.
	Href string
}

// TextX is where the box's text starts.
func (b moreTopoBox) TextX() int { return b.X + 10 }

// moreTopoData is what the Topology page shows.
type moreTopoData struct {
	Topologies []moreTopology
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

// moreBox returns a box of column col centred on y.
func moreBox(col, y int, label, sub, class, href string) moreTopoBox {
	b := moreTopoBox{X: topoX[col], Y: y - 16, W: topoW[col], H: 26, TextY: y + 1, Class: class, Href: href}
	chars := (b.W - 20) * 10 / 76
	b.Label, b.Full = moreClip(label, chars), label
	if sub != "" {
		b.H, b.TextY, b.SubY = 36, y, y+14
		b.Sub = moreClip(sub, chars+4)
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

// moreTopologyOf lays out the diagram of a realm. It draws nothing when
// the realm has neither a mesh nor a cluster.
func moreTopologyOf(sn store.Snapshot, realm string) (moreTopology, bool) {
	clusters := sn.ClustersOf(realm)
	m, hasMesh := sn.MeshOf(realm)
	if len(clusters) == 0 && (!hasMesh || m.Info == nil) {
		return moreTopology{}, false
	}
	type item struct{ label, sub string }
	var meshItems []item
	head := "MESH"
	if m.Info != nil {
		head += " " + strings.ToUpper(m.Info.Network)
		meshItems = append(meshItems, item{m.Info.DNSContainer, "DNS · " + m.Info.DNSIP}, item{m.Info.SSHContainer, "SSH relay"})
	}
	if mon := sn.MonitoringOf(realm); mon != nil {
		for _, s := range mon.Services {
			meshItems = append(meshItems, item{string(s.Container), string(s.Name)})
		}
	}
	for _, e := range m.External {
		meshItems = append(meshItems, item{e, "connected container"})
	}
	subnets := map[string]string{}
	for _, n := range sn.NetworksOf(realm) {
		subnets[n.Name] = n.Subnet
	}

	// Nodes first: they set the height. A cluster without nodes takes one
	// row, so that its network still shows.
	t := moreTopology{Realm: realm, Width: topoWidth}
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
		for _, n := range nodes {
			short := strings.TrimPrefix(n.Container, prefix)
			class := ""
			if n.State != cluster.StateRunning {
				class = "bad off"
			}
			g.boxes = append(g.boxes, moreBox(3, y, short+"."+c.Name, "", class, "/nodes/"+realm+"/"+c.Name+"/"+short))
			y += topoNodeDY
		}
		if len(nodes) == 0 {
			y += topoNodeDY
		}
		g.y = (top + y - topoNodeDY) / 2
		groups = append(groups, g)
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
	host := moreBox(0, t.Height/2-20, "this host", hostSub, "", "/doctor")
	t.BandX, t.BandY, t.BandW, t.BandH = topoX[1]-12, 36, topoW[1]+24, t.Height-46
	t.Heads = []moreTopoHead{{topoX[0], "HOST"}, {topoX[1], head}, {topoX[2], "CLUSTER NETWORKS"}, {topoX[3], "NODES"}}
	t.HasMesh = len(meshItems) > 0
	t.Boxes = append(t.Boxes, host)

	var mesh []moreTopoBox
	for i, it := range meshItems {
		b := moreBox(1, topoTop+i*topoMeshDY, it.label, it.sub, "", "")
		mesh = append(mesh, b)
		t.Edges = append(t.Edges, moreEdge(host, b, "mesh"))
	}
	t.Boxes = append(t.Boxes, mesh...)
	for _, g := range groups {
		state := string(g.c.State)
		name := string(cluster.NetworkName(realm, g.c.Name))
		sub := state
		if cidr := subnets[name]; cidr != "" {
			sub = cidr + " · " + state
		}
		class := ""
		if g.c.State == cluster.StateMixed {
			class = "bad"
		}
		nb := moreBox(2, g.y, name, sub, class, "/clusters/"+realm+"/"+g.c.Name)
		t.Boxes = append(t.Boxes, nb)
		if len(mesh) > 0 {
			t.Edges = append(t.Edges, moreEdge(mesh[0], nb, "mesh"))
		}
		for _, b := range g.boxes {
			t.Edges = append(t.Edges, moreEdge(nb, b, "net"))
		}
		t.Boxes = append(t.Boxes, g.boxes...)
	}
	t.Label = fmt.Sprintf("Topology of realm %s: the host, the mesh network with %d services, and %d cluster networks with their nodes",
		realm, len(meshItems), len(groups))
	return t, true
}

// moreTopologyLoad fills the data of the Topology page.
func moreTopologyLoad(v *View) error {
	realms, err := moreRealms(v)
	if err != nil {
		return err
	}
	d := moreTopoData{}
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
