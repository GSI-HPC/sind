// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build linux

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// On a pseudo-terminal, standard error is a terminal that is no pipe. The
// size and the foreground the display reads from it are the kit's.
func TestAPseudoTerminalIsATerminal(t *testing.T) {
	_, tty := openPTYPair(t)

	assert.True(t, onTerminal(tty))
	assert.False(t, intoPipe(tty))
}
