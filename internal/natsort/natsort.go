// SPDX-License-Identifier: LGPL-3.0-or-later

// Package natsort orders names naturally, with runs of digits compared by
// their value: "worker-2" before "worker-10", "dev2" before "dev10".
package natsort

import (
	"cmp"
	"strings"
)

// Key returns a comparison key where runs of ASCII digits are
// zero-padded to a fixed width so that lexical comparison yields natural
// order (e.g. "worker-2" < "worker-10").
func Key(s string) string {
	const padWidth = 20
	var b strings.Builder
	b.Grow(len(s) + padWidth)
	i := 0
	for i < len(s) {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			run := s[i:j]
			for k := len(run); k < padWidth; k++ {
				b.WriteByte('0')
			}
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
