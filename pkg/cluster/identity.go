// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
)

// nssSlurmCheck succeeds when glibc finds the nss_slurm module, which
// official images install as /usr/lib64/libnss_slurm.so.2.
const nssSlurmCheck = `ldconfig -p | grep -q 'libnss_slurm\.so\.2' || test -e /usr/lib64/libnss_slurm.so.2`

// nssSlurmSwitch puts slurm first on the passwd and group lines of
// /etc/nsswitch.conf, before the sources the image has, and fails unless
// both lines now start with it.
const nssSlurmSwitch = `sed -i --follow-symlinks -E 's/^(passwd|group):[[:space:]]*/\1: slurm /' /etc/nsswitch.conf
grep -q '^passwd: slurm ' /etc/nsswitch.conf
grep -q '^group: slurm ' /etc/nsswitch.conf`

// enableNSSSlurm makes a worker resolve users and groups with nss_slurm
// first. Inside a job step, nss_slurm answers for the job's user and groups
// from the job credential, which slurmctld fills with
// LaunchParameters=enable_nss_slurm; outside of one it finds nothing, and
// the image's own sources answer. It first checks that the image has the
// module, which custom and older images may lack.
func enableNSSSlurm(ctx context.Context, client *docker.Client, container docker.ContainerName, nc RunConfig) error {
	if _, err := client.Exec(ctx, container, "sh", "-c", nssSlurmCheck); err != nil {
		return fmt.Errorf("image %s has no libnss_slurm.so.2, which identity %s needs on managed workers; build the image with contribs/nss_slurm: %w", nc.Image, nc.Identity, err)
	}
	if _, err := client.Exec(ctx, container, "sh", "-ec", nssSlurmSwitch); err != nil {
		return fmt.Errorf("switching passwd and group lookups to nss_slurm: %w", err)
	}
	return nil
}

// nodeSlurmService returns the service sind enables and waits for on a
// managed node: the role's Slurm daemon, or sackd on the submitter with
// identity clientIds, which hands its client commands their auth/slurm
// tokens. Other nodes get theirs from their Slurm daemon.
func nodeSlurmService(nc RunConfig) (probe.Service, bool) {
	if nc.Role == config.RoleSubmitter && nc.Identity == config.IdentityClientIDs {
		return probe.ServiceSackd, true
	}
	return probe.ServiceForRole(nc.Role)
}
