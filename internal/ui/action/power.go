// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

func init() {
	register(
		Action{ID: "power", Build: buildPower, Cluster: formCluster},
		Action{ID: "connect", Build: buildConnect(false)},
		Action{ID: "disconnect", Build: buildConnect(true)},
	)
}

// PowerOp is a way to change the state of nodes: a sind power command,
// whose subcommand ID is, or, for the ops of the group Slurm, a change of
// the Slurm state of the nodes with scontrol.
type PowerOp struct {
	ID    string
	Label string
	Text  string
	// Group is the PowerGroup the op belongs to.
	Group string
	// Danger says that processes stop without warning.
	Danger bool
}

// Slurm says that the op changes the Slurm state of the nodes, not their
// containers.
func (op PowerOp) Slurm() bool { return op.Group == GroupSlurm }

// The groups of the power ops.
const (
	GroupSlurm      = "slurm"
	GroupContainers = "containers"
	GroupDanger     = "danger"
)

// PowerOps are the ways to change the state of nodes, in the order a
// dialog lists them.
var PowerOps = []PowerOp{
	{"drain", "Drain", "New jobs do not start on the nodes; running jobs go on. Asks for a reason.", GroupSlurm, false},
	{"resume", "Resume", "The nodes take jobs again.", GroupSlurm, false},
	{"on", "Start", "Starts the containers (docker start), and the mesh and monitoring if they stopped.", GroupContainers, false},
	{"shutdown", "Shut down", "Stops the containers cleanly (docker stop). Their data stays. Start them again later.", GroupContainers, false},
	{"reboot", "Reboot", "Stops, then starts the containers. Running jobs on them fail.", GroupContainers, false},
	{"freeze", "Freeze", "Pauses all processes (docker pause). Slurm sees the nodes stop answering.", GroupContainers, false},
	{"unfreeze", "Unfreeze", "Lets the paused processes run again.", GroupContainers, false},
	{"cycle", "Power-cycle", "Kills, then starts the containers. Like a reset button.", GroupDanger, true},
	{"cut", "Cut power", "Kills the containers at once (docker kill). Like pulling the plug: what is not on disk is lost.", GroupDanger, true},
}

// PowerGroup is a group of power ops, as a dialog shows them together.
type PowerGroup struct {
	ID, Label string
	Ops       []PowerOp
}

// PowerGroups returns the power ops by group, in order.
func PowerGroups() []PowerGroup {
	groups := []PowerGroup{
		{ID: GroupSlurm, Label: "Slurm (scontrol)"},
		{ID: GroupContainers, Label: "Containers"},
		{ID: GroupDanger, Label: "Can lose data"},
	}
	for i := range groups {
		for _, op := range PowerOps {
			if op.Group == groups[i].ID {
				groups[i].Ops = append(groups[i].Ops, op)
			}
		}
	}
	return groups
}

// powerAliases are the words other parts of sind ui and docker users say for
// the subcommands of sind power. "resume" is not one: it is the Slurm op.
var powerAliases = map[string]string{
	"start": "on", "stop": "shutdown", "off": "shutdown", "restart": "reboot",
	"pause": "freeze", "unpause": "unfreeze",
}

// PowerOpOf returns the power operation that word names: an op of
// PowerOps, or one of the aliases start, stop, off, restart, pause and
// unpause.
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

// maxReason is the longest reason of a drain, in characters.
const maxReason = 200

func buildPower(f Form, c Context) (Command, error) {
	realm, cl, err := findCluster(f, c)
	if err != nil {
		return Command{}, err
	}
	op, ok := PowerOpOf(strings.TrimSpace(f.Get("op")))
	if !ok {
		return Command{}, fmt.Errorf("op: pick what to do with the nodes")
	}
	if op.Slurm() {
		return buildSlurmState(f, c, realm, cl.Name, op)
	}
	picked, err := pickNodes(f, c, realm, cl.Name, "")
	if err != nil {
		return Command{}, err
	}
	shorts := shortNames(picked)
	cmd := NewCommand(sindArgv(realm, append([]string{"power", op.ID}, nodeWords(cl.Name, shorts)...)...)...)
	cmd.OK = op.Label + " " + countNodes(shorts)
	if stops(op.ID) {
		cmd.Warns = append(cmd.Warns, powerWarnings(c.Snap.ClusterNodes(realm, cl.Name), picked, c.Snap.Status(realm, cl.Name), cl.Name)...)
	}
	if op.Danger {
		cmd.Warns = append(cmd.Warns, "Cannot be undone: processes on these nodes stop without warning.")
	}
	return cmd, nil
}

// stops reports whether the sind power subcommand id takes nodes out of
// service, for a while or until they start again.
func stops(id string) bool {
	return slices.Contains([]string{"shutdown", "reboot", "freeze", "cycle", "cut"}, id)
}

// powerWarnings says what stops working when the picked nodes of the
// cluster, whose nodes are all and whose status st may be nil, go down: the
// controller in control, the standby controller of a pair, the db node or
// the last running workers.
func powerWarnings(all, picked []cluster.NodeSummary, st *cluster.Status, clusterName string) []string {
	isPicked := func(n cluster.NodeSummary) bool {
		return slices.ContainsFunc(picked, func(p cluster.NodeSummary) bool { return p.Container == n.Container })
	}
	var ctls []cluster.NodeSummary
	for _, n := range all {
		if n.Role == config.RoleController {
			ctls = append(ctls, n)
		}
	}
	warns := controllerWarnings(ctls, activeController(ctls, st), isPicked, clusterName)
	for _, n := range picked {
		if n.Role == config.RoleDB {
			warns = append(warns, fmt.Sprintf("%s runs slurmdbd and MariaDB. While it is down, sacct and sacctmgr fail; slurmctld keeps the accounting records until it is back.", ShortName(n)))
		}
	}
	running, pickedRunning := 0, 0
	for _, n := range all {
		if n.Role == config.RoleWorker && n.State == cluster.StateRunning {
			running++
			if isPicked(n) {
				pickedRunning++
			}
		}
	}
	if running > 0 && pickedRunning == running {
		warns = append(warns, fmt.Sprintf("These are all the running workers of %s: new jobs wait until a worker is back.", clusterName))
	}
	return warns
}

// activeController returns the short name of the controller of ctls that
// is in control: the one the status st, which may be nil, says is, else the
// primary while it runs, else the backup while it runs, "" for none.
func activeController(ctls []cluster.NodeSummary, st *cluster.Status) string {
	if st != nil {
		for _, n := range st.Nodes {
			if n.Role == config.RoleController && n.Health != nil && n.Health.HA != nil && n.Health.HA.InControl {
				short, _, _ := strings.Cut(n.Name, ".")
				return short
			}
		}
	}
	active := ""
	for _, n := range ctls {
		if n.State != cluster.StateRunning {
			continue
		}
		if short := ShortName(n); active == "" || short != cluster.ControllerBackupShortName {
			active = short
		}
	}
	return active
}

// controllerWarnings says what goes when the picked running controllers of
// ctls go down, of which active is in control: slurmctld stops until another
// controller takes over, or for good, or, for the standby controller of a
// pair alone, the failover.
func controllerWarnings(ctls []cluster.NodeSummary, active string, isPicked func(cluster.NodeSummary) bool, clusterName string) []string {
	var gone, left []string
	for _, n := range ctls {
		switch {
		case n.State != cluster.StateRunning:
		case isPicked(n):
			gone = append(gone, ShortName(n))
		default:
			left = append(left, ShortName(n))
		}
	}
	switch {
	case len(gone) == 0:
		return nil
	case len(left) > 0 && !slices.Contains(gone, active):
		// The controller in control keeps running.
		return []string{fmt.Sprintf("%s is the standby controller. %s stays in control and %s keeps taking jobs, but has no failover while %s is down.",
			gone[0], active, clusterName, gone[0])}
	case len(left) > 0:
		return []string{fmt.Sprintf("%s runs slurmctld. %s takes over after SlurmctldTimeout; until then %s takes no jobs.",
			gone[0], left[0], clusterName)}
	case len(gone) > 1:
		return []string{fmt.Sprintf("%s run slurmctld. While they are down, %s takes no jobs, and sbatch, squeue and sinfo fail.",
			strings.Join(gone, " and "), clusterName)}
	default:
		return []string{fmt.Sprintf("%s runs slurmctld. While it is down, %s takes no jobs, and sbatch, squeue and sinfo fail.",
			gone[0], clusterName)}
	}
}

// buildSlurmState builds the command of the ops of the group Slurm: sind
// exec runs scontrol update on the cluster's submitter or controller, for
// the picked workers.
func buildSlurmState(f Form, c Context, realm, clusterName string, op PowerOp) (Command, error) {
	picked, err := pickNodes(f, c, realm, clusterName, "drained or resumed")
	if err != nil {
		return Command{}, err
	}
	shorts := shortNames(picked)
	if st := c.Snap.Status(realm, clusterName); st != nil {
		for _, n := range st.Nodes {
			short, _, _ := strings.Cut(n.Name, ".")
			switch {
			case n.Managed:
			case n.Role == config.RoleController:
				return Command{}, fmt.Errorf("cluster %s is unmanaged: sind runs no Slurm on it", clusterName)
			case slices.Contains(shorts, short):
				return Command{}, fmt.Errorf("nodes: %s is unmanaged: it runs no slurmd", short)
			}
		}
	}
	words := []string{"exec"}
	words = append(words, clusterWord(clusterName)...)
	words = append(words, "--", "scontrol", "update", "NodeName="+strings.Join(nodeWords(config.DefaultClusterName, shorts), ","))
	var cmd Command
	if op.ID == "drain" {
		reason := strings.TrimSpace(f.Get("reason"))
		switch {
		case reason == "":
			return Command{}, &Missing{Field: "reason", Text: "write why the nodes drain, such as disk test"}
		case utf8.RuneCountInString(reason) > maxReason || !plainText(reason):
			return Command{}, fmt.Errorf("reason: write one line of at most %d characters", maxReason)
		}
		cmd = NewCommand(sindArgv(realm, append(words, "State=DRAIN", "Reason="+reason)...)...)
		cmd.Notes = append(cmd.Notes, "Running jobs go on; Slurm starts no new job on the nodes. sinfo shows them as draining, then drained. Resume puts them back.")
	} else {
		cmd = NewCommand(sindArgv(realm, append(words, "State=RESUME")...)...)
		cmd.Notes = append(cmd.Notes, "The nodes take jobs again. A node that is down because its container stopped comes back once you start it.")
	}
	cmd.OK = op.Label + " " + countNodes(shorts)
	if !slices.ContainsFunc(c.Snap.ClusterNodes(realm, clusterName), func(n cluster.NodeSummary) bool {
		return n.Role == config.RoleController && n.State == cluster.StateRunning
	}) {
		cmd.Warns = append(cmd.Warns, "No controller of "+clusterName+" runs, so scontrol fails. Start the controller first.")
	}
	return cmd, nil
}

// shortNames returns the short names of the nodes.
func shortNames(nodes []cluster.NodeSummary) []string {
	shorts := make([]string, len(nodes))
	for i, n := range nodes {
		shorts[i] = ShortName(n)
	}
	return shorts
}

// countNodes names the nodes for a button: the one node, or how many.
func countNodes(shorts []string) string {
	if len(shorts) == 1 {
		return shorts[0]
	}
	return fmt.Sprintf("%d nodes", len(shorts))
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
