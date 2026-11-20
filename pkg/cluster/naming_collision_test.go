// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/observability"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
)

// Docker has one name space per daemon for containers, one for networks
// and one for volumes. sind makes its names by joining a realm, a cluster
// and a fixed word with "-", and realm and cluster names may hold a "-",
// so two resources can get one name. This file checks, for every name sind
// makes over a set of realm and cluster names, that only one class of
// collision exists: the same resource of two clusters whose realm and
// cluster join to the same string, such as realm a-b with cluster c and
// realm a with cluster b-c. Their realms differ, so each sind command acts
// on the one its realm label names (ownedBy), and the preflight of Create
// refuses the second cluster.
//
// A new resource whose name can be that of another one fails the test. Add
// a new name template to nameTemplates.

// nameSpace is one of Docker's name spaces.
type nameSpace string

const (
	spaceContainer nameSpace = "container"
	spaceNetwork   nameSpace = "network"
	spaceVolume    nameSpace = "volume"
)

// nameTemplate is a way sind names a resource: <realm>-<suffix> for one of
// a realm, <realm>-<cluster>-<suffix> for one of a cluster.
type nameTemplate struct {
	space     nameSpace
	suffix    string
	ofCluster bool
}

// name returns the name the template gives a resource of the realm, and
// of the cluster for a template of a cluster.
func (t nameTemplate) name(realm, cluster string) string {
	if t.ofCluster {
		return realm + "-" + cluster + "-" + t.suffix
	}
	return realm + "-" + t.suffix
}

// nameTemplates returns the templates of every name sind gives a Docker
// resource, taken from the functions that make the names: it calls each
// with the realm "r" and the cluster "c".
func nameTemplates(t *testing.T) []nameTemplate {
	t.Helper()
	var templates []nameTemplate
	add := func(space nameSpace, name string, ofCluster bool) {
		prefix := "r-"
		if ofCluster {
			prefix = "r-c-"
		}
		suffix, ok := strings.CutPrefix(name, prefix)
		require.True(t, ok && suffix != "", "the name %q of a %s is not %s<suffix>", name, space, prefix)
		templates = append(templates, nameTemplate{space: space, suffix: suffix, ofCluster: ofCluster})
	}

	// The mesh: every name of mesh.Names, by the type of its field, and the
	// container that generates the relay's keys, which mesh does not export.
	names := reflect.ValueOf(mesh.RealmNames("r"))
	for i := range names.NumField() {
		field := names.Type().Field(i)
		space, ok := map[string]nameSpace{
			"docker.ContainerName": spaceContainer,
			"docker.NetworkName":   spaceNetwork,
			"docker.VolumeName":    spaceVolume,
		}[field.Type.String()]
		if !ok {
			require.Equal(t, "ComposeProject", field.Name, "mesh.Names has a field that is no Docker name")
			continue
		}
		add(space, names.Field(i).String(), false)
	}
	add(spaceContainer, "r-ssh-keygen", false)

	// The realm lock and the monitoring stack.
	add(spaceNetwork, string(state.LockNetworkName("r")), false)
	add(spaceVolume, string(observability.JournalVolume("r")), false)
	for _, s := range observability.Services {
		add(spaceContainer, string(observability.ContainerName("r", s)), false)
		add(spaceVolume, string(observability.VolumeName("r", s)), false)
	}

	// The clusters: a node of each role, with a second controller and two
	// workers, the network and every volume.
	cfg := &config.Cluster{Name: "c", Nodes: []config.Node{
		{Role: config.RoleController, BackupController: true},
		{Role: config.RoleDB}, {Role: config.RoleAPI}, {Role: config.RoleSubmitter},
		{Role: config.RoleWorker, Count: 2},
	}}
	for _, node := range nodeRunConfigs(cfg, "r", "", "", "") {
		add(spaceContainer, string(ContainerName("r", "c", node.ShortName)), true)
	}
	add(spaceNetwork, string(NetworkName("r", "c")), true)
	for _, vt := range AllVolumeTypes {
		add(spaceVolume, string(VolumeName("r", "c", vt)), true)
	}
	return templates
}

// nameOwner is a resource that a name can be the name of.
type nameOwner struct {
	template       nameTemplate
	realm, cluster string
}

// validNames remembers which names sind takes as the name of a realm or
// of a cluster: the tests ask for the same names many times.
type validNames map[[2]string]bool

// valid reports whether sind takes name as the name of a kind, "realm" or
// "cluster" (config.CheckName).
func (v validNames) valid(kind, name string) bool {
	key := [2]string{kind, name}
	ok, known := v[key]
	if !known {
		ok = config.CheckName(kind, name) == nil
		v[key] = ok
	}
	return ok
}

// nameOwners returns every resource that name can be in the name space:
// each template of the space with each realm, and cluster, that sind takes
// and that give the name.
func (v validNames) nameOwners(templates []nameTemplate, space nameSpace, name string) []nameOwner {
	var owners []nameOwner
	for _, t := range templates {
		rest, ok := strings.CutSuffix(name, "-"+t.suffix)
		if !ok || t.space != space {
			continue
		}
		if !t.ofCluster {
			if v.valid("realm", rest) {
				owners = append(owners, nameOwner{template: t, realm: rest})
			}
			continue
		}
		for i := range len(rest) {
			if rest[i] != '-' {
				continue
			}
			realm, cluster := rest[:i], rest[i+1:]
			if v.valid("realm", realm) && v.valid("cluster", cluster) {
				owners = append(owners, nameOwner{template: t, realm: realm, cluster: cluster})
			}
		}
	}
	return owners
}

// collisionNames returns the realm or cluster names the test makes names
// from: every word of a template's suffix and the word "x", alone and two
// joined with "-", that sind takes as a name of the kind. A collision
// needs a name that ends as a suffix begins, so these are the names that
// can make one.
func collisionNames(templates []nameTemplate, kind string) []string {
	words := []string{"x"}
	for _, t := range templates {
		words = append(words, strings.Split(t.suffix, "-")...)
	}
	slices.Sort(words)
	words = slices.Compact(words)
	var names []string
	for _, a := range words {
		names = append(names, a)
		for _, b := range words {
			names = append(names, a+"-"+b)
		}
	}
	return slices.DeleteFunc(names, func(n string) bool { return config.CheckName(kind, n) != nil })
}

func TestNameTemplates(t *testing.T) {
	var got []string
	for _, tpl := range nameTemplates(t) {
		got = append(got, fmt.Sprintf("%s %s", tpl.space, tpl.name("R", "C")))
	}
	assert.ElementsMatch(t, []string{
		"network R-mesh", "container R-dns", "container R-ssh", "volume R-ssh-config", "container R-ssh-keygen",
		"network R-lock", "volume R-journal",
		"container R-grafana", "volume R-grafana", "container R-prometheus", "volume R-prometheus",
		"container R-loki", "volume R-loki", "container R-alloy", "volume R-alloy",
		"container R-C-controller", "container R-C-controller-backup", "container R-C-db", "container R-C-api",
		"container R-C-submitter", "container R-C-worker-0", "container R-C-worker-1",
		"network R-C-net",
		"volume R-C-config", "volume R-C-munge", "volume R-C-data", "volume R-C-state", "volume R-C-home",
	}, got)
}

// A resource of a realm shares its name with no other resource. This is
// what the reserved cluster name ssh is for (config.ReservedClusterName).
func TestNames_RealmResourcesAreUnique(t *testing.T) {
	templates, names := nameTemplates(t), validNames{}
	for _, realm := range collisionNames(templates, "realm") {
		for _, tpl := range templates {
			if tpl.ofCluster {
				continue
			}
			name := tpl.name(realm, "")
			owners := names.nameOwners(templates, tpl.space, name)
			if !assert.Equal(t, []nameOwner{{template: tpl, realm: realm}}, owners, "the %s %s", tpl.space, name) {
				return
			}
		}
	}
}

// Two resources of clusters share a name only when they are the same
// resource of clusters whose realm and cluster join to the same string.
func TestNames_ClusterResourcesCollideOnlyAcrossRealms(t *testing.T) {
	t.Parallel()
	templates, names := nameTemplates(t), validNames{}
	realms, clusters := collisionNames(templates, "realm"), collisionNames(templates, "cluster")
	single := func(name string) bool { return !strings.Contains(name, "-") }
	checked, collisions := 0, 0
	for _, realm := range realms {
		for _, cluster := range clusters {
			// Two words in both add no way to collide: see collisionNames.
			if !single(realm) && !single(cluster) {
				continue
			}
			for _, tpl := range templates {
				if !tpl.ofCluster {
					continue
				}
				name := tpl.name(realm, cluster)
				owners := names.nameOwners(templates, tpl.space, name)
				checked++
				collisions += len(owners) - 1
				// Plain comparisons: the assert functions compare by
				// reflection, which is slow for this many names.
				if !slices.Contains(owners, nameOwner{template: tpl, realm: realm, cluster: cluster}) {
					t.Fatalf("the %s %s is not found as %s of cluster %s in realm %s", tpl.space, name, tpl.suffix, cluster, realm)
				}
				for _, o := range owners {
					if o.template != tpl || o.realm+"-"+o.cluster != realm+"-"+cluster {
						t.Fatalf("the %s %s is %s of cluster %s in realm %s, and %s of cluster %q in realm %s",
							tpl.space, name, tpl.suffix, cluster, realm, o.template.suffix, o.cluster, o.realm)
					}
				}
			}
		}
	}
	assert.Greater(t, checked, 100000, "names checked")
	assert.Positive(t, collisions, "the test finds the collisions across realms")
}

// The test finds a collision that a new template would add: a cluster
// volume "lock-state" would make its volume of cluster a the state volume
// of cluster a-lock, and a realm container "x-db" the db node of cluster
// x. Without the reserved cluster name ssh, the config volume of a
// cluster ssh would be the realm's SSH volume.
func TestNames_FindsNewCollisions(t *testing.T) {
	templates, names := nameTemplates(t), validNames{}
	suffixes := func(owners []nameOwner) []string {
		var suffixes []string
		for _, o := range owners {
			suffixes = append(suffixes, o.template.suffix)
		}
		slices.Sort(suffixes)
		return slices.Compact(suffixes)
	}

	more := append(slices.Clone(templates), nameTemplate{space: spaceVolume, suffix: "lock-state", ofCluster: true})
	assert.Equal(t, []string{"lock-state", "state"}, suffixes(names.nameOwners(more, spaceVolume, "r-a-lock-state")))

	more = append(slices.Clone(templates), nameTemplate{space: spaceContainer, suffix: "x-db"})
	assert.Equal(t, []string{"db", "x-db"}, suffixes(names.nameOwners(more, spaceContainer, "r-x-db")))

	assert.Equal(t, []string{"ssh-config"}, suffixes(names.nameOwners(templates, spaceVolume, "r-ssh-config")))
	names[[2]string{"cluster", config.ReservedClusterName}] = true
	assert.Equal(t, []string{"config", "ssh-config"}, suffixes(names.nameOwners(templates, spaceVolume, "r-ssh-config")))
}
