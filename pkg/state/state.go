// SPDX-License-Identifier: LGPL-3.0-or-later

// Package state locates sind's per-user state on the host and holds the
// realm lock, which serializes the operations that change a realm.
//
// sind keeps its state in $XDG_STATE_HOME/sind, or ~/.local/state/sind:
// the MCP stream's generated token (mcp-token) and, per realm, the lock
// file and the exported SSH configuration. The realm itself (its mesh, DNS
// records, known_hosts and clusters) lives in the Docker daemon, and so
// does the second part of the realm lock, which serializes the daemon's
// clients (LockRealm).
package state

import (
	"errors"
	"os"
	"path/filepath"
)

// Dir returns sind's state directory, $XDG_STATE_HOME/sind, falling back
// to ~/.local/state/sind. A relative XDG_STATE_HOME is ignored, as the XDG
// Base Directory specification requires; the state would otherwise move
// with the working directory. Adapted from GSI-HPC/clusterctl
// internal/config/paths.go.
func Dir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(home) {
			return "", errors.New("HOME is not an absolute path and XDG_STATE_HOME is not set to one")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "sind"), nil
}

// RealmDir returns the state directory of realm, Dir()/<realm>, which holds
// the realm's lock file and SSH configuration export.
func RealmDir(realm string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, realm), nil
}
