// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/go-clikit/localweb/page"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/state"
)

// actDialogKeys are the query keys that open a dialog with its fields
// filled in.
var actDialogKeys = []string{
	"realm", "cluster", "nodes", "op", "reason", "container",
	// The fields of the New cluster dialog, which a link may fill in to
	// start from a cluster that exists.
	"name", "image", "wait", "backup", "db", "api", "submitter", "workers", "size", "cpus", "memory", "tmp", "users",
	"dtype", "dir", "mkdir", "mount", "monitoring", "pull", "wait_other",
}

// actRowFieldPattern is a field of a node, a worker group or a row of a
// user, group or account, which a link may fill in too, as may the lists
// of the keys of the groups and rows.
var actRowFieldPattern = regexp.MustCompile(`^(wg|ur|gr|ar|(controller|db|api|submitter|[wuga][0-9a-z]{1,8})-[a-zA-Z]{1,16})$`)

// actLinkKeys are the keys of the query that open a dialog with its fields
// filled in: actDialogKeys, and the fields of nodes and rows.
func actLinkKeys(q url.Values) []string {
	keys := slices.Clone(actDialogKeys)
	for k := range q {
		if actRowFieldPattern.MatchString(k) {
			keys = append(keys, k)
		}
	}
	return keys
}

const (
	// actMaxValue is the longest value of a field a form may post, in
	// bytes, but for a field of text, the lines of a Slurm config file or
	// the YAML of a config, which may have actMaxText; actMaxValues is the
	// most values.
	actMaxValue  = 1024
	actMaxText   = 64 << 10
	actMaxValues = 2048
)

// actCheckForm refuses a form with a value that is too long, or too many.
func actCheckForm(f url.Values) error {
	n := 0
	for k, vals := range f {
		n += len(vals)
		limit := actMaxValue
		if strings.HasPrefix(k, "slurm-") || k == "yaml" {
			limit = actMaxText
		}
		for _, v := range vals {
			if len(v) > limit {
				return fmt.Errorf("the value of %s is longer than %d bytes: %w", k, limit, ErrBadRequest)
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
	// Draft has the browser keep what the user typed until the command
	// starts or the user discards it, and the dialog opens with it again;
	// a click beside such a dialog does not close it.
	Draft bool
	// Follow opens the run page once the command starts. Other dialogs
	// close, and the page behind them stays, with a notice of the run.
	Follow bool
	// Removes says that the command removes the cluster or the realm of
	// the link: opened on a page of it, the dialog follows its run, as
	// the page would show only that it is gone.
	Removes bool
	// RealmField shows the realm as a field, filled in with the realm of
	// the link; other dialogs keep it hidden.
	RealmField bool
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
		OK: "Create", Wide: true, Plan: true, Draft: true, Follow: true, RealmField: true, Fields: action.ClusterFieldsOf, Sections: actClusterSections,
		Title: func(*actDialog) string { return "New cluster" },
		Lead: func(*actDialog) string {
			return "sind ui saves the config, then runs sind create cluster with it."
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
			if names := strings.FieldsFunc(d.Form.Get("users"), func(r rune) bool { return r == ',' || r == ' ' }); len(names) > 0 && !d.Form.Has(action.UserRows.List) {
				// The users of a link become rows.
				for i, name := range names {
					key := action.UserRows.Prefix + strconv.Itoa(i)
					d.Form.Add(action.UserRows.List, key)
					d.Form.Set(key+"-name", name)
				}
			}
			d.Form.Del("users")
			d.def("identity", string(config.IdentityLocal))
			d.def("stab", "main")
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
		OK: "Delete", Danger: true, NeedsCluster: true, Removes: true,
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
		Title: func(d *actDialog) string { return "Power and drain: nodes of " + d.Cluster },
		Lead: func(*actDialog) string {
			return "Drain and Resume change what Slurm does with the nodes. The other actions change their containers, up to 8 at a time."
		},
		Defaults: func(d *actDialog) {
			op, ok := action.PowerOpOf(d.Form.Get("op"))
			if !ok {
				op, _ = action.PowerOpOf("shutdown")
			}
			d.Form.Set("op", op.ID)
		},
	},
	"connect": {
		OK: "Connect", RealmField: true, Fields: actFields("container", "realm"),
		Title: func(*actDialog) string { return "Connect a container" },
		Lead: func(*actDialog) string {
			return "Attaches a container that sind did not make, such as a kind node, to the mesh and cluster networks of the realm. It then resolves the nodes by name."
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
		OK: "Delete", Danger: true, Removes: true,
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
	Fields func(f url.Values) []string
	// Sum is the line that sums the section up.
	Sum func(p actPreview) string
}

// actClusterSections are the sections of the New cluster dialog.
var actClusterSections = []actSection{
	{ID: "users", Fields: action.UserFieldsOf, Sum: actUsersSum},
	{ID: "identity", Fields: actFields("identity", "ctlusers"), Sum: func(p actPreview) string {
		mode := cmp.Or(p.Form.Get("identity"), string(config.IdentityLocal))
		if mode == string(config.IdentityClientIDs) && actOn(p.Form.Get("ctlusers")) {
			return mode + ", users on the controllers too"
		}
		return mode
	}},
	{ID: "slurm", Fields: actFields(action.SlurmFields()...), Sum: func(p actPreview) string {
		var files []string
		for _, s := range action.SlurmFiles {
			if p.SlurmSet(s.ID) {
				files = append(files, s.File)
			}
		}
		if len(files) == 0 {
			return "No additions"
		}
		return "Adds to " + strings.Join(files, ", ")
	}},
	{ID: "data", Fields: actFields("dtype", "dir", "mkdir", "mount", "cvmfs"), Sum: func(p actPreview) string {
		sum := strings.TrimSpace(p.Form.Get("dir"))
		if p.Form.Get("dtype") == "volume" {
			sum = "Docker volume"
		} else if len(p.Cmd.Dirs) > 0 {
			sum += ", created"
		}
		sum = actWithMount(sum, p.Form)
		if actOn(p.Form.Get("cvmfs")) {
			sum += ", CVMFS"
		}
		return sum
	}},
	{ID: "run", Fields: actFields("pull", "wait", "wait_other"), Sum: actRunSum},
}

// YAMLMode reports whether the user edits the YAML of the config, which
// then takes the place of the form.
func (p actPreview) YAMLMode() bool { return p.Form.Get("yaml_on") != "" }

// SlurmSet reports whether the form adds lines to the Slurm config file id.
func (p actPreview) SlurmSet(id string) bool { return strings.TrimSpace(p.Form.Get("slurm-"+id)) != "" }

// SlurmFiles are the Slurm config files that a new cluster may add lines
// to.
func (p actPreview) SlurmFiles() []action.SlurmFile { return action.SlurmFiles }

// SlurmFiles are the Slurm config files that a new cluster may add lines
// to.
func (d *actDialog) SlurmFiles() []action.SlurmFile { return action.SlurmFiles }

// actIdentity is an identity mode of a new cluster, with what it does.
type actIdentity struct {
	Mode config.IdentityMode
	Hint string
}

// actIdentities are the identity modes of a new cluster, as
// action.IdentityModes orders them.
var actIdentities = []actIdentity{
	{config.IdentityLocal, "Every node has the users. munge authenticates them."},
	{config.IdentityNSSSlurm, "Managed workers do not have the users; nss_slurm gives them the job's user. munge authenticates them."},
	{config.IdentityClientIDs, "Only the login node has the users. auth/slurm carries them to slurmctld; there is no munge."},
}

// Identities are the identity modes of a new cluster.
func (d *actDialog) Identities() []actIdentity { return actIdentities }

// actFields returns the fields of a section that are always the same.
func actFields(fields ...string) func(url.Values) []string {
	return func(url.Values) []string { return fields }
}

// actUsersSum sums up the users, groups and accounts of a new cluster: how
// many have a name.
func actUsersSum(p actPreview) string {
	var parts []string
	for _, k := range []struct {
		rows action.Rows
		what string
	}{{action.UserRows, "user"}, {action.GroupRows, "group"}, {action.AccountRows, "account"}} {
		if n := len(actNames(p.Form, k.rows)); n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s%s", n, k.what, map[bool]string{true: "", false: "s"}[n == 1]))
		}
	}
	if len(parts) == 0 {
		return "No users"
	}
	return strings.Join(parts, ", ")
}

// actNames are the names of the rows of the form, without empty ones.
func actNames(f url.Values, r action.Rows) []string {
	var names []string
	for _, key := range r.Keys(f) {
		if name := strings.TrimSpace(f.Get(key + "-name")); name != "" && r.Key(key) {
			names = append(names, name)
		}
	}
	return names
}

// GroupNames are the groups of the form, which the fields of a user
// suggest.
func (p actPreview) GroupNames() []string { return actNames(p.Form, action.GroupRows) }

// AccountNames are the accounts of the form, which the fields of a user
// and of a parent suggest.
func (p actPreview) AccountNames() []string { return actNames(p.Form, action.AccountRows) }

// actRowView is a row of a user, group or account of the New cluster
// dialog.
type actRowView struct {
	Key  string
	form url.Values
}

// Val returns the value of the row's field attr.
func (r actRowView) Val(attr string) string { return r.form.Get(r.Key + "-" + attr) }

// actLimit is a limit of the account Row, as the dialog shows it.
type actLimit struct{ Row, Key, Value string }

// Limits are the limits of an account row.
func (r actRowView) Limits() []actLimit {
	values := r.form[r.Key+"-lv"]
	var out []actLimit
	for i, k := range r.form[r.Key+"-lk"] {
		l := actLimit{Row: r.Key, Key: k}
		if i < len(values) {
			l.Value = values[i]
		}
		out = append(out, l)
	}
	return out
}

// Blank is a new limit of an account row.
func (r actRowView) Blank() actLimit { return actLimit{Row: r.Key} }

// Row returns the row key of the dialog.
func (d *actDialog) Row(key string) actRowView { return actRowView{Key: key, form: d.Form} }

// Rows returns the rows of a kind, "user", "group" or "account", whose keys
// the dialog made.
func (d *actDialog) Rows(kind string) []actRowView {
	r := map[string]action.Rows{"user": action.UserRows, "group": action.GroupRows, "account": action.AccountRows}[kind]
	var out []actRowView
	for _, key := range r.Keys(d.Form) {
		if r.Key(key) {
			out = append(out, d.Row(key))
		}
	}
	return out
}

// LimitKeys are the limits that an account may have.
func (d *actDialog) LimitKeys() []string { return config.LimitKeys }

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
		fields := sec.Fields(p.Form)
		for _, pr := range p.Cmd.Problems {
			if slices.Contains(fields, pr.Field) {
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
		n.Group, n.Label = true, action.GroupLabel(f, key)
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
	// OpGroups are the power operations, by group.
	OpGroups []action.PowerGroup
	// Waits are the choices of the wait for a new cluster.
	Waits []actChoice
	// Lock is who holds the realm's lock, if the command will wait for it.
	Lock *state.LockHolder
	// Draft says that the fields are those of the user's draft.
	Draft bool
	// defaulted are the fields that Spec.Defaults filled.
	defaulted []string
}

// actOK is the button of a dialog that runs the command.
type actOK struct {
	Label string
	// Danger paints it red, Off disables it, and Swap marks it as a part of
	// a preview that takes the place of the button of the dialog.
	Danger, Off, Swap bool
}

// OKButton is the button that runs the command, off until a preview says
// that it can run.
func (d *actDialog) OKButton() actOK {
	return actOK{Label: d.Spec.OK, Danger: d.Spec.Danger, Off: true}
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
		d.defaulted = append(d.defaulted, field)
	}
}

// Defaulted reports whether the field has its default, as the link gives
// no value: the dialog selects a default name, so that typing replaces it.
func (d *actDialog) Defaulted(field string) bool { return slices.Contains(d.defaulted, field) }

// Val returns the value of the field.
func (d *actDialog) Val(field string) string { return d.Form.Get(field) }

// YAMLMode reports whether the user edits the YAML of the config, which
// only a draft can open the dialog with.
func (d *actDialog) YAMLMode() bool { return d.Form.Get("yaml_on") != "" }

// Is reports whether the field has the value.
func (d *actDialog) Is(field, value string) bool { return d.Form.Get(field) == value }

// RealmNames are the realms that the realm field suggests: those that sind
// ui sees, and the realm of the dialog.
func (d *actDialog) RealmNames() []string {
	names := []string{d.Realm}
	for _, r := range d.Snap.Realms {
		if r.Name != d.Realm {
			names = append(names, r.Name)
		}
	}
	slices.Sort(names)
	return names
}

// RealmHint says what the realm field of the dialog does.
func (d *actDialog) RealmHint() string {
	if d.ID == "connect" {
		return "The realm whose networks the container joins. It needs a cluster, whose mesh comes with it."
	}
	return "A group of clusters. A new name makes a new realm."
}

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
// query. The script of the dialogs posts the same link with the field
// draft, the user's draft of a dialog that keeps one, which then fills in
// the fields where it fits the link (actDraftOf).
func actDialogRoute(s *Server, w http.ResponseWriter, r *http.Request) {
	a, ok := actLookup(s, w, r)
	if !ok {
		return
	}
	keys := actLinkKeys(r.URL.Query())
	v, err := s.View(r, keys...)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	spec := actSpecs[a.ID]
	d := &actDialog{
		View: v, ID: a.ID, Spec: spec, Form: url.Values{},
		OpGroups: action.PowerGroups(), Waits: actWaits,
	}
	for _, k := range keys {
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
	if draft, ok := actDraftOf(spec, r.PostForm.Get("draft"), d.Form); ok {
		d.Form, d.Draft = draft, true
	}
	if spec.NeedsCluster {
		if err := d.pickCluster(); err != nil {
			s.Fail(w, r, err)
			return
		}
	}
	if spec.Defaults != nil && !d.Draft {
		spec.Defaults(d)
	}
	d.Typed = a.Want(d.Form)
	d.Title, d.Lead = spec.Title(d), spec.Lead(d)
	if !page.IsHTMX(r) {
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

// actDraftKeys are the fields of a posted form that a draft leaves out:
// they belong to one post, or to one look at the command.
var actDraftKeys = []string{"csrf", "shown", "confirm", "draft"}

// actDraftOf returns the fields of the draft that the browser kept of a
// dialog, raw as a form encodes them, when the dialog keeps drafts and the
// link fits the draft: the same realm, and no other field filled in, as a
// link with fields of its own starts from them.
func actDraftOf(spec actSpec, raw string, link url.Values) (url.Values, bool) {
	if !spec.Draft || raw == "" {
		return nil, false
	}
	for k := range link {
		if k != "realm" {
			return nil, false
		}
	}
	f, err := url.ParseQuery(raw)
	if err != nil || actCheckForm(f) != nil || action.Realm(f) != link.Get("realm") {
		return nil, false
	}
	for _, k := range actDraftKeys {
		f.Del(k)
	}
	return f, true
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
	// Hint says what the form still lacks, before the user tries to run
	// it: the dialog shows it in place of the command, not as an error.
	Hint string
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

// OK labels the button that runs the command: as the command says, or as
// the dialog does.
func (p actPreview) OK() string { return cmp.Or(p.Cmd.OK, p.Spec.OK) }

// OKButton is the button that runs the command, as the preview says.
func (p actPreview) OKButton() actOK {
	return actOK{Label: p.OK(), Danger: p.Danger, Off: p.Blocked, Swap: true}
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

// HostMB is the memory of the Docker host in MiB, 0 when unknown.
func (p actPreview) HostMB() int { return int(p.Snap.Docker.MemTotal >> 20) }

// HostHigh is the memory of the host above which the plan warns, in MiB.
func (p actPreview) HostHigh() int { return p.HostMB() * action.HostMemoryWarn / 100 }

// MemoryLabel writes a size in MiB for people.
func (p actPreview) MemoryLabel(mb int) string { return action.MemoryLabel(mb) }

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

// ProblemOf returns what is wrong with the field, or "": a problem of the
// plan, or the error of the command when it starts with the field.
func (p actPreview) ProblemOf(field string) string {
	for _, pr := range p.Cmd.Problems {
		if pr.Field == field {
			return pr.Text
		}
	}
	if text, ok := strings.CutPrefix(p.Err, field+": "); ok {
		return text
	}
	return ""
}

// ErrAtField reports whether the error of the command belongs to a field
// that shows its own problem, so that the field shows it in place of the
// preview.
func (p actPreview) ErrAtField() bool {
	return slices.ContainsFunc(p.Fields(), func(field string) bool { return strings.HasPrefix(p.Err, field+": ") })
}

// Problems is how many things keep the command from running.
func (p actPreview) Problems() int {
	if p.Err != "" {
		return 1
	}
	return len(p.Cmd.Problems)
}

// First is the problem that the status of the dialog leads to: the first
// problem of the command, or its error, which has no field.
func (p actPreview) First() action.Problem {
	if p.Err == "" && len(p.Cmd.Problems) > 0 {
		return p.Cmd.Problems[0]
	}
	return action.Problem{Text: p.Err}
}

// StatusText is the text of the status button of a command with
// problems: how many, and the field of the first.
func (p actPreview) StatusText() string {
	n, label := p.Problems(), p.First().Label
	text := fmt.Sprintf("%d problem%s", n, actPlural(n))
	switch {
	case label == "":
	case n == 1:
		text += ": " + label
	default:
		text += ", first: " + label
	}
	return text
}

// StatusGo says where the status button goes: to the field of the first
// problem, or to the list of problems in the plan.
func (p actPreview) StatusGo() string {
	if p.First().Field != "" {
		return "Go to the field"
	}
	return "Show the problems"
}

// Say is what the dialog tells assistive technology about its status: the
// first problem and how many there are, or that the command can run.
func (p actPreview) Say() string {
	if !p.Blocked {
		if n := p.Warnings(); n > 0 {
			return fmt.Sprintf("Ready, with %d warning%s. %s runs the command.", n, actPlural(n), p.Spec.OK)
		}
		return "Ready. " + p.Spec.OK + " runs the command."
	}
	first, n := p.First(), p.Problems()
	text := first.Text
	if first.Label != "" {
		text = first.Label + ": " + text
	}
	if n > 1 {
		text += fmt.Sprintf(" %d problems in all.", n)
	}
	return text
}

// SayKey changes when the status changes in a way to say: the dialog does
// not repeat the status for each key that changes the text of a problem.
func (p actPreview) SayKey() string {
	if !p.Blocked {
		return fmt.Sprintf("ready %d", p.Warnings())
	}
	return fmt.Sprintf("%d %s", p.Problems(), p.First().Field)
}

// Totals sums up what the nodes of the plan use: containers, CPUs and
// memory, against that of the Docker host when known.
func (p actPreview) Totals() string {
	if p.Cmd.Containers == 0 {
		return ""
	}
	mem := action.MemoryLabel(p.Cmd.MemoryMB)
	if host := p.HostMB(); host > 0 {
		mem += " of " + action.MemoryLabel(host)
	}
	return fmt.Sprintf("%d container%s · %d CPU%s · %s", p.Cmd.Containers, actPlural(p.Cmd.Containers), p.Cmd.CPUs, actPlural(p.Cmd.CPUs), mem)
}

// actPlural is the s of a count other than one.
func actPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Warnings is how many warnings the command has, which the pill counts.
func (p actPreview) Warnings() int { return len(p.Cmd.Warns) }

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
	var missing *action.Missing
	if errors.As(err, &missing) {
		p.Hint, p.Blocked = missing.Hint(), true
		return p
	}
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
func actContext(r *http.Request, v *View, st *actState) action.Context {
	c := action.Context{Snap: v.Snap, Home: st.home, ConfigDir: st.configDir, Stat: st.stat}
	if st.images != nil {
		c.Image = func(image string) action.ImageFacts { return st.images.facts(r.Context(), image) }
	}
	return c
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
	s.Write(w, r, http.StatusOK, "act-preview", v.withData(actEvaluate(v, a, r.PostForm, actContext(r, v, st))))
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
	p := actEvaluate(v, a, f, actContext(r, v, st))
	switch {
	case p.Err != "":
	case p.Hint != "":
		// A run of a form that lacks a value is an error.
		p.Err, p.Hint = p.Hint, ""
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
			realm, cl := a.Target(f)
			actStarted(s, w, r, v, p, id, p.Spec.Removes && actOnPageOf(r, realm, cl))
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

// actStartedNote is what the notice of a command that started says.
type actStartedNote struct{ ID, Display string }

// actOnPageOf reports whether htmx sent r from a page of the cluster, or
// without a cluster of the realm: the cluster page or that of one of its
// nodes.
func actOnPageOf(r *http.Request, realm, cluster string) bool {
	u, err := url.Parse(r.Header.Get("HX-Current-URL"))
	if err != nil {
		return false
	}
	of := realm
	if cluster != "" {
		of += "/" + cluster
	}
	return u.Path == "/clusters/"+of || strings.HasPrefix(u.Path, "/clusters/"+of+"/") || strings.HasPrefix(u.Path, "/nodes/"+of+"/")
}

// actStarted answers a run that started. A dialog that follows its run,
// one whose page the command removes (gone), or a form posted without
// script, goes to the run page; any other dialog closes on the event
// act-started, and htmx adds a notice with a link to the run to the page.
// The event carries the run page and whether the browser goes there, so
// that the script of the dialogs drops a draft.
func actStarted(s *Server, w http.ResponseWriter, r *http.Request, v *View, p actPreview, id string, gone bool) {
	to := "/tasks/" + id
	follow := p.Spec.Follow || gone || !page.IsHTMX(r)
	event, _ := json.Marshal(map[string]any{"act-started": map[string]any{"url": to, "follow": follow}})
	if follow {
		w.Header().Set("HX-Trigger", string(event))
		page.Redirect(w, r, to)
		return
	}
	w.Header().Set("HX-Trigger-After-Settle", string(event))
	page.Retarget(w, "#act-notes", "beforeend")
	s.Write(w, r, http.StatusOK, "act-started", v.withData(actStartedNote{ID: id, Display: p.Cmd.Display}))
}

// actRejected answers a run that did not start: htmx puts the preview, with
// the reason, into the dialog.
func actRejected(s *Server, w http.ResponseWriter, r *http.Request, v *View, p actPreview) {
	page.Retarget(w, "#act-preview", "innerHTML")
	s.Write(w, r, http.StatusOK, "act-preview", v.withData(p))
}
