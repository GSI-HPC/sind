// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"bytes"
	"context"
	"io"
)

// terminalKey is the context key of WithTerminal.
type terminalKey struct{}

// WithTerminal returns ctx for a command that writes more to a terminal
// than to a pipe, such as docker pull, which writes the bytes each layer
// has downloaded only to a terminal: OSExecutor gives its standard output a
// pseudo-terminal of TerminalWidth columns, and turns what the command
// writes there into plain lines (NewTerminalLines) before it captures them
// or hands them to a line parser. Where it cannot open one, the command
// writes to a pipe, as without WithTerminal. Standard error stays a pipe.
func WithTerminal(ctx context.Context) context.Context {
	return context.WithValue(ctx, terminalKey{}, true)
}

// Terminal reports whether WithTerminal put a terminal in ctx.
func Terminal(ctx context.Context) bool {
	on, _ := ctx.Value(terminalKey{}).(bool)
	return on
}

// terminalOpenedKey is the context key of WithTerminalOpened.
type terminalOpenedKey struct{}

// WithTerminalOpened returns ctx for a command run WithTerminal whose caller
// needs to know that it got the terminal, as a parser of what docker pull
// writes does, which reads the bytes only where docker writes them:
// OSExecutor calls opened once it has opened the terminal, before the
// command starts, and not at all where it writes to a pipe instead.
func WithTerminalOpened(ctx context.Context, opened func()) context.Context {
	return context.WithValue(ctx, terminalOpenedKey{}, opened)
}

// TerminalOpened returns the function WithTerminalOpened put in ctx, or
// nil.
func TerminalOpened(ctx context.Context) func() {
	opened, _ := ctx.Value(terminalOpenedKey{}).(func())
	return opened
}

// TerminalWidth is the width of the pseudo-terminal of WithTerminal, in
// columns: wide enough for docker pull to write its byte counts and the
// time left, too narrow for it to draw a bar as well.
const TerminalWidth = 100

// TerminalLines writes what a command wrote to a terminal as plain lines,
// each ended by "\n", without the escape sequences that move the cursor or
// erase. A carriage return, a line feed and a move of the cursor up or down
// end a line, as a program that redraws lines in place, such as the
// progress of docker pull, writes one line before each of them; a line of
// blanks only is left out, and the blanks around a line are cut. Other
// control characters are left out. It is not safe for concurrent use.
type TerminalLines struct {
	w    io.Writer
	line []byte
	// esc is set after an ESC, csi in a control sequence, "ESC [".
	esc, csi bool
}

// NewTerminalLines returns a TerminalLines that writes its lines to w.
func NewTerminalLines(w io.Writer) *TerminalLines {
	return &TerminalLines{w: w}
}

// Write cuts p into lines, and writes those that ended to w at once. It
// returns len(p) unless w fails.
func (t *TerminalLines) Write(p []byte) (int, error) {
	var out []byte
	for _, b := range p {
		switch {
		case t.csi:
			// Parameters and intermediates go on to the final byte.
			if b >= 0x40 && b <= 0x7e {
				t.csi = false
				if b == 'A' || b == 'B' {
					out = t.end(out)
				}
			}
		case t.esc:
			// An escape sequence such as "ESC ( B" ends after its
			// intermediates, one that is "ESC [" goes on.
			t.esc = b >= 0x20 && b <= 0x2f
			t.csi = b == '['
		case b == 0x1b:
			t.esc = true
		case b == '\r' || b == '\n':
			out = t.end(out)
		case b < 0x20 && b != '\t', b == 0x7f:
		default:
			t.line = append(t.line, b)
		}
	}
	if len(out) == 0 {
		return len(p), nil
	}
	if _, err := t.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush writes the line that has not ended, if any.
func (t *TerminalLines) Flush() error {
	out := t.end(nil)
	if len(out) == 0 {
		return nil
	}
	_, err := t.w.Write(out)
	return err
}

// end appends the line so far to out, unless it is blank, and starts the
// next one.
func (t *TerminalLines) end(out []byte) []byte {
	line := bytes.TrimSpace(t.line)
	t.line = t.line[:0]
	if len(line) == 0 {
		return out
	}
	out = append(out, line...)
	return append(out, '\n')
}
