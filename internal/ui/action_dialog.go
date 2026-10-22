// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/state"
)

// actDialogKeys are the query keys that open a dialog with its fields
// filled in.
var actDialogKeys = []string{
	"realm", "cluster", "nodes", "op", "container",
	// The fields of the New cluster dialog, which a link may fill in to
	// start from a cluster that exists.
	"name", "image", "wait", "backup", "db", "api", "submitter", "workers", "size", "cpus", "memory", "tmp", "users",
	"dtype", "dir", "mkdir", "mount", "monitoring", "pull", "wait_other",
}

const (
	// actMaxValue is the longest value of a field a form may post, in
	// bytes; actMaxValues is the most values.
	actMaxValue  = 1024
	actMaxValues = 512
)

// actCheckForm refuses a form with a value that is too long, or too many.
func actCheckForm(f url.Values) error {
	n := 0
	for k, vals := range f {
		n += len(vals)
		for _, v := range vals {
			if len(v) > actMaxValue {
				return fmt.Errorf("the value of %s is longer than %d bytes: %w", k, actMaxValue, ErrBadRequest)
			}
		}
	}
	if n > actMaxValues {
		return fmt.Errorf("more than %d values: %w", actMaxValues, ErrBadRequest)
	}
	return nil
}

// actSpec describes how an action's dialog looks.
type actSpec struct {
	// OK labels the button that runs the command.
	OK string
	// Danger paints the button red.
	Danger bool
	// Wide makes the dialog wide.
	Wide bool
	// NeedsCluster says that the dialog is for one cluster, named by the
	// link.
	NeedsCluster bool
	// Plan puts the preview into a pane beside the fields, which shows
	// the plan of the command even while a field is wrong.
	Plan bool
	// Fields returns the fields of the form that show their own problem.
	Fields func(f url.Values) []string
	// Sections are the parts of the form that fold, each with a line
	// that sums it up.
	Sections []actSection
	// Title and Lead are the heading and the line below it.
	Title func(d *actDialog) string
	Lead  func(d *actDialog) string
	// Defaults fills the fields that the link does not give.
	Defaults func(d *actDialog)
}

// actSpecs are the dialogs, by action id.
var actSpecs = map[string]actSpec{
	"create-cluster": {
		OK: "Create", Wide: true, Plan: true, Fields: action.ClusterFieldsOf, Sections: actClusterSections,
		Title: func(*actDialog) string { return "New cluster" },
		Lead: func(d *actDialog) string {
			return "In realm " + d.Realm + ". sind ui saves the config, then runs sind create cluster with it."
		},
		Defaults: func(d *actDialog) {
			d.def("name", action.FreeName(d.Snap, d.Realm, "hpc"))
			d.def("image", config.DefaultImage)
			if w := d.Form.Get("wait"); w != "" && w != "other" && !slices.Contains(action.Waits, w) {
				// A time that has no button of its own.
				d.Form.Set("wait", "other")
				d.Form.Set("wait_other", w)
			}
			d.def("wait", action.DefaultWait)
			d.def("db", "on")
			d.def("submitter", "on")
			if !d.Form.Has("wg") {
				d.Form.Set("wg", "w0")
				d.Form.Set("w0-count", cmp.Or(d.Form.Get("workers"), "2"))
			}
			if !d.Form.Has("size") {
				// A link with a size of its own gives custom values.
				d.Form.Set("size", "s")
				if d.Form.Has("cpus") || d.Form.Has("memory") || d.Form.Has("tmp") {
					d.Form.Set("size", "x")
				}
			}
			d.def("cpus", "1")
			d.def("memory", "512m")
			d.def("dtype", "hostPath")
			d.def("dir", actDefaultDir(d.Form.Get("name")))
			d.def("mkdir", "on")
			if !d.Form.Has("pull") {
				// Pull follows the image until the user ticks the box.
				d.Form.Set("pull_auto", "1")
				if pull, _ := action.PullByDefault(d.Form.Get("image")); pull {
					d.Form.Set("pull", "on")
				}
			}
		},
	},
	"create-worker": {
		OK: "Add", NeedsCluster: true,
		Title: func(d *actDialog) string { return "Add workers to " + d.Cluster },
		Lead: func(*actDialog) string {
			return "New workers take the image, CPUs and memory of the newest worker, unless you change them."
		},
		Defaults: func(d *actDialog) {
			d.def("count", "1")
		},
	},
	"delete-cluster": {
		OK: "Delete", Danger: true, NeedsCluster: true,
		Title: func(d *actDialog) string { return "Delete cluster " + d.Cluster },
		Lead: func(d *actDialog) string {
			return fmt.Sprintf("Removes its %d containers, its network and its volumes.", len(d.Snap.ClusterNodes(d.Realm, d.Cluster)))
		},
	},
	"delete-worker": {
		OK: "Delete", Danger: true, NeedsCluster: true,
		Title: func(d *actDialog) string { return "Delete workers of " + d.Cluster },
		Lead: func(*actDialog) string {
			return "Removes the worker containers and their DNS records, then tells slurmctld."
		},
	},
	"power": {
		OK: "Run", NeedsCluster: true,
		Title: func(d *actDialog) string { return "Power: nodes of " + d.Cluster },
		Lead:  func(*actDialog) string { return "At most 8 nodes at a time." },
		Defaults: func(d *actDialog) {
			op, ok := action.PowerOpOf(d.Form.Get("op"))
			if !ok {
				op = action.PowerOps[0]
			}
			d.Form.Set("op", op.ID)
		},
	},
	"connect": {
		OK:    "Connect",
		Title: func(*actDialog) string { return "Connect a container" },
		Lead: func(d *actDialog) string {
			return "Attaches a container that sind did not make, such as a kind node, to the mesh and cluster networks of realm " +
				d.Realm + ". It then resolves the nodes by name."
		},
	},
	"disconnect": {
		OK:    "Disconnect",
		Title: func(*actDialog) string { return "Disconnect a container" },
		Lead: func(d *actDialog) string {
			return "Detaches a container that sind did not make from the networks of realm " + d.Realm + ". The container keeps running."
		},
	},
	"delete-realm": {
		OK: "Delete", Danger: true,
		Title: func(d *actDialog) string { return "Delete realm " + d.Realm },
		Lead: func(d *actDialog) string {
			r, _ := d.Snap.Realm(d.Realm)
			return fmt.Sprintf("Deletes all %d clusters of realm %s, its mesh, DNS server, SSH relay, monitoring stack and the exported SSH config. Connected containers stay; sind only detaches them.", r.Clusters, d.Realm)
		},
	},
}

// actSection is a part of a form that folds.
type actSection struct {
	ID string
	// Fields are those in the section, whose problems it counts.
	Fields []string
	// Sum is the line that sums the section up.
	Sum func(p actPreview) string
}

// actClusterSections are the sections of the New cluster dialog.
var actClusterSections = []actSection{
	{ID: "data", Fields: []string{"dtype", "dir", "mkdir", "mount"}, Sum: func(p actPreview) string {
		if p.Form.Get("dtype") == "volume" {
			return actWithMount("Docker volume", p.Form)
		}
		sum := strings.TrimSpace(p.Form.Get("dir"))
		if len(p.Cmd.Dirs) > 0 {
			sum += ", created"
		}
		return actWithMount(sum, p.Form)
	}},
	{ID: "run", Fields: []string{"pull", "wait", "wait_other"}, Sum: actRunSum},
}

// actRunSum sums up the run options of a new cluster.
func actRunSum(p actPreview) string {
	sum := "No pull"
	if p.PullOn() {
		sum = "Pull"
	}
	switch wait := p.Form.Get("wait"); wait {
	case "0":
		return sum + ", no wait limit"
	case "other":
		return sum + ", wait " + strings.TrimSpace(p.Form.Get("wait_other"))
	case "":
		return sum + ", wait " + action.DefaultWait
	default:
		return sum + ", wait " + wait
	}
}

// PullOn reports whether the command of a new cluster pulls the image.
func (p actPreview) PullOn() bool {
	return slices.Contains(p.Cmd.Argv, "--pull")
}

// PullHint says why the command of a new cluster pulls the image, or not.
func (p actPreview) PullHint() string {
	if p.Form.Get("pull_auto") == "" {
		return "Gets the newest image first, even if Docker has one."
	}
	pull, why := action.PullByDefault(strings.TrimSpace(p.Form.Get("image")))
	if pull {
		return "On, as " + why + ". Create gets the newest image first."
	}
	return "Off, as " + why + "."
}

// actWithMount adds the mount path of the data to sum, if it is not the
// default.
func actWithMount(sum string, f url.Values) string {
	if m := strings.TrimSpace(f.Get("mount")); m != "" {
		return sum + ", at " + m
	}
	return sum
}

// actSectionView is a section as a preview shows it.
type actSectionView struct {
	ID, Summary string
	// Bad is how many problems the section has.
	Bad int
}

// Sections sum up the sections of the dialog.
func (p actPreview) Sections() []actSectionView {
	var out []actSectionView
	for _, sec := range p.Spec.Sections {
		v := actSectionView{ID: sec.ID, Summary: sec.Sum(p)}
		for _, pr := range p.Cmd.Problems {
			if slices.Contains(sec.Fields, pr.Field) {
				v.Bad++
			}
		}
		out = append(out, v)
	}
	return out
}

// actEff are the node defaults of a new cluster, which a node takes where
// its own field is empty.
type actEff struct {
	Image, CPUs, Memory, TmpSize string
}

// actEffOf returns the node defaults of the form.
func actEffOf(f url.Values) actEff {
	e := actEff{Image: strings.TrimSpace(f.Get("image")), CPUs: "1", Memory: config.DefaultMemory, TmpSize: config.DefaultTmpSize}
	if e.Image == "" {
		e.Image = config.DefaultImage
	}
	if f.Get("size") == "x" {
		e.CPUs = cmp.Or(strings.TrimSpace(f.Get("cpus")), e.CPUs)
		e.Memory = cmp.Or(strings.TrimSpace(f.Get("memory")), e.Memory)
		e.TmpSize = cmp.Or(strings.TrimSpace(f.Get("tmp")), e.TmpSize)
		return e
	}
	for _, size := range action.Sizes {
		if size.ID == f.Get("size") {
			e.CPUs, e.Memory, e.TmpSize = strconv.Itoa(size.CPUs), size.Memory, cmp.Or(size.TmpSize, e.TmpSize)
		}
	}
	return e
}

// actNodeView is a node of the New cluster dialog: a role or a worker group,
// with its size button and editor.
type actNodeView struct {
	Key, Label string
	// Group says that the node is a worker group.
	Group bool
	// Eff are the node defaults, which the editor shows as placeholders.
	Eff  actEff
	form url.Values
	// first is the index of the first worker of a group.
	first int
}

// actNodeOf returns the node key of the form.
func actNodeOf(f url.Values, key string) actNodeView {
	n := actNodeView{Key: key, Label: key, Eff: actEffOf(f), form: f}
	if !slices.Contains(action.NodeRoles, key) {
		n.Group, n.Label = true, "workers"
	}
	return n
}

// Val returns the value of the node's field attr.
func (n actNodeView) Val(attr string) string { return n.form.Get(n.Key + "-" + attr) }

// On reports whether the node's checkbox attr is ticked.
func (n actNodeView) On(attr string) bool { return actOn(n.Val(attr)) }

// Managed says whether the node can be unmanaged: the submitter cannot.
func (n actNodeView) Managed() bool { return n.Key != "submitter" }

// UnmanagedHint says what an unmanaged node is.
func (n actNodeView) UnmanagedHint() string {
	switch {
	case n.Key == "controller":
		return "Makes the cluster unmanaged: sind writes no Slurm config and starts no Slurm daemons."
	case n.Group:
		return "The workers start without slurmd."
	}
	return "The node starts without its daemons."
}

// actList is a field of a node that takes a list.
type actList struct{ Attr, Example string }

// Lists are the fields of the node that take a list.
func (n actNodeView) Lists() []actList {
	return []actList{{"capAdd", "such as SYS_ADMIN"}, {"capDrop", "such as NET_RAW"}, {"devices", "such as /dev/fuse"}, {"securityOpt", "such as seccomp=unconfined"}}
}

// Chip is the text of the node's size button: its own values, or
// "defaults".
func (n actNodeView) Chip() string {
	var parts []string
	if v := strings.TrimSpace(n.Val("cpus")); v != "" {
		parts = append(parts, v+" CPU"+map[bool]string{true: "", false: "s"}[v == "1"])
	}
	if v := strings.TrimSpace(n.Val("memory")); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(n.Val("tmp")); v != "" {
		parts = append(parts, "/tmp "+v)
	}
	if strings.TrimSpace(n.Val("image")) != "" {
		parts = append(parts, "own image")
	}
	if n.Managed() && n.On("unmanaged") {
		parts = append(parts, "unmanaged")
	}
	for _, l := range n.Lists() {
		if strings.TrimSpace(n.Val(l.Attr)) != "" {
			parts = append(parts, "more")
			break
		}
	}
	if len(parts) == 0 {
		return "defaults"
	}
	return strings.Join(parts, " · ")
}

// Own says that the node has values of its own.
func (n actNodeView) Own() bool { return n.Chip() != "defaults" }

// Open says that the node's editor starts open: where it has values of
// its own.
func (n actNodeView) Open() bool { return n.Own() }

// Range names the workers of a group, as the dialog opens.
func (n actNodeView) Range() string {
	count, err := strconv.Atoi(strings.TrimSpace(n.Val("count")))
	if err != nil || count < 1 {
		count = 1
	}
	return actRange(n.first, n.first+count-1)
}

// actRange names the workers first to last.
func actRange(first, last int) string {
	if first == last {
		return fmt.Sprintf("worker-%d", first)
	}
	return fmt.Sprintf("worker-%d … worker-%d", first, last)
}

// actOn reports whether a checkbox's value is ticked: on, or the 1 of a
// link.
func actOn(v string) bool { return slices.Contains([]string{"on", "1", "true"}, strings.TrimSpace(v)) }

// Node returns the node key of the dialog; a worker group knows the index
// of its first worker.
func (d *actDialog) Node(key string) actNodeView {
	n := actNodeOf(d.Form, key)
	for _, g := range d.Groups() {
		if g == key {
			break
		}
		n.first += actNodeOf(d.Form, g).count()
	}
	return n
}

// count is the number of workers of a group, 1 where its field is wrong.
func (n actNodeView) count() int {
	c, err := strconv.Atoi(strings.TrimSpace(n.Val("count")))
	if err != nil || c < 1 {
		return 1
	}
	return c
}

// Groups are the keys of the worker groups.
func (d *actDialog) Groups() []string { return action.WorkerGroups(d.Form) }

// Sizes are the node defaults that the dialog offers.
func (d *actDialog) Sizes() []action.Size { return action.Sizes }

// actDialog is what the dialog's templates render from.
type actDialog struct {
	*View
	ID string
	// Spec is how the dialog looks.
	Spec actSpec
	// Form holds the values of the fields.
	Form url.Values
	// Realm and Cluster are those of the dialog.
	Realm, Cluster string
	// Title, Lead and Typed are the heading, the line below it and what the
	// user must type to confirm, if anything.
	Title, Lead, Typed string
	// Nodes are the nodes of the cluster that the user can pick.
	Nodes []actNodeChoice
	// Ops are the power operations.
	Ops []action.PowerOp
	// Waits are the choices of the wait for a new cluster.
	Waits []actChoice
	// Lock is who holds the realm's lock, if the command will wait for it.
	Lock *state.LockHolder
}

// actChoice is an option of a select.
type actChoice struct{ Value, Label string }

// actWaits are the choices of the wait for a new cluster: those of
// action.Waits, and another time.
var actWaits = []actChoice{
	{"2m", "2m"}, {"5m", "5m"}, {"10m", "10m"}, {"0", "No limit"}, {"other", "Other"},
}

// actMemories are the memories of a node that the field suggests.
var actMemories = []string{"512m", "1g", "2g", "4g", "8g"}

// actRole is a role of the New cluster dialog that is a checkbox.
type actRole struct {
	Name, Label, Small, Hint string
	// Sub sets the row in below the one above it.
	Sub bool
}

// actRoles are the roles of the New cluster dialog that are a checkbox.
var actRoles = []actRole{
	{"backup", "backup", "controller-backup", "A second slurmctld that takes over. Same image and size as the controller.", true},
	{"db", "db", "", "MariaDB and slurmdbd: accounting.", false},
	{"api", "api", "", "slurmrestd. Needs slurmrestd in the image; sind-node images have it from Slurm 26.05.", false},
	{"submitter", "submitter", "", "The login node. sind enter puts you here.", false},
}

// Roles are the roles of the New cluster dialog that are a checkbox.
func (d *actDialog) Roles() []actRole { return actRoles }

// Memories are the memories of a node that the field suggests.
func (d *actDialog) Memories() []string { return actMemories }

// On reports whether the checkbox field is ticked: on, or the 1 of a link.
func (d *actDialog) On(field string) bool { return actOn(d.Form.Get(field)) }

// DirFollows reports whether the directory is the one that the name gives,
// which then follows the name as the user types it.
func (d *actDialog) DirFollows() bool { return d.Form.Get("dir") == actDefaultDir(d.Form.Get("name")) }

// actDefaultDir is the directory of the data of a new cluster name.
func actDefaultDir(name string) string { return "~/sind/" + name }

// actNodeChoice is a node of the picker of the power and delete worker
// dialogs.
type actNodeChoice struct {
	Short, Role, State string
	On                 bool
}

// def sets the field to v unless the link gave a value for it.
func (d *actDialog) def(field, v string) {
	if !d.Form.Has(field) {
		d.Form.Set(field, v)
	}
}

// Val returns the value of the field.
func (d *actDialog) Val(field string) string { return d.Form.Get(field) }

// Is reports whether the field has the value.
func (d *actDialog) Is(field, value string) bool { return d.Form.Get(field) == value }

// Body is the name of the block with the dialog's fields.
func (d *actDialog) Body() string { return "act-dlg-" + d.ID }

// actLocks reports whether the command of the action takes the realm lock,
// as sind does: every command that changes a realm does, but the power
// commands that only stop or pause containers.
func actLocks(a action.Action, f action.Form) bool {
	if a.ID != "power" {
		return true
	}
	op, _ := action.PowerOpOf(f.Get("op"))
	return slices.Contains([]string{"on", "reboot", "cycle"}, op.ID)
}

// actDialogRoute answers with a dialog, filled in with the fields in the
// query.
func actDialogRoute(s *Server, w http.ResponseWriter, r *http.Request) {
	a, ok := actLookup(s, w, r)
	if !ok {
		return
	}
	v, err := s.View(r, actDialogKeys...)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	spec := actSpecs[a.ID]
	d := &actDialog{
		View: v, ID: a.ID, Spec: spec, Form: url.Values{},
		Ops: action.PowerOps, Waits: actWaits,
	}
	for _, k := range actDialogKeys {
		if vals := v.Params[k]; len(vals) > 0 {
			d.Form[k] = slices.Clone(vals)
		}
	}
	d.Realm = action.Realm(d.Form)
	if err := config.CheckName("realm", d.Realm); err != nil {
		s.Fail(w, r, fmt.Errorf("realm: %v: %w", err, ErrBadRequest))
		return
	}
	d.Form.Set("realm", d.Realm)
	if spec.NeedsCluster {
		if err := d.pickCluster(); err != nil {
			s.Fail(w, r, err)
			return
		}
	}
	if spec.Defaults != nil {
		spec.Defaults(d)
	}
	d.Typed = a.Want(d.Form)
	d.Title, d.Lead = spec.Title(d), spec.Lead(d)
	if r.Header.Get("HX-Request") != "true" {
		// A dialog opened by its URL, as a link without script or the
		// jump box does: the dialog over a page of its own.
		pv := v.withData(d)
		pv.Title, pv.Content = d.Title, "act-dialog-page"
		pv.Events = s.eventsURL(nil, pv)
		s.Write(w, r, http.StatusOK, "layout", pv)
		return
	}
	s.Write(w, r, http.StatusOK, "act-dialog", v.withData(d))
}

// pickCluster reads the cluster of the link, and lists the nodes of it that
// the dialog can act on.
func (d *actDialog) pickCluster() error {
	d.Cluster = strings.TrimSpace(d.Form.Get("cluster"))
	if d.Cluster == "" {
		return fmt.Errorf("cluster: say which cluster: %w", ErrBadRequest)
	}
	if err := config.CheckName("cluster", d.Cluster); err != nil {
		return fmt.Errorf("cluster: %v: %w", err, ErrBadRequest)
	}
	picked := strings.FieldsFunc(strings.Join(d.Form["nodes"], ","), func(r rune) bool { return r == ',' || r == ' ' })
	for _, n := range d.Snap.ClusterNodes(d.Realm, d.Cluster) {
		if d.ID == "delete-worker" && n.Role != config.RoleWorker {
			continue
		}
		short := action.ShortName(n)
		d.Nodes = append(d.Nodes, actNodeChoice{Short: short, Role: string(n.Role), State: string(n.State), On: slices.Contains(picked, short)})
	}
	return nil
}

// withData returns a copy of the view that carries data.
func (v *View) withData(data any) *View {
	c := *v
	c.Data = data
	return &c
}

// actPreview is what the answer to a preview or a refused run renders from.
type actPreview struct {
	*View
	ID   string
	Spec actSpec
	// Form is the form that the preview is of.
	Form action.Form
	Cmd  action.Command
	Err  string
	Warn string
	// Danger paints the button red.
	Danger bool
	// Blocked disables the button: the form cannot run yet.
	Blocked bool
	// Shown is the fingerprint of the command, which the run checks.
	Shown string
	// Realm is the realm of the command, and Lock who holds its lock if the
	// command will wait for it.
	Realm string
	Lock  *state.LockHolder
}

// actMaxTiles is the most nodes that a plan draws one by one.
const actMaxTiles = 16

// Tiles are the nodes that the plan draws: all, or the first ones when
// there are more than actMaxTiles.
func (p actPreview) Tiles() []action.PlannedNode {
	if len(p.Cmd.Nodes) > actMaxTiles {
		return p.Cmd.Nodes[:actMaxTiles-1]
	}
	return p.Cmd.Nodes
}

// More is how many nodes the plan does not draw.
func (p actPreview) More() int { return len(p.Cmd.Nodes) - len(p.Tiles()) }

// Fields are the fields of the form that show their own problem.
func (p actPreview) Fields() []string {
	if p.Spec.Fields == nil {
		return nil
	}
	return p.Spec.Fields(p.Form)
}

// NodeViews are the nodes of a new cluster whose size buttons and ranges
// the preview updates.
func (p actPreview) NodeViews() []actNodeView {
	var out []actNodeView
	for _, key := range action.NodeRoles {
		out = append(out, actNodeOf(p.Form, key))
	}
	for _, g := range action.WorkerGroups(p.Form) {
		if actGroupPattern.MatchString(g) {
			out = append(out, actNodeOf(p.Form, g))
		}
	}
	return out
}

// actGroupPattern is the key of a worker group, which ends up in ids.
var actGroupPattern = regexp.MustCompile(`^w[0-9a-z]{1,8}$`)

// Eff are the node defaults of a new cluster.
func (p actPreview) Eff() actEff { return actEffOf(p.Form) }

// RangeOf names the workers of the group key in the plan.
func (p actPreview) RangeOf(key string) string {
	first, last := -1, -1
	for i, n := range p.Cmd.Nodes {
		if n.Key == key {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return ""
	}
	return actRange(actWorkerIndex(p.Cmd.Nodes[first]), actWorkerIndex(p.Cmd.Nodes[last]))
}

// actWorkerIndex is the number in the name of a worker.
func actWorkerIndex(n action.PlannedNode) int {
	i, _ := strconv.Atoi(strings.TrimPrefix(n.Short, "worker-"))
	return i
}

// ProblemOf returns what is wrong with the field, or "".
func (p actPreview) ProblemOf(field string) string {
	for _, pr := range p.Cmd.Problems {
		if pr.Field == field {
			return pr.Text
		}
	}
	return ""
}

// Problems is how many things keep the command from running.
func (p actPreview) Problems() int {
	if p.Err != "" {
		return 1
	}
	return len(p.Cmd.Problems)
}

// Defaulted names the fields whose defaults the plan takes, as they are
// wrong.
func (p actPreview) Defaulted() string {
	var labels []string
	for _, pr := range p.Cmd.Problems {
		if pr.Default {
			labels = append(labels, pr.Label)
		}
	}
	return strings.Join(labels, ", ")
}

// actEvaluate builds the command of the form and decides whether it may run.
func actEvaluate(v *View, a action.Action, f action.Form, ctx action.Context) actPreview {
	p := actPreview{View: v, ID: a.ID, Spec: actSpecs[a.ID], Form: f}
	p.Danger = p.Spec.Danger
	if op, ok := action.PowerOpOf(f.Get("op")); a.ID == "power" && ok && op.Danger {
		p.Danger = true
	}
	cmd, err := a.Build(f, ctx)
	if err != nil {
		p.Err, p.Blocked = err.Error(), true
		return p
	}
	p.Cmd, p.Shown = cmd, cmd.Fingerprint()
	p.Blocked = !a.Confirmed(f) || len(cmd.Problems) > 0
	p.Realm = action.Realm(f)
	if actLocks(a, f) {
		p.Lock = v.Snap.Lock(p.Realm)
	}
	return p
}

// actContext returns what the actions check a form against.
func actContext(v *View, st *actState) action.Context {
	return action.Context{Snap: v.Snap, Home: st.home, ConfigDir: st.configDir, Stat: st.stat}
}

// actFormView returns the view of a request that posts a dialog's form,
// once the form passes its limits.
func actFormView(s *Server, r *http.Request) (*View, error) {
	if err := actCheckForm(r.PostForm); err != nil {
		return nil, err
	}
	return s.View(r)
}

// actPreviewRoute answers with the command that the form would run.
func actPreviewRoute(s *Server, w http.ResponseWriter, r *http.Request) {
	a, ok := actLookup(s, w, r)
	if !ok {
		return
	}
	st, ok := actStateOf(s, w, r)
	if !ok {
		return
	}
	v, err := actFormView(s, r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Write(w, r, http.StatusOK, "act-preview", v.withData(actEvaluate(v, a, r.PostForm, actContext(v, st))))
}

// actRunRoute builds the command of the form again and runs it as a task,
// if it is the command the dialog showed. The browser follows the task.
func actRunRoute(s *Server, w http.ResponseWriter, r *http.Request) {
	a, ok := actLookup(s, w, r)
	if !ok {
		return
	}
	st, ok := actStateOf(s, w, r)
	if !ok {
		return
	}
	v, err := actFormView(s, r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	f := r.PostForm
	p := actEvaluate(v, a, f, actContext(v, st))
	switch {
	case p.Err != "":
	case len(p.Cmd.Problems) > 0:
		// The preview lists them; the button was off.
	case !a.Confirmed(f):
		p.Err = "Type " + a.Want(f) + " to confirm."
	case f.Get("shown") != p.Shown:
		// The state changed under the dialog, such as a cluster that went.
		p.Warn, p.Blocked = "The command changed while you looked. Check it, then press the button again.", false
		actRejected(s, w, r, v, p)
		return
	default:
		id, err := actStart(st, a, f, p.Cmd)
		if err == nil {
			w.Header().Set("HX-Redirect", "/tasks/"+id)
			return
		}
		s.Env().Log.Error("starting a task", "action", a.ID, "err", err)
		p.Err = err.Error()
	}
	p.Blocked = true
	actRejected(s, w, r, v, p)
}

// actStart creates the command's directories, saves its file, if it has
// one, and starts the command as a task. It returns the task's ID.
func actStart(st *actState, a action.Action, f action.Form, cmd action.Command) (string, error) {
	for _, dir := range cmd.Dirs {
		if err := st.mkdirAll(dir); err != nil {
			return "", fmt.Errorf("creating the directory %s: %w", dir, err)
		}
	}
	if cmd.Config != nil {
		if err := st.writeFile(cmd.Config.Path, []byte(cmd.Config.Content)); err != nil {
			return "", fmt.Errorf("saving the config: %w", err)
		}
	}
	realm, cl := a.Target(f)
	return st.starter.Start(run.Spec{Action: a.ID, Argv: cmd.Argv, Display: cmd.Display, Realm: realm, Cluster: cl})
}

// actRejected answers a run that did not start: htmx puts the preview, with
// the reason, into the dialog.
func actRejected(s *Server, w http.ResponseWriter, r *http.Request, v *View, p actPreview) {
	w.Header().Set("HX-Retarget", "#act-preview")
	w.Header().Set("HX-Reswap", "innerHTML")
	s.Write(w, r, http.StatusOK, "act-preview", v.withData(p))
}
