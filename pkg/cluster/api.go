// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import "fmt"

// slurmrestdCheck succeeds when the node has slurmrestd and the systemd
// unit sind enables it with.
const slurmrestdCheck = `command -v slurmrestd >/dev/null
systemctl cat slurmrestd.service >/dev/null`

// slurmrestdDropIn is the systemd drop-in that sind adds to slurmrestd's
// unit on a managed api node.
const slurmrestdDropIn = "/etc/systemd/system/slurmrestd.service.d/sind.conf"

// slurmrestdSecurity writes slurmrestdDropIn and reloads systemd, so that
// enabling the unit picks it up. slurmrestd unshares its System V IPC and
// file descriptor table at startup and exits when it cannot, which a
// container denies: Docker's default seccomp profile allows unshare only
// with CAP_SYS_ADMIN. SLURMRESTD_SECURITY turns both off, which isolates no
// less here: the container has an IPC namespace of its own, and systemd
// starts slurmrestd with a file descriptor table of its own.
const slurmrestdSecurity = `mkdir -p /etc/systemd/system/slurmrestd.service.d
printf '[Service]\nEnvironment=SLURMRESTD_SECURITY=disable_unshare_sysv,disable_unshare_files\n' >` + slurmrestdDropIn + `
systemctl daemon-reload`

// slurmrestdSteps returns the node setup steps (see nodeSetupSteps) of a
// managed api node: the check for slurmrestd and its unit, which fails
// naming the image (the sind-node images of Slurm 25.11 have none, and
// neither may a custom or a cached older image; without the check,
// enabling the unit would fail only once every node is up), and the
// drop-in that lets slurmrestd start in the container.
func slurmrestdSteps(nc RunConfig) []setupStep {
	return []setupStep{
		{
			name:   "slurmrestd",
			what:   fmt.Sprintf("image %s has no slurmrestd with its systemd unit, which the api node runs; sind-node images ship it from Slurm 26.05 on (--pull refreshes a cached one), and a custom image needs it installed but not enabled", nc.Image),
			script: slurmrestdCheck,
		},
		{
			name:   "slurmrestd-security",
			what:   "configuring slurmrestd to start without unsharing namespaces, which the container denies",
			script: slurmrestdSecurity,
		},
	}
}
