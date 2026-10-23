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
}

// pgDialog returns the URL of the dialog of the action with the query
// pairs kv (key, value, key, value...); a pair with an empty value is left
// out.
func pgDialog(action string, kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u := "/dialogs/" + url.PathEscape(action)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
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
}

// Link returns the address of the node's page.
func (n pgNode) Link() string { return pgNodeURL(n.Realm, n.Cluster, n.Short) }

// StateClass returns the class of the node's state pill.
func (n pgNode) StateClass() string { return pgStateClass(n.State) }

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

// Tile returns the classes of the node's tile in a node map.
func (n pgNode) Tile() string {
	role := string(n.Role)
	if n.Short == cluster.ControllerBackupShortName {
		role = "controller-backup"
	}
	c := "pg-nm pg-nm-" + role + " pg-nm-" + string(n.State)
	if n.State == cluster.StateRunning && len(n.Down()) > 0 {
		c += " pg-nm-svcdown"
	}
	return c
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

// Issues says what is wrong with the node, in short words: a node that is
// paused or in an unknown state is a warning, one that does not run or has a
// service down a failure.
func (n pgNode) Issues() []pgPill {
	var out []pgPill
	if n.State != cluster.StateRunning {
		out = append(out, pgPill{string(n.State), pgStateClass(n.State)})
	}
	for _, s := range n.Down() {
		out = append(out, pgPill{s + " down", "bad"})
	}
	return out
}

// Problem reports whether anything is wrong with the node.
func (n pgNode) Problem() bool { return len(n.Issues()) > 0 }

// ServiceUp counts the services that are up.
func (n pgNode) ServiceUp() int {
	c := 0
	for _, s := range n.Services {
		if s.OK {
			c++
		}
	}
	return c
}

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

// StateClass returns the class of the cluster's state pill.
func (c pgCluster) StateClass() string { return pgClusterClass(c.State) }

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
	{"summary", "Summary"}, {"nodes", "Nodes"}, {"logs", "Logs"},
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
		mon := sn.MonitoringOf(r)
		for _, c := range sn.ClustersOf(r) {
			out = append(out, pgClusterOf(sn, r, c, mon, now))
		}
	}
	return out
}

// pgClusterOf builds one cluster with its nodes and runs.
func pgClusterOf(sn store.Snapshot, realm string, c cluster.Summary, mon *cluster.Monitoring, now time.Time) pgCluster {
	pc := pgCluster{Realm: realm, Name: c.Name, Slurm: c.SlurmVersion, State: c.State}
	st := sn.Status(realm, c.Name)
	for _, ns := range sn.ClusterNodes(realm, c.Name) {
		pc.Nodes = append(pc.Nodes, pgNodeOf(realm, ns, st))
	}
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
// run that has not ended and names the cluster after its command words, as
// in "create worker dev". A run names no realm, so a run of a cluster of
// the same name in another realm counts too, unless sind ui started it and
// knows its realm.
func pgRunsOn(sn store.Snapshot, realm, name string, now time.Time) []pgRun {
	var out []pgRun
	for _, r := range slices.Backward(sn.Runs) {
		if r.Done() {
			continue
		}
		if t, ok := runTaskOf(sn, r.Trace); ok && t.Realm != "" && t.Realm != realm {
			continue
		}
		words := strings.Fields(r.Title)
		if len(words) > 2 && slices.Contains(words[2:], name) {
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
	// Href is the run's page, or the first run's for several.
	Href string
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
		return &pgRunBanner{Command: r.Title, Text: text, Href: r.Link()}
	}
	waiting := 0
	for _, r := range c.Runs {
		if r.Waiting {
			waiting++
		}
	}
	text := "run on " + c.Name + "."
	if waiting > 0 {
		verb := "wait"
		if waiting == 1 {
			verb = "waits"
		}
		text = fmt.Sprintf("run on %s: %d works, %d %s for the lock of the realm.", c.Name, len(c.Runs)-waiting, waiting, verb)
	}
	return &pgRunBanner{Count: fmt.Sprintf("%d commands", len(c.Runs)), Text: text, Href: c.Runs[0].Link()}
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
