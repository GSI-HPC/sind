// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"

	"github.com/spf13/afero"

	"github.com/GSI-HPC/sind/internal/ssh"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/state"
)

// ExportSSHConfig brings the realm's SSH config export in its state
// directory (state.RealmDir) up to date in fs: ssh_config, id_ed25519 and
// known_hosts, read from the realm's SSH relay in one docker exec, each
// replaced through a temporary file and a rename. When the relay container
// does not exist, the realm has no cluster: it removes the files, and the
// directory if that leaves it empty. The sind CLI runs it after every
// command that changes the realm's clusters; GetSSHConfig returns the
// path of the ssh_config.
func ExportSSHConfig(ctx context.Context, client *docker.Client, fs afero.Fs, realm string) error {
	dir, err := state.RealmDir(realm)
	if err != nil {
		return err
	}
	err = ssh.ExportConfig(ctx, client, fs, dir, realm)
	if !docker.IsNotFound(err) {
		return err
	}
	return ssh.RemoveExport(fs, dir)
}
