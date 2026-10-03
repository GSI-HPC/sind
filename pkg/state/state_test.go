// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDir_XDGSet(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")
	dir, err := Dir()
	require.NoError(t, err)
	assert.Equal(t, "/custom/state/sind", dir)
}

func TestDir_XDGFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/user")
	dir, err := Dir()
	require.NoError(t, err)
	assert.Equal(t, "/home/user/.local/state/sind", dir)
}

func TestDir_RelativeXDGIgnored(t *testing.T) {
	for _, xdg := range []string{"state", "./state", "../state"} {
		t.Run(xdg, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", xdg)
			t.Setenv("HOME", "/home/user")
			dir, err := Dir()
			require.NoError(t, err)
			assert.Equal(t, "/home/user/.local/state/sind", dir)
		})
	}
}

func TestDir_NoHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	_, err := Dir()
	require.Error(t, err)
}

func TestDir_RelativeHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "state")
	t.Setenv("HOME", "home")
	_, err := Dir()
	require.EqualError(t, err, "HOME is not an absolute path and XDG_STATE_HOME is not set to one")
}

func TestRealmDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")
	dir, err := RealmDir("ci-42")
	require.NoError(t, err)
	assert.Equal(t, "/custom/state/sind/ci-42", dir)

	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	_, err = RealmDir("ci-42")
	require.Error(t, err)
}
