// SPDX-License-Identifier: LGPL-3.0-or-later

// Package action turns the forms of sind ui's dialogs into the sind commands
// they run.
//
// Each Action's Build is the only place that makes a command's argument
// vector. The dialog shows the command's Display, and the runner executes
// its Argv, so the command shown is the command run. A browser sends field
// values, never a command line, and Build checks every one of them against
// the rules of sind and the current snapshot.
package action

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// Form holds the fields of a dialog as the browser posted them.
type Form = url.Values

// Command is what an action runs.
type Command struct {
	// Argv is the command line, run as it is with no shell. Argv[0] is
	// "sind".
	Argv []string
	// Display is Argv quoted for a POSIX shell: what the dialog shows and
	// Copy copies.
	Display string
	// Notes say what the user should know before running the command;
	// Warns are those to read with care.
	Notes []string
	Warns []string
	// Config is a file that the command reads and that sind ui saves
	// first, if the action has one.
	Config *File
	// Dirs are directories that sind ui creates before it runs the
	// command.
	Dirs []string
	// Script is a shell script that does what running the command does,
	// with its file and directories, for Copy; empty where Display does.
	Script string
	// Nodes are the nodes the command will make, for the preview.
	Nodes []PlannedNode
	// Summary is a line about Nodes.
	Summary string
	// Containers, CPUs and MemoryMB are what Nodes may use together: the
	// containers, the CPUs and the memory in MiB. OverHost says whether
	// MemoryMB is more than the Docker host has, which the plan asks to
	// accept.
	Containers, CPUs, MemoryMB int
	OverHost                   bool
	// Budget is what the new nodes of a command that adds workers take
	// of the Docker host, beside the nodes that run on it, nil while sind
	// ui does not know.
	Budget *Budget
	// Images are the images of Nodes and what sind ui knows of them.
	Images []PlanImage
	// After is what the command runs once the nodes are up, and SlurmConf
	// the slurm.conf and sind-nodes.conf that it writes, for the plan of a
	// new cluster whose config is valid.
	After     []Step
	SlurmConf string
	// Problems are the fields that the command cannot take as they are.
	// The command does not run while it has any, but its plan shows.
	Problems []Problem
	// OK labels the button that runs the command, such as "Delete
	// worker-5"; empty for the dialog's own label.
	OK string
	// Goes and Stays say what a command that deletes removes and what it
	// keeps. A command with Losses says it for each cluster there, and
	// Stays is then what else stays.
	Goes, Stays []string
	Losses      []Loss
	// Ack is what the user ticks to confirm that data volumes go, ""
	// when none does; a name in it is in backquotes.
	Ack string
	// Parts are the commands of an action that runs several, each as a
	// task of its own, instead of Argv; Display shows them one per line.
	Parts []Part
}

// Part is one of the commands of an action that runs several.
type Part struct {
	// Action is the id of the action whose task the part is, as the
	// task's page shows it.
	Action string
	// Realm and Cluster are what the part works on.
	Realm, Cluster string
	Argv           []string
	Display        string
}

// NewParts returns the command that runs the parts.
func NewParts(parts []Part) Command {
	lines := make([]string, len(parts))
	for i, p := range parts {
		lines[i] = p.Display
	}
	return Command{Display: strings.Join(lines, "\n"), Parts: parts}
}

// Problem is a field of a form that is wrong. Build reports it, instead of
// an error, where it can still make the rest of the plan.
type Problem struct {
	// Field is the form field, or "" for the config as a whole.
	Field string
	// Label names the field for the user.
	Label string
	// Text says what is wrong and how to fix it.
	Text string
	// Default says that the plan uses the field's default until it is
	// fixed.
	Default bool
}

// Missing is the error of a form that lacks a value that the command needs,
// such as a field that is still empty. A dialog shows it as a hint until the
// user tries to run the command, as nothing is wrong yet.
type Missing struct {
	// Field is the form field.
	Field string
	// Text says what to fill in, as the second part of the error.
	Text string
}

func (m *Missing) Error() string { return m.Field + ": " + m.Text }

// Hint is Text as a sentence.
func (m *Missing) Hint() string {
	return strings.ToUpper(m.Text[:1]) + m.Text[1:] + "."
}

// File is a file that sind ui saves before it runs a command, readable by
// its owner alone.
type File struct {
	Path    string
	Content string
}

// PlannedNode is a node that a command will make.
type PlannedNode struct {
	Short string
	Role  string
	// Key is the node of the form that makes it: a role, backup, or the
	// key of a worker group.
	Key string
}

// NewCommand returns the command with the arguments argv.
func NewCommand(argv ...string) Command {
	return Command{Argv: argv, Display: Quote(argv)}
}

// Fingerprint identifies what the command does: its command line and the
// file it saves. The dialog sends it back with the run, so that a command
// that changed since the preview is not run unseen.
func (c Command) Fingerprint() string {
	h := sha256.New()
	h.Write([]byte(c.Display))
	if c.Config != nil {
		h.Write([]byte{0})
		h.Write([]byte(c.Config.Path))
		h.Write([]byte{0})
		h.Write([]byte(c.Config.Content))
	}
	for _, d := range c.Dirs {
		h.Write([]byte{1})
		h.Write([]byte(d))
	}
	if c.Ack != "" {
		// A data volume that came while the user looked is not deleted
		// unseen.
		h.Write([]byte{2})
		h.Write([]byte(c.Ack))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Quote writes argv as a command line for a POSIX shell.
func Quote(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = quoteWord(a)
	}
	return strings.Join(words, " ")
}

func quoteWord(s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_@%+=:,./-", r))
	}) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Context is what Build knows besides the form.
type Context struct {
	// Snap is what sind ui shows, which Build checks the form against.
	Snap store.Snapshot
	// Home is the user's home directory, which "~" in a path stands for.
	Home string
	// ConfigDir is where sind ui saves the configs of the clusters it
	// creates.
	ConfigDir string
	// Stat looks at a file of the host, as os.Stat; nil checks none.
	Stat func(name string) (fs.FileInfo, error)
	// Image tells what sind ui knows of an image; nil knows nothing.
	Image func(image string) ImageFacts
	// Worker tells the size of a worker container; nil knows nothing.
	Worker func(container string) WorkerFacts
	// Sizes tells the sizes of node containers, in their order, as one
	// look at Docker; nil knows nothing.
	Sizes func(containers []string) []WorkerFacts
}

// WorkerFacts is what sind ui knows of the size of a worker container.
type WorkerFacts struct {
	// CPUs is its CPU limit, 0 for none; Memory its memory limit in bytes,
	// 0 for none.
	CPUs   int
	Memory int64
	// Err says why sind ui cannot look at it.
	Err string
}

// Action is a change that a dialog makes by running one command.
type Action struct {
	// ID names the action in URLs and templates.
	ID string
	// Build checks the form and makes the command. Its error is shown to
	// the user, so it says what to change.
	Build func(f Form, c Context) (Command, error)
	// Typed returns what the user must type to confirm the command, or
	// "" if none. The server checks it, whatever the browser did.
	Typed func(f Form) string
	// TypedWhat says what the typed word is when the name of the action
	// does not tell: "the number of workers". "" for nothing.
	TypedWhat func(f Form) string
	// Cluster returns the cluster the command works on, or "".
	Cluster func(f Form) string
}

// ConfirmField is the field that holds the typed confirmation.
const ConfirmField = "confirm"

// AckField is the box that the user ticks to confirm that data volumes go.
const AckField = "ack"

// Acked reports whether the form confirms the loss of the data volumes
// of the command, if any go.
func (c Command) Acked(f Form) bool {
	return c.Ack == "" || f.Get(AckField) == "on"
}

// Want returns what the user must type to confirm the command, or "".
func (a Action) Want(f Form) string {
	if a.Typed == nil {
		return ""
	}
	return a.Typed(f)
}

// WantWhat says what the word of Want is, or "".
func (a Action) WantWhat(f Form) string {
	if a.TypedWhat == nil || a.Want(f) == "" {
		return ""
	}
	return a.TypedWhat(f)
}

// Confirmed reports whether the form holds the confirmation that a requires,
// if it requires one.
func (a Action) Confirmed(f Form) bool {
	want := a.Want(f)
	return want == "" || strings.TrimSpace(f.Get(ConfirmField)) == want
}

// Target returns the realm and the cluster that the command of the form
// works on; the cluster is empty for none.
func (a Action) Target(f Form) (realm, cluster string) {
	realm = Realm(f)
	if a.Cluster != nil {
		cluster = a.Cluster(f)
	}
	return realm, cluster
}

// Realm returns the realm of the form, the default realm when it names none.
func Realm(f Form) string {
	if r := strings.TrimSpace(f.Get("realm")); r != "" {
		return r
	}
	return mesh.DefaultRealm
}

// Lookup returns the action with the ID id.
func Lookup(id string) (Action, bool) {
	a, ok := registry[id]
	return a, ok
}

// IDs returns the IDs of all actions, in order.
func IDs() []string {
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

var registry = map[string]Action{}

// register adds the actions to the registry.
func register(as ...Action) {
	for _, a := range as {
		if _, dup := registry[a.ID]; dup {
			panic("action " + a.ID + " registered twice")
		}
		registry[a.ID] = a
	}
}

// formCluster is the Cluster of actions whose field "cluster" names it.
func formCluster(f Form) string { return strings.TrimSpace(f.Get("cluster")) }

// sindArgv starts the command line of sind in realm: --realm names any realm
// but the default one, as the default needs no flag.
func sindArgv(realm string, words ...string) []string {
	argv := []string{"sind"}
	if realm != mesh.DefaultRealm {
		argv = append(argv, "--realm", realm)
	}
	return append(argv, words...)
}

// checkRealm returns the realm of the form once it is a valid name.
func checkRealm(f Form) (string, error) {
	realm := Realm(f)
	if err := nameError("realm", realm); err != nil {
		return "", fmt.Errorf("realm: %w", err)
	}
	return realm, nil
}

// findCluster returns the cluster that the form's field "cluster" names in
// the form's realm, and the realm.
func findCluster(f Form, c Context) (string, cluster.Summary, error) {
	realm, err := checkRealm(f)
	if err != nil {
		return "", cluster.Summary{}, err
	}
	name := formCluster(f)
	if name == "" {
		return "", cluster.Summary{}, &Missing{Field: "cluster", Text: "pick a cluster"}
	}
	if err := config.CheckName("cluster", name); err != nil {
		return "", cluster.Summary{}, fmt.Errorf("cluster: %w", err)
	}
	s, ok := c.Snap.Cluster(realm, name)
	if !ok {
		return "", s, fmt.Errorf("realm %s has no cluster %s", realm, name)
	}
	return realm, s, nil
}

// clusterWord is the word of a command line that names the cluster: none for
// the default cluster, which the commands take without a name.
func clusterWord(name string) []string {
	if name == config.DefaultClusterName {
		return nil
	}
	return []string{name}
}

// number reads the whole number of field in the form, from min to max; an
// empty field is def, or an error where def is -1.
func number(f Form, field, label string, minimum, maximum, def int) (int, error) {
	s := strings.TrimSpace(f.Get(field))
	if s == "" && def >= 0 {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < minimum || n > maximum {
		return 0, fmt.Errorf("%s: write a number from %d to %d", label, minimum, maximum)
	}
	return n, nil
}

// plainText reports whether s holds only printable characters, which a path
// or a name needs to reach a command line and a file unchanged.
func plainText(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || r == unicode.ReplacementChar })
}

// containerPattern is what a Docker container name looks like.
var containerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
