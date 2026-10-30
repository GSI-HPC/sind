// SPDX-License-Identifier: LGPL-3.0-or-later

package envvar

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLookup(t *testing.T) {
	t.Setenv(Realm, "ci")
	value, ok := Lookup(Realm)
	assert.True(t, ok)
	assert.Equal(t, "ci", value)

	// An empty variable is one that is not set.
	t.Setenv(Realm, "")
	value, ok = Lookup(Realm)
	assert.False(t, ok)
	assert.Empty(t, value)
}

// TestAll checks that All holds every variable once, each a SIND_ name,
// and every variable that stands in for the command line.
func TestAll(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range All {
		assert.True(t, strings.HasPrefix(name, "SIND_"), name)
		assert.False(t, seen[name], "%s is listed twice", name)
		seen[name] = true
	}
	for _, name := range CommandLine {
		assert.True(t, seen[name], "%s is missing from All", name)
	}
	assert.Len(t, All, len(CommandLine)+2)
}
