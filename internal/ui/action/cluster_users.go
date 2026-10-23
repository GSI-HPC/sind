// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
)

// Rows is a kind of row of the New cluster dialog: users, groups or
// accounts. The keys of the rows are the values of the field List, in
// order; each starts with Prefix. The fields of a row are KEY-ATTR, for
// the Attrs.
type Rows struct {
	List, Prefix string
	Attrs        []string
}

// The rows of the New cluster dialog. An account has its limits as the
// pairs of the fields KEY-lk and KEY-lv too.
var (
	UserRows    = Rows{List: "ur", Prefix: "u", Attrs: []string{"name", "uid", "accounts", "group", "groups", "coordinator", "admin"}}
	GroupRows   = Rows{List: "gr", Prefix: "g", Attrs: []string{"name", "gid"}}
	AccountRows = Rows{List: "ar", Prefix: "a", Attrs: []string{"name", "parent", "lk"}}
)

// maxRows is the most rows of a kind that a form may have.
const maxRows = 64

// rowPattern is what the key of a row looks like after its prefix; the
// script of the dialog makes the keys.
var rowPattern = regexp.MustCompile(`^[0-9a-z]{1,8}$`)

// Key reports whether key is one of the rows: the prefix, then letters or
// digits, and not a node role, whose fields look the same.
func (r Rows) Key(key string) bool {
	rest, ok := strings.CutPrefix(key, r.Prefix)
	return ok && rowPattern.MatchString(rest) && !slices.Contains(NodeRoles, key)
}

// Keys returns the keys of the rows of the form, in order.
func (r Rows) Keys(f Form) []string { return f[r.List] }

// fields are the fields of the rows of the form, in order.
func (r Rows) fields(f Form) []string {
	var fields []string
	for _, key := range r.Keys(f) {
		if r.Key(key) {
			for _, a := range r.Attrs {
				fields = append(fields, key+"-"+a)
			}
		}
	}
	return fields
}

// UserFieldsOf returns the fields of the users, groups and accounts of the
// form, in the order the dialog shows them.
func UserFieldsOf(f Form) []string {
	return slices.Concat(UserRows.fields(f), GroupRows.fields(f), AccountRows.fields(f))
}

// splitList splits a field that takes a list: values separated by commas
// or white space.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

// rowsOf calls read for each row of the form whose key the dialog made,
// once, and at most maxRows times; label names the rows in a problem.
func rowsOf(f Form, r Rows, label string, ps *problems, read func(key string)) {
	var seen []string
	for _, key := range r.Keys(f) {
		switch {
		case !r.Key(key) || slices.Contains(seen, key):
			ps.add("", label, false, fmt.Errorf("the row %q is not one that the dialog made; open the dialog again", key))
			continue
		case len(seen) == maxRows:
			ps.add(key+"-name", label, false, fmt.Errorf("a cluster here has at most %d %s", maxRows, strings.ToLower(label)))
			continue
		}
		seen = append(seen, key)
		read(key)
	}
}

// rowID reads the ID field of a row: a number from config.MinUID, or 0 for
// the next free one.
func rowID(f Form, field, label string, ps *problems) int {
	s := strings.TrimSpace(f.Get(field))
	if s == "" {
		return 0
	}
	id, err := strconv.Atoi(s)
	if err != nil || id < config.MinUID || id > config.MaxUID {
		ps.add(field, label, true, fmt.Errorf("write a number from %d, or nothing for the next free one", config.MinUID))
		return 0
	}
	return id
}

// clusterUsers reads the rows of the users, groups and accounts into the
// config. A row without a name is left out, as is a name that sind
// refuses. The checks that need the whole config, such as a group that a
// user names but no row declares, are those of the config.
func clusterUsers(f Form, cfg *config.Cluster, ps *problems) {
	rowsOf(f, UserRows, "Users", ps, func(key string) {
		get := func(attr string) string { return strings.TrimSpace(f.Get(key + "-" + attr)) }
		name := get("name")
		if name == "" {
			return
		}
		if config.CheckUserName(name) != nil {
			ps.add(key+"-name", "User", false, errAccountName)
			return
		}
		u := config.User{
			Name: name, UID: rowID(f, key+"-uid", "UID of "+name, ps), Group: get("group"), Groups: splitList(get("groups")),
			Accounts: splitList(get("accounts")), Coordinator: splitList(get("coordinator")),
		}
		switch admin := config.AdminLevel(get("admin")); admin {
		case "", config.AdminOperator, config.AdminAdmin:
			u.AdminLevel = admin
		default:
			ps.add(key+"-admin", "Admin level of "+name, true, errors.New("pick none, operator or admin"))
		}
		cfg.Users = append(cfg.Users, u)
	})
	rowsOf(f, GroupRows, "Groups", ps, func(key string) {
		name := strings.TrimSpace(f.Get(key + "-name"))
		if name == "" {
			return
		}
		if config.CheckGroupName(name) != nil {
			ps.add(key+"-name", "Group", false, errAccountName)
			return
		}
		cfg.Groups = append(cfg.Groups, config.Group{Name: name, GID: rowID(f, key+"-gid", "GID of "+name, ps)})
	})
	rowsOf(f, AccountRows, "Accounts", ps, func(key string) {
		name := strings.TrimSpace(f.Get(key + "-name"))
		if name == "" {
			return
		}
		cfg.Accounts = append(cfg.Accounts, config.Account{Name: name, Parent: strings.TrimSpace(f.Get(key + "-parent")), Limits: accountLimits(f, key, name, ps)})
	})
	if !cfg.UsesAccounts() || cfg.HasManagedDB() {
		return
	}
	// The plan leaves the accounts out, so that the rest of the config
	// shows.
	field, err := "db", errors.New("the Slurm accounts need the db node, which runs slurmdbd; tick db, or remove the accounts")
	if !cfg.Managed() {
		field, err = "controller-unmanaged", errors.New("the Slurm accounts need a managed cluster with a db node")
	}
	ps.add(field, "Accounts", true, err)
	cfg.Accounts = nil
	for i := range cfg.Users {
		cfg.Users[i].Accounts, cfg.Users[i].Coordinator, cfg.Users[i].AdminLevel = nil, nil, ""
	}
}

// accountLimits reads the limits of the account key: the pairs of the
// fields KEY-lk and KEY-lv. A pair without a key and value is left out, as
// is a key that sacctmgr does not take and a QOS that a new cluster lacks.
func accountLimits(f Form, key, name string, ps *problems) config.Limits {
	keys, values := f[key+"-lk"], f[key+"-lv"]
	var limits config.Limits
	for i, k := range keys {
		k = strings.TrimSpace(k)
		v := ""
		if i < len(values) {
			v = strings.TrimSpace(values[i])
		}
		if k == "" && v == "" {
			continue
		}
		i := slices.IndexFunc(config.LimitKeys, func(l string) bool { return strings.EqualFold(l, k) })
		if i < 0 {
			ps.add(key+"-lk", "Limits of "+name, false, fmt.Errorf("%q is no limit that sacctmgr takes; pick one of the list, such as MaxJobs", k))
			continue
		}
		if err := qosError(config.LimitKeys[i], v); err != nil {
			ps.add(key+"-lk", "Limits of "+name, false, err)
			continue
		}
		if limits == nil {
			limits = config.Limits{}
		}
		limits[config.LimitKeys[i]] = v
	}
	return limits
}
