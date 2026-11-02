// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
)

func init() {
	register(
		Action{ID: "connect", Build: buildConnect},
		Action{ID: "disconnect", Build: buildDisconnect},
	)
}

// ConnectOther is the field of Connect that holds the name of a container
// that the list of the dialog does not offer; the field "container" holds
// the one picked from the list, and wins.
const ConnectOther = "other"

// containerName returns the container of the form, once it is a valid
// name.
func containerName(f Form, names ...string) (string, error) {
	var name string
	for _, field := range names {
		if name = strings.TrimSpace(f.Get(field)); name != "" {
			break
		}
	}
	if name == "" {
		return "", &Missing{Field: "container", Text: "pick a container, or write the name of one, such as kind-control-plane"}
	}
	if !containerPattern.MatchString(name) {
		return "", fmt.Errorf("container: use letters, digits, _, . and - in the name of a container, and start it with a letter or a digit")
	}
	return name, nil
}

// buildConnect attaches a container that sind did not make to the
// networks of a realm. It warns of a realm that sind ui does not see, as
// sind connects a container only to a realm with a mesh, and checks the
// container against what sind ui sees: one of sind's own cannot connect,
// and one that is on the mesh already, does not run or is unknown gets a
// warning. It says what the container then reaches, unless it is on the
// mesh already or unknown.
func buildConnect(f Form, c Context) (Command, error) {
	realm, err := checkRealm(f)
	if err != nil {
		return Command{}, err
	}
	name, err := containerName(f, "container", ConnectOther)
	if err != nil {
		return Command{}, err
	}
	if r, ok := SindContainer(c.Snap, name); ok {
		return Command{}, fmt.Errorf("container: %s is a container of sind in realm %s; pick one that sind did not make", name, r)
	}
	cmd := NewCommand(sindArgv(realm, "connect", "container", name)...)
	cmd.OK = "Connect " + name
	if _, ok := c.Snap.Realm(realm); !ok {
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("sind ui sees no realm %s. sind connects a container only to a realm with a cluster, whose mesh it joins; create a cluster in %s first.", realm, realm))
		return cmd, nil
	}
	m := c.Snap.Meshes[realm]
	i := slices.IndexFunc(m.Connectable, func(o store.Outside) bool { return o.Name == name })
	// A mesh that sind ui could not read fully knows no container for sure.
	known := m.Info != nil && m.Err == ""
	switch {
	case slices.Contains(m.External, name):
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("%s is on the mesh of realm %s already. sind adds only the networks that it lacks.", name, realm))
		return cmd, nil
	case i >= 0 && m.Connectable[i].State != "running":
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("%s does not run, it is %s. It reaches the nodes once you start it.", name, m.Connectable[i].State))
	case i < 0 && known:
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("sind ui sees no container %s. The command fails unless it runs by then.", name))
		return cmd, nil
	}
	if m.Info != nil {
		cmd.Notes = append(cmd.Notes, connectReach(c.Snap, realm, name, m))
	}
	return cmd, nil
}

// connectReach says what a container that joins the realm then reaches:
// its mesh network and the networks of its clusters, and a node by name.
func connectReach(sn store.Snapshot, realm, name string, m store.Mesh) string {
	var clusters []string
	for _, cl := range sn.Clusters[realm] {
		clusters = append(clusters, cl.Name)
	}
	slices.SortFunc(clusters, natsort.Compare)
	text := "`" + name + "` joins `" + m.Info.Network + "`"
	switch len(clusters) {
	case 0:
	case 1:
		text += " and the network of " + clusters[0]
	default:
		text += " and the networks of " + JoinWords(clusters)
	}
	switch n := len(m.Records); n {
	case 0:
		return text + ". It can then reach the nodes of realm " + realm + " by name."
	case 1:
		return text + ". It can then reach `" + m.Records[0].Hostname + "` by name."
	case 2:
		return text + ". It can then reach `" + m.Records[0].Hostname + "` and the other node by name."
	default:
		return fmt.Sprintf("%s. It can then reach `%s` and the other %d nodes by name.", text, m.Records[0].Hostname, n-1)
	}
}

// JoinWords joins words as a sentence does: "a", "a and b", "a, b and c".
func JoinWords(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// SindContainer returns the realm whose node, mesh or monitoring container
// the name is, if any: a container that sind made.
func SindContainer(sn store.Snapshot, name string) (string, bool) {
	for realm, nodes := range sn.Nodes {
		if slices.ContainsFunc(nodes, func(n cluster.NodeSummary) bool { return n.Container == name }) {
			return realm, true
		}
	}
	for realm, m := range sn.Meshes {
		if i := m.Info; i != nil && (name == i.DNSContainer || name == i.SSHContainer) {
			return realm, true
		}
	}
	for realm, mon := range sn.Monitorings {
		if mon != nil && slices.ContainsFunc(mon.Services, func(s cluster.MonitoringService) bool { return string(s.Container) == name }) {
			return realm, true
		}
	}
	return "", false
}

// buildDisconnect detaches a container that sind did not make from the
// networks of a realm, which sind checks itself.
func buildDisconnect(f Form, _ Context) (Command, error) {
	realm, err := checkRealm(f)
	if err != nil {
		return Command{}, err
	}
	name, err := containerName(f, "container")
	if err != nil {
		return Command{}, err
	}
	return NewCommand(sindArgv(realm, "disconnect", "container", name)...), nil
}
