// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"strings"
)

func init() {
	register(
		Action{ID: "power", Build: buildPower, Cluster: formCluster},
		Action{ID: "connect", Build: buildConnect(false)},
		Action{ID: "disconnect", Build: buildConnect(true)},
	)
}

// PowerOp is a way to change the power state of nodes, as a sind power
// command: ID is the subcommand.
type PowerOp struct {
	ID    string
	Label string
	Text  string
	// Danger says that processes stop without warning.
	Danger bool
}

// PowerOps are the ways to change power, in the order a dialog lists them.
var PowerOps = []PowerOp{
	{"shutdown", "Shut down", "Stops the containers cleanly (docker stop). Their data stays. Start them again later.", false},
	{"on", "Start", "Starts the containers (docker start), and the mesh and monitoring if they stopped.", false},
	{"reboot", "Reboot", "Stops, then starts the containers. Running jobs on them fail.", false},
	{"cycle", "Power-cycle", "Kills, then starts the containers. Like a reset button.", true},
	{"cut", "Cut power", "Kills the containers at once (docker kill). Like pulling the plug: what is not on disk is lost.", true},
	{"freeze", "Freeze", "Pauses all processes (docker pause). Slurm sees the nodes stop answering.", false},
	{"unfreeze", "Unfreeze", "Lets the paused processes run again.", false},
}

// powerAliases are the words other parts of sind ui and docker users say for
// the subcommands of sind power.
var powerAliases = map[string]string{
	"start": "on", "stop": "shutdown", "off": "shutdown", "restart": "reboot",
	"pause": "freeze", "unpause": "unfreeze", "resume": "unfreeze",
}

// PowerOpOf returns the power operation that word names: a subcommand of
// sind power, or one of the aliases start, stop, restart, pause and unpause.
func PowerOpOf(word string) (PowerOp, bool) {
	if a, ok := powerAliases[word]; ok {
		word = a
	}
	for _, op := range PowerOps {
		if op.ID == word {
			return op, true
		}
	}
	return PowerOp{}, false
}

func buildPower(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	op, ok := PowerOpOf(strings.TrimSpace(f.Get("op")))
	if !ok {
		return Command{}, fmt.Errorf("op: pick what to do with the nodes")
	}
	picked, err := pickNodes(f, c, realm, cl.Name, false)
	if err != nil {
		return Command{}, err
	}
	shorts := make([]string, len(picked))
	for i, n := range picked {
		shorts[i] = ShortName(n)
	}
	cmd := NewCommand(sindArgv(realm, append([]string{"power", op.ID}, nodeWords(cl.Name, shorts)...)...)...)
	if op.Danger {
		cmd.Warns = append(cmd.Warns, "Cannot be undone: processes on these nodes stop without warning.")
	}
	return cmd, nil
}

// buildConnect returns the Build of connect container, or with detach of
// disconnect container. Connect warns of a realm that sind ui does not
// see, as sind connects a container only to a realm with a mesh.
func buildConnect(detach bool) func(Form, Context) (Command, error) {
	return func(f Form, c Context) (Command, error) {
		realm, err := checkRealm(f)
		if err != nil {
			return Command{}, err
		}
		name := strings.TrimSpace(f.Get("container"))
		if name == "" {
			return Command{}, &Missing{Field: "container", Text: "write the name of a container, such as kind-control-plane"}
		}
		if !containerPattern.MatchString(name) {
			return Command{}, fmt.Errorf("container: write the name of a container, such as kind-control-plane")
		}
		if detach {
			return NewCommand(sindArgv(realm, "disconnect", "container", name)...), nil
		}
		cmd := NewCommand(sindArgv(realm, "connect", "container", name)...)
		if _, ok := c.Snap.Realm(realm); !ok {
			cmd.Warns = append(cmd.Warns, fmt.Sprintf("sind ui sees no realm %s. sind connects a container only to a realm with a cluster, whose mesh it joins; create a cluster in %s first.", realm, realm))
		}
		return cmd, nil
	}
}
