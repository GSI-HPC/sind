// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strconv"
	"strings"
)

// The templates of the main pages register no template function: the
// templates of every part are parsed together, so a function that one part
// adds would break every server that is built without that part. They call
// methods of their data instead: PgRealmOptions, PgRealmNext and
// PgRealmScope on the view, and the methods of the pg view models.

// PgRealmOptions returns the entries of the realm picker in the top bar.
func (v *View) PgRealmOptions() []PgRealmOption { return pgRealmOptions(v) }

// PgRealmNext returns the address that the realm picker goes to.
func (v *View) PgRealmNext() string { return pgRealmNext(v) }

// PgRealmScope returns the realm that the page shows, and the address of
// the page with every realm.
func (v *View) PgRealmScope() PgRealmScope { return pgRealmScope(v) }

// pgPlural returns "1 cluster" or "2 clusters".
func pgPlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// pgJoin joins words as a sentence does: "a", "a and b", "a, b and c".
func pgJoin(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
