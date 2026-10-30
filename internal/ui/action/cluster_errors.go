// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
)

// The texts of the New cluster page say what to change, in the words of
// the form: sind's own errors name the keys of the config and quote Go
// strings, which suit a config file, not a form.

// maxName is the longest name of a cluster or realm.
const maxName = 63

// nameError says what is wrong with name, the name of a cluster or a realm
// (kind), and how to fix it, with a name that would do where there is one;
// nil where sind takes the name.
func nameError(kind, name string) error {
	if config.CheckName(kind, name) == nil {
		return nil
	}
	try := ""
	if s := slug(name); s != name && s != "" && config.CheckName(kind, s) == nil {
		try = ", such as " + s
	}
	bad := strings.IndexFunc(name, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-'
	})
	switch {
	case name == "":
		return fmt.Errorf("give the %s a name, such as %s", kind, map[string]string{"cluster": "hpc", "realm": "ci"}[kind])
	case len(name) > maxName:
		return fmt.Errorf("use at most %d characters; this name has %d", maxName, len(name))
	case bad >= 0:
		what := "no spaces"
		if r := []rune(name[bad:])[0]; r != ' ' {
			what = "no " + string(r)
		}
		return fmt.Errorf("use only lowercase letters, digits and -, %s%s", what, try)
	case strings.ToLower(name) != name:
		return fmt.Errorf("use lowercase letters%s", try)
	case strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-"):
		return fmt.Errorf("start and end with a letter or a digit%s", try)
	case name == config.ReservedClusterName:
		return fmt.Errorf("%s is the name of the SSH relay of the realm; pick another name", name)
	}
	return fmt.Errorf("a name may not end in -%s, as the SSH relay of a realm uses it; pick another name", config.ReservedClusterName)
}

// memoryPattern is a memory size as people write it, in lowercase and
// without spaces: a number, and a unit with an optional b or ib, such as
// 2g, 2gb or 2gib, or b for bytes.
var memoryPattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)(?:([kmgtp])(?:i?b)?|b)?$`)

// memorySize reads a memory size as people write it, such as 2 GB or
// 512 MiB, and returns it in Docker's short form, 2g or 512m. Docker's
// units are powers of 1024, so GB reads as GiB.
func memorySize(s string) (string, error) {
	m := memoryPattern.FindStringSubmatch(strings.ToLower(strings.Join(strings.Fields(s), "")))
	if m == nil {
		return "", errors.New("write a size such as 2g or 512m")
	}
	v := m[1] + m[2]
	if _, err := config.MemoryMB(v); err != nil {
		return "", fmt.Errorf("write a size such as 2g or 512m; %w", err)
	}
	return v, nil
}

// tmpSize reads the size of the /tmp of a node, which the kernel takes as
// it is.
func tmpSize(s string) (string, error) {
	if config.CheckTmpSize("", s) != nil {
		return "", errors.New("write a whole number with a unit, such as 256m or 1g, or a share of the memory, such as 10%")
	}
	return s, nil
}

// slug makes a name that sind takes of s: lowercase, with a hyphen for each
// run of other characters, and none at the ends.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if 'a' <= r && r <= 'z' || '0' <= r && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	out := b.String()
	if len(out) > maxName {
		out = strings.TrimRight(out[:maxName], "-")
	}
	return out
}

// configRule turns an error of sind's check of the whole config into a
// problem at the field of the form that causes it.
type configRule struct {
	re *regexp.Regexp
	// problem returns the problem of the match m in the form f.
	problem func(f Form, m []string) Problem
}

// rowField returns the field attr of the row of r whose name is name: the
// nth such row, from 0, as a duplicate is the second one; "" for none.
func rowField(f Form, r Rows, name string, nth int, attr string) string {
	for _, key := range r.Keys(f) {
		if r.Key(key) && strings.TrimSpace(f.Get(key+"-name")) == name {
			if nth == 0 {
				return key + "-" + attr
			}
			nth--
		}
	}
	return ""
}

// userFieldLabels are the labels of the fields of a user row, by the key
// in sind's errors.
var userFieldLabels = map[string]struct{ attr, label string }{
	"accounts":    {"accounts", "Accounts of "},
	"coordinator": {"coordinator", "Coordinator of "},
}

// configRules are the errors of config.ValidateAll that a form can cause,
// in the words of the form.
var configRules = []configRule{
	{regexp.MustCompile(`^user "([^"]+)": (accounts|coordinator): account "([^"]+)" is not declared in accounts$`), func(f Form, m []string) Problem {
		l := userFieldLabels[m[2]]
		return Problem{Field: rowField(f, UserRows, m[1], 0, l.attr), Label: l.label + m[1],
			Text: fmt.Sprintf("add the account %s under Slurm accounts first, or remove it from %s", m[3], m[1])}
	}},
	{regexp.MustCompile(`^user "([^"]+)": (accounts|coordinator): account "([^"]+)" is listed twice$`), func(f Form, m []string) Problem {
		l := userFieldLabels[m[2]]
		return Problem{Field: rowField(f, UserRows, m[1], 0, l.attr), Label: l.label + m[1], Text: m[3] + " is there twice; remove one"}
	}},
	{regexp.MustCompile(`^user "([^"]+)": coordinator and adminLevel need accounts: `), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, UserRows, m[1], 0, "accounts"), Label: "Accounts of " + m[1],
			Text: fmt.Sprintf("give %s an account first: a coordinator and an admin level need one", m[1])}
	}},
	{regexp.MustCompile(`^user "([^"]+)": group "([^"]+)" is not declared in groups$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, UserRows, m[1], 0, "group"), Label: "Primary group of " + m[1],
			Text: fmt.Sprintf("add the group %s under Groups first, or leave the primary group empty", m[2])}
	}},
	{regexp.MustCompile(`^user "([^"]+)": supplementary group "([^"]+)" is not declared in groups$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, UserRows, m[1], 0, "groups"), Label: "Groups of " + m[1],
			Text: fmt.Sprintf("add the group %s under Groups first, or remove it from %s", m[2], m[1])}
	}},
	{regexp.MustCompile(`^user "([^"]+)": "([^"]+)" is its primary group, not a supplementary one$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, UserRows, m[1], 0, "groups"), Label: "Groups of " + m[1],
			Text: fmt.Sprintf("%s is the primary group of %s already; remove it from Groups", m[2], m[1])}
	}},
	{regexp.MustCompile(`^user "([^"]+)": supplementary group "([^"]+)" is listed twice$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, UserRows, m[1], 0, "groups"), Label: "Groups of " + m[1], Text: m[2] + " is there twice; remove one"}
	}},
	{regexp.MustCompile(`^duplicate (user|group|account) "([^"]+)"$`), func(f Form, m []string) Problem {
		r := map[string]Rows{"user": UserRows, "group": GroupRows, "account": AccountRows}[m[1]]
		return Problem{Field: rowField(f, r, m[2], 1, "name"), Label: strings.ToUpper(m[1][:1]) + m[1][1:],
			Text: fmt.Sprintf("another %s is named %s; rename one of them", m[1], m[2])}
	}},
	{regexp.MustCompile(`^users "([^"]+)" and "([^"]+)" have the same uid (\d+)$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, UserRows, m[2], 0, "uid"), Label: "UID of " + m[2],
			Text: fmt.Sprintf("%s has the UID %s already; pick another, or leave it empty for the next free one", m[1], m[3])}
	}},
	{regexp.MustCompile(`^group "([^"]+)" clashes with the private group of user "([^"]+)"`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, GroupRows, m[1], 0, "name"), Label: "Group",
			Text: fmt.Sprintf("the user %s has a private group of that name; rename the group, or make it the primary group of %s", m[2], m[2])}
	}},
	{regexp.MustCompile(`^account "([^"]+)": parent "([^"]+)" is not declared in accounts$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, AccountRows, m[1], 0, "parent"), Label: "Parent of " + m[1],
			Text: fmt.Sprintf("add the account %s first, or leave the parent empty for root", m[2])}
	}},
	{regexp.MustCompile(`^account "([^"]+)": parent "([^"]+)" must be declared before it$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, AccountRows, m[1], 0, "parent"), Label: "Parent of " + m[1],
			Text: fmt.Sprintf("sind creates a parent first, so %s must come above %s; remove %s and add it again", m[2], m[1], m[1])}
	}},
	{regexp.MustCompile(`^invalid account name "([^"]*)": `), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, AccountRows, m[1], 0, "name"), Label: "Account",
			Text: "use lowercase letters, digits, _ and -, not - first, at most 64 characters"}
	}},
	{regexp.MustCompile(`^account "(root)" exists in every cluster`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, AccountRows, m[1], 0, "name"), Label: "Account",
			Text: "root is there in every cluster; pick another name, and an empty parent puts the account below root"}
	}},
	{regexp.MustCompile(`^account "([^"]+)": limit (\S+) must not be empty$`), func(f Form, m []string) Problem {
		return Problem{Field: rowField(f, AccountRows, m[1], 0, "lk"), Label: "Limits of " + m[1],
			Text: fmt.Sprintf("give %s a value, or remove it", m[2])}
	}},
}

// configProblem returns the problem of an error of sind's check of the
// config of the form f: at the field that causes it, in the words of the
// form, where a rule knows the error.
func configProblem(f Form, err error) Problem {
	for _, r := range configRules {
		if m := r.re.FindStringSubmatch(err.Error()); m != nil {
			return r.problem(f, m)
		}
	}
	return Problem{Label: "Config", Text: err.Error()}
}

// errAccountName is the problem of the name of a user or a group: how it
// is made.
var errAccountName = errors.New("start with a lowercase letter or _, then use lowercase letters, digits, _ and -, at most 32 characters")
