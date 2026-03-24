// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPower_SubcommandsExist(t *testing.T) {
	subcmds := []string{"shutdown", "cut", "on", "reboot", "cycle", "freeze", "unfreeze"}

	for _, sub := range subcmds {
		t.Run(sub, func(t *testing.T) {
			cmd := NewRootCommand()
			c, _, err := cmd.Find([]string{"power", sub})
			require.NoError(t, err)
			assert.Contains(t, c.Use, "NODES")
		})
	}
}

func TestPower_RequiresArgs(t *testing.T) {
	subcmds := []string{"shutdown", "cut", "on", "reboot", "cycle", "freeze", "unfreeze"}

	for _, sub := range subcmds {
		t.Run(sub, func(t *testing.T) {
			_, _, err := executeCommand("power", sub)
			assert.Error(t, err)
		})
	}
}
