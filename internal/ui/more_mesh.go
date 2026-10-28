// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

func init() {
	defaultRegistry.AddFragment(Fragment{
		Name:     "more-mesh",
		Topics:   []store.Topic{store.TopicRealms, store.TopicDocker},
		Prefixes: []string{"mesh/", "networks/", "nodes/"},
		Params:   []string{"realm"},
		Load:     moreMeshLoad,
	})
	// A list page reads the realm cookie, which only a route can see.
	defaultRegistry.AddRoute(pgRealmRoute(Page{
		Name: "more-mesh", Pattern: "GET /mesh", Title: "Mesh and DNS", Nav: "mesh",
		Params: []string{"realm"}, Fragments: []string{"more-mesh"},
	}, true, false))
}

// moreMeshData is what the Mesh and DNS page shows.
type moreMeshData struct {
	// Realms are the realms shown, Multi says there is more than one, so
	// each section names its realm.
	Realms []moreMeshRealm
	Multi  bool
	// Notice is set when there is no realm to show.
	Notice moreNotice
	// DockerErr is why Docker is unreachable, while the data is the last
	// that was read.
	DockerErr string
}

// moreMeshRealm is the mesh of one realm.
type moreMeshRealm struct {
	Name string
	// Err says why the mesh could not be read.
	Err string
	// Has is set when the realm has a mesh.
	Has  bool
	Info mesh.Info
	// Subnet is the mesh network's address range, empty when unknown.
	Subnet   string
	Records  []moreDNSRecord
	External []moreExternal
	// Outsides are the containers that sind did not make and that run,
	// which can join the realm.
	Outsides []pgOutside
	// ConnectURL and DeleteURL open the dialogs of the realm.
	ConnectURL, DeleteURL string
}

// RecordsText counts the realm's DNS records: "1 record" or "2 records".
func (r moreMeshRealm) RecordsText() string { return pgPlural(len(r.Records), "record", "records") }

// moreDNSRecord is a record of the mesh DNS, with the page of the node
// that has the name, "" for none.
type moreDNSRecord struct {
	mesh.DNSRecord
	Href string
}

// moreDNSRecords adds the node pages to the records of a realm.
func moreDNSRecords(sn store.Snapshot, realm string, records []mesh.DNSRecord) []moreDNSRecord {
	pages := map[string]string{}
	for _, n := range sn.Nodes[realm] {
		pages[n.FQDN] = pgNodeURL(realm, n.Cluster, pgShort(realm, n.Cluster, n.Container))
	}
	out := make([]moreDNSRecord, len(records))
	for i, r := range records {
		out[i] = moreDNSRecord{DNSRecord: r, Href: pages[r.Hostname]}
	}
	return out
}

// moreExternal is a container outside sind on the mesh network.
type moreExternal struct {
	Name          string
	DisconnectURL string
}

// ConnectCommand is the command that connects a container of any name to
// the realm.
func (r moreMeshRealm) ConnectCommand() string {
	if r.Name == mesh.DefaultRealm {
		return "sind connect container NAME"
	}
	return "sind --realm " + r.Name + " connect container NAME"
}

// pgOutside is a container that sind did not make, and that can join a
// realm: the Connect dialog offers it, and the Mesh and DNS page while no
// container is connected.
type pgOutside struct {
	Name, Image string
	// Networks names its networks, as "network bridge"; Up is how long it
	// runs, as "up 2 hours".
	Networks, Up string
	// State is Docker's word for its state; Off says that it does not
	// run, so that it cannot be picked.
	State string
	Off   bool
	// Also names the other realms that have it, as "also in realm sind".
	Also string
	// ConnectURL opens the Connect dialog for it.
	ConnectURL string
}

// pgOutsides returns the containers that sind did not make and that are
// not on the mesh of the realm, those that run first, each by name. It
// leaves out the containers of sind's other realms, such as their
// monitoring stacks, which the docker ps of the realm does not tell apart.
func pgOutsides(sn store.Snapshot, realm string) []pgOutside {
	var on, off []pgOutside
	for _, c := range sn.Meshes[realm].Connectable {
		if _, ok := action.SindContainer(sn, c.Name); ok {
			continue
		}
		o := pgOutside{Name: c.Name, Image: c.Image, State: c.State, Off: c.State != "running", Up: strings.ToLower(c.Status),
			ConnectURL: pgDialog("connect", "realm", realm, "container", c.Name)}
		switch len(c.Networks) {
		case 0:
		case 1:
			o.Networks = "network " + c.Networks[0]
		default:
			o.Networks = "networks " + strings.Join(c.Networks, ", ")
		}
		var also []string
		for _, r := range pgRealmNames(sn, "") {
			if r != realm && slices.Contains(sn.Meshes[r].External, c.Name) {
				also = append(also, r)
			}
		}
		switch len(also) {
		case 0:
		case 1:
			o.Also = "also in realm " + also[0]
		default:
			o.Also = "also in realms " + pgJoin(also)
		}
		if o.Off {
			off = append(off, o)
		} else {
			on = append(on, o)
		}
	}
	return append(on, off...)
}

// moreMeshLoad fills the data of the Mesh and DNS page from the snapshot.
func moreMeshLoad(v *View) error {
	realms := moreRealms(v)
	d := moreMeshData{Multi: len(realms) > 1}
	if len(realms) == 0 {
		d.Notice = moreNoRealms(v.Snap.Docker)
	} else {
		d.DockerErr = moreDockerDown(v.Snap.Docker)
	}
	for _, name := range realms {
		r := moreMeshRealm{
			Name:       name,
			ConnectURL: pgDialog("connect", "realm", name),
			DeleteURL:  pgDialog("delete-realm", "realm", name),
		}
		if m, ok := v.Snap.Meshes[name]; ok {
			r.Err, r.Records = m.Err, moreDNSRecords(v.Snap, name, m.Records)
			if m.Info != nil {
				r.Has, r.Info = true, *m.Info
				for _, n := range v.Snap.Networks[name] {
					if n.Name == m.Info.Network {
						r.Subnet = n.Subnet
					}
				}
			}
			for _, e := range m.External {
				r.External = append(r.External, moreExternal{e, pgDialog("disconnect", "realm", name, "nodes", e)})
			}
		}
		for _, o := range pgOutsides(v.Snap, name) {
			if !o.Off {
				r.Outsides = append(r.Outsides, o)
			}
		}
		d.Realms = append(d.Realms, r)
	}
	v.Data = d
	return nil
}
