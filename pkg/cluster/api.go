// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import "fmt"

// slurmrestdCheck succeeds when the node has slurmrestd and the systemd
// unit sind enables it with.
const slurmrestdCheck = `command -v slurmrestd >/dev/null
systemctl cat slurmrestd.service >/dev/null`

// slurmrestdStep returns the node setup step (see nodeSetupSteps) that
// checks a managed api node for slurmrestd and its unit, and fails naming
// the image: the sind-node images of Slurm 25.11 have none, and neither may
// a custom or a cached older image. Without it, enabling the unit would fail
// only once every node is up.
func slurmrestdStep(nc RunConfig) setupStep {
	return setupStep{
		name:   "slurmrestd",
		what:   fmt.Sprintf("image %s has no slurmrestd with its systemd unit, which the api node runs; sind-node images ship it from Slurm 26.05 on (--pull refreshes a cached one), and a custom image needs it installed but not enabled", nc.Image),
		script: slurmrestdCheck,
	}
}
