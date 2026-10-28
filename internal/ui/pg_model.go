// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// The view models of the main pages. They are built from a snapshot only,
// so every page renders from an empty one.

// pgAction is a button or a link of a page. A dialog link opens the dialog
// of the actions part in the layer; any other link goes to a page.
type pgAction struct {
	Label  string
	URL    string
	Class  string
	Dialog bool
	// Name is the accessible name, where Label alone does not say what the
	// button acts on; empty for Label.
	Name string
	// Verb is the label without the dots, set on an action of a
	// selection of nodes and on a row's button; Fit, set on the former,
	// lists the node states and roles that it acts on, separated by
	// spaces.
	Verb, Fit string
}

// pgQueryURL returns path with the query pairs kv (key, value, key,
// value...); a pair with an empty value is left out.
func pgQueryURL(path string, kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}

// pgDialog returns the URL of the dialog of the action with the query
// pairs kv, as pgQueryURL.
func pgDialog(action string, kv ...string) string {
	return pgQueryURL("/dialogs/"+url.PathEscape(action), kv...)
}

// pgDialogAction returns a dialog link.
func pgDialogAction(label, class, action string, kv ...string) pgAction {
	return pgAction{Label: label, Class: class, URL: pgDialog(action, kv...), Dialog: true}
}

// pgClusterURL returns the address of a cluster page, with the tab when it
// is not the first.
func pgClusterURL(realm, name, tab string) string {
	u := "/clusters/" + url.PathEscape(realm) + "/" + url.PathEscape(name)
	if tab != "" && tab != "summary" {
		u += "?tab=" + url.QueryEscape(tab)
	}
	return u
}

// pgNodeURL returns the address of a node page.
func pgNodeURL(realm, name, short string) string {
	return "/nodes/" + url.PathEscape(realm) + "/" + url.PathEscape(name) + "/" + url.PathEscape(short)
}

// pgShort returns the short name of a node from its container name: the
// name without the prefix of its realm and cluster.
func pgShort(realm, name, container string) string {
	return strings.TrimPrefix(container, cluster.ContainerPrefix(realm, name))
}

// pgArg returns a node's name as sind's commands take it: the short name,
// and the cluster unless it is the default one.
func pgArg(name, short string) string {
	if name == config.DefaultClusterName {
		return short
	}
	return short + "." + name
}

// pgClusterArg returns the optional CLUSTER argument of a command: empty for
// the default cluster, else a space and the name.
func pgClusterArg(name string) string {
	if name == config.DefaultClusterName {
		return ""
	}
	return " " + name
}

// pgSind returns "sind", or "sind --realm R" in any realm but the default.
func pgSind(realm string) string {
	if realm == "" || realm == mesh.DefaultRealm {
		return "sind"
	}
	return "sind --realm " + realm
}

// pgStateClass returns the class of the pill of a node's state.
func pgStateClass(s cluster.State) string {
	switch s {
	case cluster.StateRunning:
		return "ok"
	case cluster.StateStopped:
		return "bad"
	case cluster.StatePaused, cluster.StateUnknown:
		return "warn"
	}
	return ""
}

// pgClusterClass returns the class of the pill of a cluster's state.
func pgClusterClass(s cluster.State) string {
	switch s {
	case cluster.StateRunning:
		return "ok"
	case cluster.StatePaused, cluster.StateMixed, cluster.StateUnknown:
		return "warn"
	}
	return ""
}

// pgSvc is the health of one service of a node.
type pgSvc struct {
	Name string
	OK   bool
}

// pgServiceOrder is the order of the columns of the health matrix.
var pgServiceOrder = []string{"munge", "sshd", "slurmctld", "slurmd", "mariadb", "slurmdbd", "slurmrestd", "sackd"}

// pgNode is a node as the pages show it.
type pgNode struct {
	Realm     string
	Cluster   string
	Short     string
	Arg       string
	Container string
	FQDN      string
	IP        string
	Role      config.Role
	State     cluster.State
	// Services are the services of the node when the status of its
	// cluster is in the store, else nil.
	Services []pgSvc
	Managed  bool
	HA       *cluster.HAStatus
	// Known is set when the status of the node's cluster was in the store.
	Known bool
	// Busy is set while a command works on the node's cluster, such as
	// one that adds workers or reboots a node (pgRunsOn), Creating while
	// one creates the cluster.
	Busy, Creating bool
	// Slurm is what sinfo says about the node, nil when it does not list
	// it or was not asked.
	Slurm *slurm.NodeInfo
}

// Link returns the address of the node's page.
func (n pgNode) Link() string { return pgNodeURL(n.Realm, n.Cluster, n.Short) }

// StateClass returns the class of the node's state pill.
func (n pgNode) StateClass() string { return pgStateClass(n.State) }

// HeadPill is the state pill of the node's page: Pill, but for a running
// node that needs a look, which says why in the class of its problem, as
// "running · slurmd down" or "running · drained", so that green means that
// nothing needs the user.
func (n pgNode) HeadPill() pgPill {
	if n.State != cluster.StateRunning || !n.Problem() {
		return pgPill{string(n.State), n.StateClass()}
	}
	var words []string
	for _, i := range n.Issues() {
		words = append(words, i.Text)
	}
	return pgPill{string(n.State) + " · " + strings.Join(words, ", "), n.Sev()}
}

// RoleClass returns the class of the node's role chip.
func (n pgNode) RoleClass() string {
	if n.Role == config.RoleController {
		return "brand"
	}
	return ""
}

// Running reports whether the node's container runs.
func (n pgNode) Running() bool { return n.State == cluster.StateRunning }

// Worker reports whether the node is a worker, the only role to delete.
func (n pgNode) Worker() bool { return n.Role == config.RoleWorker }

// RowPower returns the power button of the node's row in the Nodes tab:
// Start for a node that does not run, Unfreeze for a paused one, and none
// for a running one, whose verbs the selection offers.
func (n pgNode) RowPower() *pgAction {
	verb, op := "Start", "start"
	switch n.State {
	case cluster.StateRunning:
		return nil
	case cluster.StatePaused:
		verb, op = "Unfreeze", "unpause"
	}
	a := n.power(verb+"…", "", op)
	a.Verb = verb
	return &a
}

// power returns the power dialog of op for the node.
func (n pgNode) power(label, class, op string) pgAction {
	return pgDialogAction(label, class, "power", "realm", n.Realm, "cluster", n.Cluster, "nodes", n.Short, "op", op)
}

// SSH returns the command that opens a shell on the node.
func (n pgNode) SSH() string { return pgSind(n.Realm) + " ssh " + n.Arg }

// Match lists the words that the actions of a selection of nodes fit:
// the node's state and its role.
func (n pgNode) Match() string { return string(n.State) + " " + string(n.Role) }

// Tile returns the classes of the node's tile in a node map. One with a
// service down has a red ring, one that Slurm sends no jobs an amber one.
func (n pgNode) Tile() string {
	role := string(n.Role)
	if n.Short == cluster.ControllerBackupShortName {
		role = "controller-backup"
	}
	c := "pg-nm pg-nm-" + role + " pg-nm-" + string(n.State)
	switch {
	case n.State == cluster.StateRunning && len(n.Down()) > 0:
		c += " pg-nm-svcdown"
	case n.slurmWords() != "":
		c += " pg-nm-slurm"
	}
	return c
}

// slurmWords says that Slurm sends the node no jobs (SlurmIssue), as
// "drained in Slurm", for its tile; empty when it does not say so.
func (n pgNode) slurmWords() string {
	if i, ok := n.SlurmIssue(); ok {
		return i.Text + " in Slurm"
	}
	return ""
}

// TileLabel names the node's tile for screen readers: its name, state,
// the services that are down, and what Slurm says of a node that it sends
// no jobs, as "worker-4.dev, running, slurmd down".
func (n pgNode) TileLabel() string {
	label := n.Arg + ", " + string(n.State)
	if down := n.Down(); len(down) > 0 {
		label += ", " + strings.Join(down, " and ") + " down"
	}
	if w := n.slurmWords(); w != "" {
		label += ", " + w
	}
	return label
}

// TileTitle is the tooltip of the node's tile: "worker-4 · worker ·
// running · slurmd down".
func (n pgNode) TileTitle() string {
	title := n.Short + " · " + string(n.Role) + " · " + string(n.State)
	if down := n.Down(); len(down) > 0 {
		title += " · " + strings.Join(down, " and ") + " down"
	}
	if w := n.slurmWords(); w != "" {
		title += " · " + w
	}
	return title
}

// Down returns the services that are down on a running node.
func (n pgNode) Down() []string {
	if n.State != cluster.StateRunning {
		return nil
	}
	var out []string
	for _, s := range n.Services {
		if !s.OK {
			out = append(out, s.Name)
		}
	}
	return out
}

// pgPill is a short text with the class of its pill.
type pgPill struct {
	Text  string
	Class string
}

// ServiceIssues says what is wrong with the node's container and services,
// in short words: a node that is paused or in an unknown state is a
// warning, one that does not run or has a service down a failure.
func (n pgNode) ServiceIssues() []pgPill {
	var out []pgPill
	if n.State != cluster.StateRunning {
		out = append(out, pgPill{string(n.State), pgStateClass(n.State)})
	}
	for _, s := range n.Down() {
		out = append(out, pgPill{s + " down", "bad"})
	}
	return out
}

// SlurmIssue returns the state of a node that runs with its services up
// but that Slurm sends no jobs, as sinfo writes it ("drained", "down*"),
// and whether there is one. A node with an issue of its container or its
// services has none of its own: that issue is why Slurm sends it no jobs.
func (n pgNode) SlurmIssue() (pgPill, bool) {
	if n.Slurm == nil || len(n.ServiceIssues()) > 0 || slurm.NodeStateTakesJobs(n.Slurm.State) {
		return pgPill{}, false
	}
	return pgPill{n.Slurm.State, pgSlurmClass(n.Slurm.State)}, true
}

// SlurmReason is the reason that Slurm gives for the state of a node that
// it sends no jobs (SlurmIssue), as "memory errors"; empty for none.
func (n pgNode) SlurmReason() string {
	if _, ok := n.SlurmIssue(); ok {
		return n.Slurm.Reason
	}
	return ""
}

// Issues says what is wrong with the node, in short words: the issues of
// its container and services, or else the state of a worker that Slurm
// sends no jobs (SlurmIssue).
func (n pgNode) Issues() []pgPill {
	out := n.ServiceIssues()
	if i, ok := n.SlurmIssue(); ok {
		out = append(out, i)
	}
	return out
}

// Problem reports whether the node needs a look: it has an issue (Issues),
// such as a container that does not run, a service that the last probe of
// its cluster found down, or a worker that Slurm sends no jobs, as a
// drained one. It is the one rule of "needs a look" for every page and the
// rail. A service or a Slurm state is none yet while a command works on the
// cluster (Busy): the command may be starting it; nor is anything while a
// command creates the cluster (Creating).
func (n pgNode) Problem() bool {
	return !n.Creating && (n.State != cluster.StateRunning || !n.Busy && len(n.Issues()) > 0)
}

// Sev says how bad the node's problem is (Problem): bad for a container
// that does not run or a service that is down, warn for a paused node, one
// in a state that sind does not know, or one that Slurm sends no jobs; empty
// for a node that needs no look.
func (n pgNode) Sev() string {
	switch {
	case !n.Problem():
		return ""
	case n.State == cluster.StateStopped || len(n.Down()) > 0:
		return "bad"
	}
	return "warn"
}

// pgLook counts the nodes that need a look (pgNode.Problem), with the class
// that every count of them has: bad when one of them is bad (pgNode.Sev),
// else warn.
func pgLook(nodes []pgNode) (n int, class string) {
	class = "warn"
	for _, x := range nodes {
		switch x.Sev() {
		case "bad":
			class = "bad"
			n++
		case "warn":
			n++
		}
	}
	return n, class
}

// pgLookText says how many nodes need a look: "1 node needs a look", "3
// nodes need a look". Every page that counts them says it so, and
// pgLookShort where the word nodes is clear.
func pgLookText(n int) string { return pgPlural(n, "node needs", "nodes need") + " a look" }

// pgLookShort says how many need a look, where it is clear that it counts
// nodes: "1 needs a look", "3 need a look".
func pgLookShort(n int) string { return pgPlural(n, "needs", "need") + " a look" }

// pgLookUnit is the unit after the count of pgLookShort: "needs a look"
// for one, "need a look" for more.
func pgLookUnit(n int) string {
	if n == 1 {
		return "needs a look"
	}
	return "need a look"
}

// LogsLink returns the address of the Logs tab of the node's cluster, with
// the node picked.
func (n pgNode) LogsLink() string {
	return pgLogsURL(n.Realm, n.Cluster, n.Short, "")
}

// pgSnapNodes returns the nodes of the realm, or of every realm when realm
// is empty, with what the status of their cluster and Slurm tell when the
// store has them.
func pgSnapNodes(sn store.Snapshot, realm string) []pgNode {
	var out []pgNode
	for _, r := range pgRealmNames(sn, realm) {
		start := len(out)
		busy := map[string][2]bool{}
		for _, ns := range sn.Nodes[r] {
			n := pgNodeOf(r, ns, sn.Status(r, ns.Cluster))
			b, ok := busy[ns.Cluster]
			if !ok {
				b[0], b[1] = pgBusy(sn, r, ns.Cluster)
				busy[ns.Cluster] = b
			}
			n.Busy, n.Creating = b[0], b[1]
			out = append(out, n)
		}
		for name := range busy {
			pgAttachSlurm(out[start:], name, sn.Slurm(r, name))
		}
	}
	return out
}

// pgCount counts the items of s that f holds for.
func pgCount[T any](s []T, f func(T) bool) int {
	n := 0
	for _, x := range s {
		if f(x) {
			n++
		}
	}
	return n
}

// ServiceUp counts the services that are up.
func (n pgNode) ServiceUp() int { return pgCount(n.Services, func(s pgSvc) bool { return s.OK }) }

// Service returns the health of the named service, ok false when the node
// has no such service.
func (n pgNode) Service(name string) (up, ok bool) {
	for _, s := range n.Services {
		if s.Name == name {
			return s.OK, true
		}
	}
	return false, false
}

// pgNodeOf builds a node from its summary, and adds what the cluster's
// status holds about it when the store has one.
func pgNodeOf(realm string, ns cluster.NodeSummary, st *cluster.Status) pgNode {
	n := pgNode{
		Realm: realm, Cluster: ns.Cluster, Short: pgShort(realm, ns.Cluster, ns.Container),
		Container: ns.Container, FQDN: ns.FQDN, IP: ns.IP, Role: ns.Role, State: ns.State,
	}
	n.Arg = pgArg(n.Cluster, n.Short)
	if st == nil {
		return n
	}
	for _, s := range st.Nodes {
		if s == nil || s.Name != n.Arg && s.Name != n.Short+"."+n.Cluster {
			continue
		}
		n.Known, n.Managed = true, s.Managed
		if s.Health != nil {
			n.HA = s.Health.HA
			if n.IP == "" {
				n.IP = s.Health.IP
			}
			for name, ok := range s.Health.Services {
				n.Services = append(n.Services, pgSvc{string(name), ok})
			}
			slices.SortFunc(n.Services, func(a, b pgSvc) int {
				return slices.Index(pgServiceOrder, a.Name) - slices.Index(pgServiceOrder, b.Name)
			})
		}
		break
	}
	return n
}

// pgCluster is a cluster as the pages show it.
type pgCluster struct {
	Realm      string
	Name       string
	Slurm      string
	State      cluster.State
	Nodes      []pgNode
	Monitoring bool
	// Runs are the runs that work on the cluster now.
	Runs []pgRun
}

// Link returns the address of the cluster's page.
func (c pgCluster) Link() string { return pgClusterURL(c.Realm, c.Name, "") }

// Look counts the nodes of the cluster that need a look (pgNode.Problem).
func (c pgCluster) Look() int { n, _ := pgLook(c.Nodes); return n }

// Pill is the state pill of the cluster: its state, and the count of the
// nodes that need a look when some do, as "running · 2 need a look", in
// the class of that count (pgLook), so that green means that nothing needs
// the user.
func (c pgCluster) Pill() pgPill {
	if n, class := pgLook(c.Nodes); n > 0 {
		return pgPill{string(c.State) + " · " + pgLookShort(n), class}
	}
	return pgPill{string(c.State), pgClusterClass(c.State)}
}

// Roles summarizes the nodes by role: "1 controller · 4 workers".
func (c pgCluster) Roles() string {
	order := []string{"controller", cluster.ControllerBackupShortName, "db", "api", "submitter", "worker"}
	count := map[string]int{}
	for _, n := range c.Nodes {
		r := string(n.Role)
		if n.Short == cluster.ControllerBackupShortName {
			r = cluster.ControllerBackupShortName
		}
		count[r]++
	}
	var parts []string
	for _, r := range order {
		if count[r] == 0 {
			continue
		}
		label := r
		if r == "worker" && count[r] > 1 {
			label = "workers"
		}
		parts = append(parts, fmt.Sprintf("%d %s", count[r], label))
	}
	return strings.Join(parts, " · ")
}

// Problems lists what is wrong in the cluster, one entry per issue, such as
// "worker-1: stopped".
func (c pgCluster) Problems() []pgPill {
	var out []pgPill
	for _, n := range c.Nodes {
		for _, i := range n.Issues() {
			out = append(out, pgPill{n.Short + ": " + i.Text, i.Class})
		}
	}
	return out
}

// Tabs of a cluster page, in order.
var pgTabs = []struct{ Key, Label string }{
	{"summary", "Summary"}, {"nodes", "Nodes"}, {"slurm", "Slurm"}, {"logs", "Logs"},
	{"network", "Network and storage"}, {"connect", "Connect"},
}

// pgValidTab reports whether tab names a tab of the cluster page.
func pgValidTab(tab string) bool {
	return slices.ContainsFunc(pgTabs, func(t struct{ Key, Label string }) bool { return t.Key == tab })
}

// pgClustersOf returns the clusters of the realm, or of every realm when
// realm is empty, with their nodes. status is read for none: only a cluster
// page needs it.
func pgClustersOf(sn store.Snapshot, realm string, now time.Time) []pgCluster {
	var out []pgCluster
	for _, r := range pgRealmNames(sn, realm) {
		mon := sn.Monitorings[r]
		for _, c := range sn.Clusters[r] {
			out = append(out, pgClusterOf(sn, r, c, mon, now))
		}
	}
	return out
}

// pgClusterOf builds one cluster with its nodes and runs.
func pgClusterOf(sn store.Snapshot, realm string, c cluster.Summary, mon *cluster.Monitoring, now time.Time) pgCluster {
	pc := pgCluster{Realm: realm, Name: c.Name, Slurm: c.SlurmVersion, State: c.State}
	st := sn.Status(realm, c.Name)
	busy, creating := pgBusy(sn, realm, c.Name)
	for _, ns := range sn.ClusterNodes(realm, c.Name) {
		n := pgNodeOf(realm, ns, st)
		n.Busy, n.Creating = busy, creating
		pc.Nodes = append(pc.Nodes, n)
	}
	pgAttachSlurm(pc.Nodes, c.Name, sn.Slurm(realm, c.Name))
	if mon != nil {
		pc.Monitoring = slices.ContainsFunc(mon.Clusters, func(m cluster.MonitoredCluster) bool { return m.Name == c.Name })
	}
	pc.Runs = pgRunsOn(sn, realm, c.Name, now)
	return pc
}

// pgRealmNames returns the realm when it is set, else the names of all
// realms of the snapshot, including the ones that hold data only.
func pgRealmNames(sn store.Snapshot, realm string) []string {
	if realm != "" {
		return []string{realm}
	}
	names := map[string]bool{}
	for _, r := range sn.Realms {
		names[r.Name] = true
	}
	for r := range sn.Clusters {
		names[r] = true
	}
	for r := range sn.Nodes {
		names[r] = true
	}
	out := make([]string, 0, len(names))
	for r := range names {
		out = append(out, r)
	}
	slices.SortFunc(out, natsort.Compare)
	return out
}

// pgRun is a run as the lists show it.
type pgRun struct {
	ID      string
	Title   string
	Class   string
	Running bool
	// Source is where the run came from (runSource); Terminal is set for
	// a run from a terminal.
	Source   string
	Terminal bool
	// Waiting is set while the run waits for the lock of its realm.
	Waiting bool
	Time    string
}

// Link returns the address of the run's page.
func (r pgRun) Link() string { return "/runs/" + url.PathEscape(r.ID) }

// pgRunOf builds a run for a list, with where it came from.
func pgRunOf(sn store.Snapshot, r store.Run, now time.Time) pgRun {
	pr := pgRun{ID: r.ID, Title: r.Title, Running: !r.Done(), Source: runSource(sn, r), Waiting: runWaitsForLock(r)}
	pr.Terminal = pr.Source == runFromTerminal
	if t, ok := runTaskOf(sn, r.Trace); ok {
		pr.Title = runTitle(r, &t)
	}
	switch {
	case !r.Done():
		pr.Class = "run"
		pr.Time = pgClock(now.Sub(r.Start))
	case r.Status == store.StatusOK:
		pr.Class, pr.Time = "ok", pgAgo(*r.End, now)
	case r.Status == store.StatusCanceled:
		pr.Class, pr.Time = "warn", pgAgo(*r.End, now)
	default:
		pr.Class, pr.Time = "bad", pgAgo(*r.End, now)
	}
	return pr
}

// pgRunsOn returns the runs that work on the cluster now, oldest first: a
// run that has not ended and names the cluster or nodes of it after its
// command words, as in "create worker dev" or "power reboot worker-1.dev".
// A run names no realm, so a run of a cluster of the same name in another
// realm counts too, unless sind ui started it and knows its realm.
func pgRunsOn(sn store.Snapshot, realm, name string, now time.Time) []pgRun {
	var out []pgRun
	for _, r := range slices.Backward(sn.Runs) {
		if pgWorksOn(sn, r, realm, name) {
			out = append(out, pgRunOf(sn, r, now))
		}
	}
	return out
}

// pgRunBanner is the line of a cluster page about the runs that work on the
// cluster now: one line for all of them.
type pgRunBanner struct {
	// Command is the title of the one run; empty for several.
	Command string
	// Count is "3 commands" for several runs.
	Count string
	Text  string
	// Href is the run's page, or Activity with the runs of the cluster
	// that go on for several; Action labels the link.
	Href   string
	Action string
}

// Banner returns the line about the runs of the cluster, nil for none.
func (c pgCluster) Banner() *pgRunBanner {
	switch len(c.Runs) {
	case 0:
		return nil
	case 1:
		r := c.Runs[0]
		text := "runs " + runSourceText(r.Source) + "."
		if r.Source == runFromTerminal {
			text = "runs in a terminal."
		}
		if r.Waiting {
			text = "waits for the lock of the realm. It runs " + runSourceText(r.Source) + "."
		}
		return &pgRunBanner{Command: r.Title, Text: text, Href: r.Link(), Action: "Show progress"}
	}
	waiting := pgCount(c.Runs, func(r pgRun) bool { return r.Waiting })
	text := "run on " + c.Name + "."
	if waiting > 0 {
		verb := "wait"
		if waiting == 1 {
			verb = "waits"
		}
		text = fmt.Sprintf("run on %s: %d works, %d %s for the lock of the realm.", c.Name, len(c.Runs)-waiting, waiting, verb)
	}
	href := runQuery{Status: "running", Realm: c.Realm, Cluster: c.Name}.href()
	return &pgRunBanner{Count: fmt.Sprintf("%d commands", len(c.Runs)), Text: text, Href: href, Action: "Show them"}
}

// pgWorksOn reports whether the run works on the cluster now (pgRunsOn):
// it has not ended, names the cluster, or nodes of it, as "worker-1.dev",
// and is not a run of sind ui in another realm.
func pgWorksOn(sn store.Snapshot, r store.Run, realm, name string) bool {
	if r.Done() {
		return false
	}
	if t, ok := runTaskOf(sn, r.Trace); ok && t.Realm != "" && t.Realm != realm {
		return false
	}
	words := strings.Fields(r.Title)
	return len(words) > 2 && slices.ContainsFunc(words[2:], func(w string) bool {
		return w == name || strings.HasSuffix(w, "."+name)
	})
}

// pgBusy reports whether a run works on the cluster now (pgRunsOn), and
// whether one creates it ("create cluster NAME").
func pgBusy(sn store.Snapshot, realm, name string) (busy, creating bool) {
	for _, r := range sn.Runs {
		if pgWorksOn(sn, r, realm, name) {
			busy = true
			creating = creating || strings.HasPrefix(r.Title, "create cluster ")
		}
	}
	return busy, creating
}

// pgAgo says how long ago t was, as "just now", "5 min ago", "3 h ago".
func pgAgo(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 50*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()+0.5))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()+0.5))
	}
	return fmt.Sprintf("%d d ago", int(d.Hours()/24+0.5))
}

// pgClock formats a duration as m:ss.s, the clock of a running command.
func pgClock(d time.Duration) string {
	d = max(d, 0)
	m := int(d / time.Minute)
	return fmt.Sprintf("%d:%04.1f", m, (d - time.Duration(m)*time.Minute).Seconds())
}

// ClusterLink returns the address of the page of the node's cluster.
func (n pgNode) ClusterLink() string { return pgClusterURL(n.Realm, n.Cluster, "") }

// NodesText returns "1 node" or "3 nodes".
func (c pgCluster) NodesText() string { return pgPlural(len(c.Nodes), "node", "nodes") }

// DeleteURL returns the address of the dialog that deletes the cluster.
func (c pgCluster) DeleteURL() string {
	return pgDialog("delete-cluster", "realm", c.Realm, "cluster", c.Name)
}
