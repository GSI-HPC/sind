// SPDX-License-Identifier: LGPL-3.0-or-later

// Package natsort orders names naturally, with runs of digits compared by
// their value: "worker-2" before "worker-10", "dev2" before "dev10".
package natsort

import (
	"cmp"
	"strconv"
	"strings"
)

// Key returns a comparison key in which lexical comparison yields natural
// order (e.g. "worker-2" < "worker-10"): each run of ASCII digits becomes
// the number of digits of its length, then its length, then the run
// without leading zeros (10 -> 1210, 9 -> 119). A longer number then sorts
// after a shorter one, whatever their lengths, and numbers of one length
// by their digits. Runs that differ only in leading zeros have the same
// key.
func Key(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	i := 0
	for i < len(s) {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			run := strings.TrimLeft(s[i:j], "0")
			n := strconv.Itoa(len(run))
			b.WriteString(strconv.Itoa(len(n)))
			b.WriteString(n)
			b.WriteString(run)
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Compare compares a and b in natural order, as strings.Compare does in
// byte order. Names that differ only in leading zeros, such as "dev01" and
// "dev1", are ordered by their bytes, so that only equal names compare
// equal.
func Compare(a, b string) int {
	return cmp.Or(strings.Compare(Key(a), Key(b)), strings.Compare(a, b))
}
