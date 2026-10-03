// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// RootAccount is the account at the top of every cluster's account tree.
// slurmctld creates it, with an association for root, when it registers
// the cluster with slurmdbd.
const RootAccount = "root"

// Account is a Slurm account that sind creates with sacctmgr once the
// cluster is registered with slurmdbd.
type Account struct {
	Name string `json:"name"`
	// Parent is the parent account: RootAccount, the default, or an
	// account declared before this one.
	Parent string `json:"parent,omitempty"`
	// Limits are further sacctmgr options of the account, passed as
	// key=value, e.g. GrpTRES: cpu=4 or MaxJobs: 1.
	Limits Limits `json:"limits,omitempty"`
}

// UnmarshalJSON supports two YAML forms:
//   - bare string: "physics"
//   - full object: "name: theory\n  parent: physics"
func (a *Account) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		a.Name = name
		return nil
	}

	type accountAlias Account // prevents infinite recursion
	var alias accountAlias
	if err := decodeStrict(data, &alias); err != nil {
		return fmt.Errorf("account must be a name or an object: %w", err)
	}
	*a = Account(alias)
	return nil
}

// Limits are sacctmgr options of an account, by option name. Numbers are
// passed in plain decimal form, as YAML 1.1 reads them before sind sees
// them: MaxJobs: 1 becomes MaxJobs=1, but 010 is octal and becomes 8, and
// 1.10 becomes 1.1. A quoted value is passed as written.
type Limits map[string]string

// UnmarshalJSON accepts a map of strings and numbers.
func (l *Limits) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("limits must be a map of option names to values: %w", err)
	}
	limits := make(Limits, len(raw))
	for key, value := range raw {
		var s string
		if err := json.Unmarshal(value, &s); err == nil {
			limits[key] = s
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(value))
		dec.UseNumber()
		var n json.Number
		if err := dec.Decode(&n); err != nil {
			return fmt.Errorf("limit %s must be a string or a number, got %s", key, value)
		}
		limits[key] = n.String()
	}
	*l = limits
	return nil
}

// Keys returns the option names in sorted order, the order sind passes
// them to sacctmgr.
func (l Limits) Keys() []string {
	return slices.Sorted(maps.Keys(l))
}

// AdminLevel is a Slurm user's administrative level.
type AdminLevel string

// Admin levels a user can have besides none, the default.
const (
	AdminOperator AdminLevel = "operator"
	AdminAdmin    AdminLevel = "admin"
)

// accountNamePattern restricts account names to lowercase letters, digits,
// underscores and hyphens, at most 64 characters, so that they reach
// sacctmgr unchanged: Slurm stores account names in lowercase.
var accountNamePattern = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]{0,63}$`)

// LimitKeys are the sacctmgr add account options an account's limits may
// set, matched case-insensitively: the account's description,
// organization and flags, and the limits and settings of its association.
// sacctmgr also takes any prefix of an option name, so a deny list could
// not keep limits off the options sind sets itself (name, parent and
// cluster): A, Acct, N, C or Pa would reach them. Where would make
// sacctmgr add account loop forever.
var LimitKeys = []string{
	"Comment", "DefaultQOS", "Description", "Fairshare", "Flags",
	"GrpJobs", "GrpJobsAccrue", "GrpSubmitJobs", "GrpTRES", "GrpTRESMins", "GrpTRESRunMins", "GrpWall",
	"MaxJobs", "MaxJobsAccrue", "MaxSubmitJobs", "MaxTRES", "MaxTRESMins", "MaxTRESMinsPerJob", "MaxTRESPerJob",
	"MaxTRESPerNode", "MaxTRESRunMins", "MaxWall", "MaxWallDurationPerJob",
	"MinPrioThresh", "Organization", "Priority", "QOS", "QosLevel", "Shares",
}

// isLimitKey reports whether key is one of LimitKeys, in any case.
func isLimitKey(key string) bool {
	return slices.ContainsFunc(LimitKeys, func(k string) bool { return strings.EqualFold(k, key) })
}

// enforcingValues are the AccountingStorageEnforce values that make
// slurmctld enforce association limits, in lowercase.
var enforcingValues = []string{"2", "limits", "safe", "all"}

// Warnings returns what is likely a mistake in a valid config, for sind
// create cluster to print: account limits (Max* and Grp*) that Slurm does
// not enforce, as slurm.main's AccountingStorageEnforce does not include
// limits. sind keeps Slurm's default of no enforcement, which changes which
// jobs run.
func (c *Cluster) Warnings() []string {
	var limited []string
	for _, a := range c.Accounts {
		var keys []string
		for _, key := range a.Limits.Keys() {
			if k := strings.ToLower(key); strings.HasPrefix(k, "max") || strings.HasPrefix(k, "grp") {
				keys = append(keys, key)
			}
		}
		if len(keys) > 0 {
			limited = append(limited, a.Name+": "+strings.Join(keys, ", "))
		}
	}
	if len(limited) == 0 {
		return nil
	}
	enforce, set := c.Slurm.Main.Parameter("AccountingStorageEnforce")
	for v := range strings.SplitSeq(enforce, ",") {
		if slices.Contains(enforcingValues, strings.ToLower(strings.TrimSpace(v))) {
			return nil
		}
	}
	fix := "slurm.main does not set AccountingStorageEnforce; add AccountingStorageEnforce=associations,limits to it"
	if set {
		fix = "slurm.main sets AccountingStorageEnforce=" + enforce + "; add limits to it"
	}
	return []string{fmt.Sprintf("Slurm does not enforce the accounts' limits (%s): %s", strings.Join(limited, "; "), fix)}
}

// UsesAccounts reports whether the config asks for Slurm accounts:
// accounts, or a user with accounts, coordinator or adminLevel.
func (c *Cluster) UsesAccounts() bool {
	if len(c.Accounts) > 0 {
		return true
	}
	for _, u := range c.Users {
		if len(u.Accounts) > 0 || len(u.Coordinator) > 0 || u.AdminLevel != "" {
			return true
		}
	}
	return false
}

// validateAccounts checks the accounts and the users' Slurm settings: names,
// parents declared before their children, limits, and that every account a
// user names is declared. Slurm accounts need slurmdbd, so a managed db
// node.
func (c *Cluster) validateAccounts() error {
	if !c.UsesAccounts() {
		return nil
	}
	if !c.HasManagedDB() {
		return fmt.Errorf("accounts, and the users' accounts, coordinator and adminLevel, require a managed db node: sind creates Slurm accounts with sacctmgr, which needs slurmdbd")
	}

	declared := make(map[string]bool, len(c.Accounts))
	for _, a := range c.Accounts {
		if !accountNamePattern.MatchString(a.Name) {
			return fmt.Errorf("invalid account name %q: it must contain only lowercase letters, digits, underscores and hyphens, not start with a hyphen, and have at most 64 characters", a.Name)
		}
		if a.Name == RootAccount {
			return fmt.Errorf("account %q exists in every cluster: declare accounts below it", RootAccount)
		}
		if declared[a.Name] {
			return fmt.Errorf("duplicate account %q", a.Name)
		}
		if a.Parent != "" && a.Parent != RootAccount && !declared[a.Parent] {
			if slices.ContainsFunc(c.Accounts, func(other Account) bool { return other.Name == a.Parent }) {
				return fmt.Errorf("account %q: parent %q must be declared before it", a.Name, a.Parent)
			}
			return fmt.Errorf("account %q: parent %q is not declared in accounts", a.Name, a.Parent)
		}
		declared[a.Name] = true
		for _, key := range a.Limits.Keys() {
			if !isLimitKey(key) {
				return fmt.Errorf("account %q: invalid limit %q: it must be one of the sacctmgr options %s", a.Name, key, strings.Join(LimitKeys, ", "))
			}
			if a.Limits[key] == "" {
				return fmt.Errorf("account %q: limit %s must not be empty", a.Name, key)
			}
		}
	}

	for _, u := range c.Users {
		for _, f := range []struct {
			name     string
			accounts []string
		}{{"accounts", u.Accounts}, {"coordinator", u.Coordinator}} {
			listed := make(map[string]bool, len(f.accounts))
			for _, a := range f.accounts {
				if !declared[a] {
					return fmt.Errorf("user %q: %s: account %q is not declared in accounts", u.Name, f.name, a)
				}
				if listed[a] {
					return fmt.Errorf("user %q: %s: account %q is listed twice", u.Name, f.name, a)
				}
				listed[a] = true
			}
		}
		switch u.AdminLevel {
		case "", AdminOperator, AdminAdmin:
		default:
			return fmt.Errorf("user %q: adminLevel must be %q or %q, got %q", u.Name, AdminOperator, AdminAdmin, u.AdminLevel)
		}
		if len(u.Accounts) == 0 && (len(u.Coordinator) > 0 || u.AdminLevel != "") {
			return fmt.Errorf("user %q: coordinator and adminLevel need accounts: sacctmgr creates a Slurm user only with an account", u.Name)
		}
	}
	return nil
}
