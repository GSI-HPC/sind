// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import "strconv"

// The templates of the main pages register no template function: the
// templates of every part are parsed together, so a function that one part
// adds would break every server that is built without that part. They call
// methods of their data instead: PgRealmOptions and PgRealmNext on the view,
// and the methods of the pg view models.

// PgRealmOptions returns the entries of the realm picker in the top bar.
func (v *View) PgRealmOptions() []PgRealmOption { return pgRealmOptions(v) }

// PgRealmNext returns the address that the realm picker returns to.
func (v *View) PgRealmNext() string { return pgRealmNext(v) }

// pgPlural returns "1 cluster" or "2 clusters".
func pgPlural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
