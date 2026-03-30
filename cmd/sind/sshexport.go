// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/ssh"
	"github.com/spf13/afero"
)

// sshExportFiles are the files managed by syncSSHExport.
var sshExportFiles = []string{"ssh_config", "id_ed25519", "known_hosts"}

// syncSSHExport updates or cleans the SSH configuration export directory.
// If the mesh SSH container exists, it exports ssh_config, id_ed25519, and
// known_hosts to dir. If the SSH container is gone (mesh cleaned up after
// last cluster deletion), it removes those files but preserves the directory.
func syncSSHExport(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, fs afero.Fs, dir string) error {
	exists, err := client.ContainerExists(ctx, meshMgr.SSHContainerName())
	if err != nil {
		return err
	}

	if exists {
		return ssh.ExportConfig(ctx, client, fs, dir, meshMgr.SSHContainerName())
	}

	for _, name := range sshExportFiles {
		_ = fs.Remove(filepath.Join(dir, name))
	}
	// Remove the realm directory if empty.
	_ = fs.Remove(dir)
	return nil
}

// sindStateDir returns the per-realm SSH export directory path.
// Uses $XDG_STATE_HOME/sind/<realm>, falling back to ~/.local/state/sind/<realm>.
func sindStateDir(realm string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "sind", realm), nil
}

// sindStateBase returns the base directory for all realm SSH exports.
func sindStateBase() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "sind"), nil
}
