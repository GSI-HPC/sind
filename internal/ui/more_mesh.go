// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
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
	defaultRegistry.AddRoute(pgListRoute(Page{
		Name: "more-mesh", Pattern: "GET /mesh", Title: "Mesh and DNS", Nav: "mesh",
		Params: []string{"realm"}, Fragments: []string{"more-mesh"},
	}))
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
	for _, n := range sn.NodesOf(realm) {
		short := strings.TrimPrefix(n.Container, cluster.ContainerPrefix(realm, n.Cluster))
		pages[n.FQDN] = "/nodes/" + realm + "/" + n.Cluster + "/" + short
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
			ConnectURL: moreDialogURL("connect", "realm", name),
			DeleteURL:  moreDialogURL("delete-realm", "realm", name),
		}
		if m, ok := v.Snap.MeshOf(name); ok {
			r.Err, r.Records = m.Err, moreDNSRecords(v.Snap, name, m.Records)
			if m.Info != nil {
				r.Has, r.Info = true, *m.Info
				for _, n := range v.Snap.NetworksOf(name) {
					if n.Name == m.Info.Network {
						r.Subnet = n.Subnet
					}
				}
			}
			for _, e := range m.External {
				r.External = append(r.External, moreExternal{e, moreDialogURL("disconnect", "realm", name, "nodes", e)})
			}
		}
		d.Realms = append(d.Realms, r)
	}
	v.Data = d
	return nil
}
