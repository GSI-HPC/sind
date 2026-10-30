// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/probe"
)

func init() {
	register(Action{ID: "restart-service", Build: buildRestartService, Cluster: formCluster})
}

// buildRestartService builds the restart of one service of a running node:
// sind ssh runs systemctl restart UNIT on it. The unit must be one of the
// services that sind checks on the node, as the status of its cluster
// lists them, so that the form names no other command. sind ssh logs in
// through the node's sshd, so no restart runs while sshd is down.
func buildRestartService(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	if len(NodeNames(f)) == 0 {
		return Command{}, &Missing{Field: "nodes", Text: "pick a node of " + cl.Name}
	}
	picked, err := pickNodes(f, c, realm, cl.Name, "")
	if err != nil {
		return Command{}, err
	}
	if len(picked) != 1 {
		return Command{}, errors.New("nodes: pick one node")
	}
	n := picked[0]
	short := ShortName(n)
	switch n.State {
	case cluster.StateRunning:
	case cluster.StatePaused:
		return Command{}, fmt.Errorf("nodes: %s is frozen: unfreeze it, and its services run on", short)
	default:
		return Command{}, fmt.Errorf("nodes: %s is %s: start it, and systemd starts its services", short, n.State)
	}
	services := NodeServices(c.Snap.Status(realm, cl.Name), short, cl.Name)
	if services == nil {
		return Command{}, fmt.Errorf("nodes: sind ui has not probed %s yet", short)
	}
	if up, ok := services[string(probe.ServiceSSHD)]; ok && !up {
		// sind ssh logs in through sshd.
		return Command{}, fmt.Errorf("nodes: sshd is down on %s, and sind ssh needs it: reboot %s", short, short)
	}
	unit := strings.TrimSpace(f.Get("unit"))
	up, ok := services[unit]
	switch {
	case unit == "":
		return Command{}, &Missing{Field: "unit", Text: "pick the service to restart"}
	case !ok:
		return Command{}, fmt.Errorf("unit: sind checks no service %s on %s; it checks %s", unit, short, strings.Join(slices.Sorted(maps.Keys(services)), ", "))
	}
	arg := nodeWords(cl.Name, []string{short})[0]
	cmd := NewCommand(sindArgv(realm, "ssh", arg, "--", "systemctl", "restart", unit)...)
	cmd.OK = "Restart " + unit
	cmd.Notes = append(cmd.Notes, "systemd stops "+unit+" and starts it again. The other processes of "+short+" go on. sind ui probes the node again within seconds.")
	if up {
		cmd.Warns = append(cmd.Warns, unit+" is active: it stops for a moment.")
	}
	return cmd, nil
}

// NodeServices returns the services that sind checks on the node of the
// status st of the cluster name, by unit, and whether each is up; nil when
// st does not list the node or has no health of it.
func NodeServices(st *cluster.Status, short, name string) map[string]bool {
	if st == nil {
		return nil
	}
	for _, s := range st.Nodes {
		if s == nil || s.Health == nil || s.Name != short+"."+name && s.Name != short {
			continue
		}
		out := map[string]bool{}
		for svc, up := range s.Health.Services {
			out[string(svc)] = up
		}
		return out
	}
	return nil
}
