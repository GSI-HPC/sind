// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/ssh"
	"github.com/spf13/afero"
)

// syncSSHExport updates or cleans the SSH configuration export directory.
// If the mesh SSH container exists, it exports ssh_config, id_ed25519, and
// known_hosts to dir (ssh.ExportConfig). If the SSH container is gone (mesh
// cleaned up after last cluster deletion), which the export's docker exec
// reports, it removes those files, and dir if that leaves it empty
// (ssh.RemoveExport).
func syncSSHExport(ctx context.Context, client *docker.Client, meshMgr *mesh.Manager, fs afero.Fs, dir string) error {
	err := ssh.ExportConfig(ctx, client, fs, dir, meshMgr.Realm)
	if !docker.IsNotFound(err) {
		return err
	}
	return ssh.RemoveExport(fs, dir)
}
