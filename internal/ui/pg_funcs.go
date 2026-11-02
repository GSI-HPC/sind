// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"strconv"

	"github.com/GSI-HPC/sind/internal/ui/action"
)

// The templates of the main pages register no template function: the
// templates of every part are parsed together, so a function that one part
// adds would break every server that is built without that part. They call
// methods of their data instead: PgRealmScope on the view (pg_realm.go),
// and the methods of the pg view models.

// pgPlural returns "1 cluster" or "2 clusters".
func pgPlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// pgJoin joins words as a sentence does: "a", "a and b", "a, b and c".
func pgJoin(words []string) string { return action.JoinWords(words) }
