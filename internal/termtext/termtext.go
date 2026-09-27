// SPDX-License-Identifier: LGPL-3.0-or-later

// Package termtext makes untrusted text safe to write to a terminal.
//
// Adapted from GSI-HPC/clusterctl internal/output/escape.go; it is meant to
// be replaced by the shared go-clikit/termtext package once that exists.
package termtext

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EscapeText makes untrusted text safe to write to a terminal, keeping its
// lines.
//
// An error message can quote what a container, docker or Slurm said, and
// that is not trusted: a carriage return or a cursor movement can overwrite
// what was printed before, and an escape sequence can retitle the terminal
// or write the clipboard. EscapeText replaces every C0 and C1 control
// character, DEL, the Unicode controls that reorder or break lines, and
// bytes that are not UTF-8 with a visible escape such as \x1b or \u009b.
// Newline and tab are kept, since they cannot move the cursor back over
// text already printed.
//
// Text that holds none of these is returned unchanged.
func EscapeText(s string) string {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if needsEscape(r, size) {
			break
		}
		i += size
	}
	if i == len(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 16)
	b.WriteString(s[:i])
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case !needsEscape(r, size):
			b.WriteString(s[i : i+size])
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	return b.String()
}

// needsEscape reports whether a rune may not reach a terminal as it is.
func needsEscape(r rune, size int) bool {
	switch {
	case r == utf8.RuneError && size == 1:
		// A byte that is not UTF-8, such as a lone 0x9b, which some
		// terminals read as the start of a control sequence.
		return true
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f,
		r >= 0x202a && r <= 0x202e,
		r >= 0x2066 && r <= 0x2069:
		// Bidirectional controls, which make a terminal show text in an
		// order other than the one it has.
		return true
	case r == 0x2028, r == 0x2029:
		// The Unicode line and paragraph separators.
		return true
	default:
		return false
	}
}
