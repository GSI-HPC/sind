// SPDX-License-Identifier: LGPL-3.0-or-later

package termtext_test

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/termtext"
	"github.com/stretchr/testify/assert"
)

// hostile is what a compromised container could write into an error: a
// carriage return and cursor-up to overwrite the line printed before, and
// OSC 52 to write the clipboard.
const hostile = "ok\r\x1b[1Aworker-0: \x1b]52;c;ZXZpbA==\x07evil"

func TestEscapeText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"plain text", "plain text"},
		{"two\nlines\n", "two\nlines\n"},
		{"tab\tseparated", "tab\tseparated"},
		{hostile, `ok\r\x1b[1Aworker-0: \x1b]52;c;ZXZpbA==\x07evil`},
		{"csi \x1b[2J clear", `csi \x1b[2J clear`},
		{"nul\x00del\x7f", `nul\x00del\x7f`},
		{"c1 \u009b31m", `c1 \u009b31m`},
		{"bidi \u202eevil", `bidi \u202eevil`},
		{"arabic letter mark \u061c, lrm \u200e, rli \u2067", `arabic letter mark \u061c, lrm \u200e, rli \u2067`},
		{"line sep \u2028 para sep \u2029", `line sep \u2028 para sep \u2029`},
		{"bad utf-8 \xff\xfe", `bad utf-8 \xff\xfe`},
		{"lone c1 byte \x9b", `lone c1 byte \x9b`},
		{"grüße ✓", "grüße ✓"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, termtext.EscapeText(tc.in))
		})
	}
}

func TestEscapeText_UnchangedIsSameString(t *testing.T) {
	s := "exit status 1: Error response from daemon: No such container: sind-dev-controller"
	assert.Equal(t, s, termtext.EscapeText(s))
}
