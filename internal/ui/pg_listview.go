// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/internal/natsort"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
)

// The list pages, Clusters and Nodes, filter and sort their rows by their
// query, so that a link or a reload shows the same rows: q is a text
// filter, state a chip, and sort a column, with a leading "-" for the
// reverse order. A row that q does not match is still rendered, hidden,
// so that the page script can show it again while the user types; the
// state chip leaves its rows out.

// pgListKeys are the query keys of a list page.
var pgListKeys = []string{"realm", "q", "state", "sort"}

// pgListQuery is what a list page shows: its realm, text filter, chip and
// sort column.
type pgListQuery struct {
	// Base is the address of the page.
	Base  string
	Realm string
	Q     string
	State string
	// Sort is the column the rows are sorted by, Desc whether in reverse.
	Sort string
	Desc bool
	// def is the column that sorts the page when the query names none.
	def string
}

// pgChipDef and pgColDef are a chip and a column of a list.
type pgChipDef struct{ Key, Label string }

type pgColDef struct {
	Key, Label, Class string
	// AllRealms shows the column only when the list shows every realm.
	AllRealms bool
}

// pgListQueryOf reads the query of a list page from v: a chip must be one
// of chips and a sort column one of cols, else it is an ErrBadRequest.
func pgListQueryOf(v *View, base, def string, chips []pgChipDef, cols []pgColDef) (pgListQuery, error) {
	realm, err := pgRealmParam(v)
	if err != nil {
		return pgListQuery{}, err
	}
	q := pgListQuery{Base: base, Realm: realm, Q: strings.TrimSpace(v.Params.Get("q")), State: v.Params.Get("state"), def: def}
	if !slices.ContainsFunc(chips, func(c pgChipDef) bool { return c.Key == q.State }) {
		return q, fmt.Errorf("state: unknown filter %q: %w", q.State, ErrBadRequest)
	}
	s := v.Params.Get("sort")
	q.Desc = strings.HasPrefix(s, "-")
	q.Sort = cmp.Or(strings.TrimPrefix(s, "-"), def)
	if !slices.ContainsFunc(cols, func(c pgColDef) bool { return c.Key == q.Sort && c.Key != "" }) {
		return q, fmt.Errorf("sort: unknown column %q: %w", s, ErrBadRequest)
	}
	return q, nil
}

// SortParam is the value of the query key sort, empty for the default.
func (q pgListQuery) SortParam() string {
	switch {
	case q.Desc:
		return "-" + q.Sort
	case q.Sort == q.def:
		return ""
	}
	return q.Sort
}

// URL returns the address of the list with the query of q, but for the
// pairs kv (key, value, key, value...). The realm is always in it, also
// empty for all realms, as a missing one means the realm of the cookie.
func (q pgListQuery) URL(kv ...string) string {
	v := url.Values{"realm": {q.Realm}}
	set := func(k, val string) {
		if val == "" && k != "realm" {
			v.Del(k)
			return
		}
		v.Set(k, val)
	}
	set("q", q.Q)
	set("state", q.State)
	set("sort", q.SortParam())
	for i := 0; i+1 < len(kv); i += 2 {
		set(kv[i], kv[i+1])
	}
	return q.Base + "?" + v.Encode()
}

// Words are the words of the text filter, in lower case: a row matches
// when its text holds each of them.
func (q pgListQuery) words() []string { return strings.Fields(strings.ToLower(q.Q)) }

// pgMatch reports whether text holds every word.
func pgMatch(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// pgChip is a state filter of a list, with the count of its rows.
type pgChip struct {
	Label string
	N     int
	On    bool
	URL   string
	// Class colours the chip, "bad" for problems that are there.
	Class string
}

// pgChips returns the chips of a list, with count telling the rows of
// each.
func pgChips(q pgListQuery, defs []pgChipDef, count func(key string) int) []pgChip {
	out := make([]pgChip, 0, len(defs))
	for _, d := range defs {
		c := pgChip{Label: d.Label, N: count(d.Key), On: d.Key == q.State, URL: q.URL("state", d.Key)}
		if d.Key == "problem" && c.N > 0 {
			c.Class = "bad"
		}
		out = append(out, c)
	}
	return out
}

// pgCol is a column head of a list: a link that sorts by it, unless the
// column does not sort.
type pgCol struct {
	Label string
	Class string
	// URL sorts by the column, the reverse order when it sorts already.
	URL string
	// Sort is the column's aria-sort, empty when it does not sort the
	// rows.
	Sort  string
	Arrow string
}

// pgCols returns the column heads of a list.
func pgCols(q pgListQuery, defs []pgColDef) []pgCol {
	var out []pgCol
	for _, d := range defs {
		if d.AllRealms && q.Realm != "" {
			continue
		}
		c := pgCol{Label: d.Label, Class: d.Class}
		if d.Key != "" {
			next := d.Key
			if d.Key == q.Sort {
				c.Sort, c.Arrow = "ascending", "▲"
				if q.Desc {
					c.Sort, c.Arrow = "descending", "▼"
				} else {
					next = "-" + d.Key
				}
			}
			if next == q.def {
				next = ""
			}
			c.URL = q.URL("sort", next)
		}
		out = append(out, c)
	}
	return out
}

// pgOrder sorts rows by cmpFn, in reverse when desc; rows that compare
// equal keep their order.
func pgOrder[T any](rows []T, desc bool, cmpFn func(a, b T) int) {
	slices.SortStableFunc(rows, func(a, b T) int {
		if desc {
			return cmpFn(b, a)
		}
		return cmpFn(a, b)
	})
}

// The clusters list.

var pgClusterChips = []pgChipDef{{"", "All"}, {"problem", "Needs a look"}, {"running", "Running"}, {"monitoring", "With monitoring"}}

var pgClusterCols = []pgColDef{
	{Key: "name", Label: "Name"}, {Key: "realm", Label: "Realm", AllRealms: true}, {Key: "state", Label: "State"},
	{Key: "slurm", Label: "Slurm"}, {Key: "nodes", Label: "Nodes", Class: "num"}, {Label: "Roles"}, {Key: "monitoring", Label: "Monitoring"},
}

// NeedsLook reports whether the cluster does not run as it should: it is
// not running, or a node of it needs a look by the rule of every page
// (pgNode.Problem).
func (c pgCluster) NeedsLook() bool {
	return c.State != cluster.StateRunning || slices.ContainsFunc(c.Nodes, pgNode.Problem)
}

// pgClusterIn reports whether the cluster is one of the chip's.
func pgClusterIn(c pgCluster, chip string) bool {
	switch chip {
	case "problem":
		return c.NeedsLook()
	case "running":
		return c.State == cluster.StateRunning
	case "monitoring":
		return c.Monitoring
	}
	return true
}

// pgClusterRank orders clusters from the worst state to the best.
func pgClusterRank(c pgCluster) int {
	switch c.State {
	case cluster.StateStopped:
		return 0
	case cluster.StateMixed:
		return 2
	case cluster.StatePaused:
		return 3
	case cluster.StateRunning:
		if len(c.Problems()) > 0 {
			return 4
		}
		return 5
	}
	return 1
}

// pgClusterCmp compares clusters by a column, then by name and realm.
func pgClusterCmp(col string) func(a, b pgCluster) int {
	byName := func(a, b pgCluster) int {
		return cmp.Or(natsort.Compare(a.Name, b.Name), natsort.Compare(a.Realm, b.Realm))
	}
	key := map[string]func(a, b pgCluster) int{
		"realm": func(a, b pgCluster) int { return natsort.Compare(a.Realm, b.Realm) },
		"state": func(a, b pgCluster) int { return cmp.Compare(pgClusterRank(a), pgClusterRank(b)) },
		"slurm": func(a, b pgCluster) int { return natsort.Compare(a.Slurm, b.Slurm) },
		"nodes": func(a, b pgCluster) int { return cmp.Compare(len(a.Nodes), len(b.Nodes)) },
		// Monitoring on comes first.
		"monitoring": func(a, b pgCluster) int { return cmp.Compare(pgBit(b.Monitoring), pgBit(a.Monitoring)) },
	}[col]
	return func(a, b pgCluster) int {
		if key != nil {
			if c := key(a, b); c != 0 {
				return c
			}
		}
		return byName(a, b)
	}
}

func pgBit(b bool) int {
	if b {
		return 1
	}
	return 0
}

// pgClusterRow is a row of the clusters list.
type pgClusterRow struct {
	pgCluster
	// Text is what the text filter looks in, in lower case.
	Text string
	// Hidden says that the text filter does not match the row.
	Hidden bool
	// Label names the cluster in its row's buttons and in the selection:
	// its name, and its realm when the list shows every realm.
	Label string
}

// Key identifies the cluster in a selection: realm/name.
func (r pgClusterRow) Key() string { return r.Realm + "/" + r.Name }

// pgClusterText is what the text filter of a cluster looks in.
func pgClusterText(c pgCluster) string {
	words := []string{c.Name, c.Realm, string(c.State), c.Slurm}
	for _, n := range c.Nodes {
		words = append(words, n.Arg)
	}
	return strings.ToLower(strings.Join(words, " "))
}

// The nodes list.

var pgNodeChips = []pgChipDef{{"", "All"}, {"problem", "Needs a look"}, {"running", "Running"}, {"stopped", "Not running"}}

var pgNodeCols = []pgColDef{
	{Key: "node", Label: "Node"}, {Key: "cluster", Label: "Cluster"}, {Key: "realm", Label: "Realm", AllRealms: true},
	{Key: "role", Label: "Role"}, {Key: "state", Label: "State"}, {Key: "ip", Label: "IP"}, {Label: "Issues"},
}

// pgNodeIn reports whether the node is one of the chip's.
func pgNodeIn(n pgNode, chip string) bool {
	switch chip {
	case "problem":
		return n.Problem()
	case "running":
		return n.Running()
	case "stopped":
		return !n.Running()
	}
	return true
}

// pgNodeRank orders nodes from the worst state to the best.
func pgNodeRank(n pgNode) int {
	switch n.State {
	case cluster.StateStopped:
		return 0
	case cluster.StatePaused:
		return 2
	case cluster.StateRunning:
		if len(n.Down()) > 0 {
			return 3
		}
		return 4
	}
	return 1
}

// pgRoleOrder is the order of the roles, as sind lists the nodes.
var pgRoleOrder = []config.Role{config.RoleController, config.RoleDB, config.RoleAPI, config.RoleSubmitter, config.RoleWorker}

// pgIP returns the address of a node for sorting; a node without one
// sorts last.
func pgIP(n pgNode) netip.Addr {
	a, err := netip.ParseAddr(n.IP)
	if err != nil {
		return netip.IPv6Unspecified()
	}
	return a
}

// pgNodeCmp compares nodes by a column; nodes that are equal by it keep
// the order of the list, by realm, cluster, role and name.
func pgNodeCmp(col string) func(a, b pgNode) int {
	byCluster := func(a, b pgNode) int {
		return cmp.Or(natsort.Compare(a.Realm, b.Realm), natsort.Compare(a.Cluster, b.Cluster))
	}
	return map[string]func(a, b pgNode) int{
		"node":    func(a, b pgNode) int { return cmp.Or(natsort.Compare(a.Arg, b.Arg), natsort.Compare(a.Realm, b.Realm)) },
		"cluster": byCluster,
		"realm":   func(a, b pgNode) int { return natsort.Compare(a.Realm, b.Realm) },
		"role": func(a, b pgNode) int {
			return cmp.Or(cmp.Compare(slices.Index(pgRoleOrder, a.Role), slices.Index(pgRoleOrder, b.Role)), natsort.Compare(a.Arg, b.Arg))
		},
		"state": func(a, b pgNode) int { return cmp.Compare(pgNodeRank(a), pgNodeRank(b)) },
		"ip":    func(a, b pgNode) int { return pgIP(a).Compare(pgIP(b)) },
	}[col]
}

// pgNodeRow is a row of the nodes list.
type pgNodeRow struct {
	pgNode
	Text   string
	Hidden bool
}

// pgNodeText is what the text filter of a node looks in.
func pgNodeText(n pgNode) string {
	words := []string{n.Arg, n.Cluster, n.Realm, string(n.Role), string(n.State), n.IP}
	for _, i := range n.Issues() {
		words = append(words, i.Text)
	}
	return strings.ToLower(strings.Join(words, " "))
}
