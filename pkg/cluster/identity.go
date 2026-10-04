// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strings"

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

// nssSlurmSteps returns the node setup steps (see nodeSetupSteps) that make
// a worker resolve users and groups with nss_slurm first. Inside a job
// step, nss_slurm answers for the job's user and groups from the job
// credential, which slurmctld fills with LaunchParameters=enable_nss_slurm;
// outside of one it finds nothing, and the image's own sources answer. The
// first step checks that the image has the module, which custom and older
// images may lack.
func nssSlurmSteps(nc RunConfig) []setupStep {
	return []setupStep{
		{
			name:   "nss_slurm",
			what:   fmt.Sprintf("image %s has no libnss_slurm.so.2, which identity %s needs on managed workers; use a current sind-node image (--pull refreshes a cached one), or build yours with contribs/nss_slurm", nc.Image, nc.Identity),
			script: nssSlurmCheck,
		},
		{
			name:   "nsswitch",
			what:   "switching passwd and group lookups to nss_slurm",
			script: nssSlurmSwitch,
		},
	}
}

// Image labels that tell sind-node images apart.
const (
	// imageTitleLabel is the OCI title label, "sind-node" on sind's images.
	imageTitleLabel = "org.opencontainers.image.title"
	sindNodeTitle   = "sind-node"
	// identityImageLabel is on the sind-node images that have nss_slurm,
	// auth/slurm and sackd, which the identity modes other than local
	// need: it came with them.
	identityImageLabel = "sind.libjwt.version"
)

// officialImageRepo is the repository of the official sind-node images, of
// which config.DefaultImage is a tag.
const officialImageRepo = "ghcr.io/gsi-hpc/sind-node"

// checkIdentityImage refuses a local sind-node image from before identity
// modes, which lacks nss_slurm and auth/slurm, before sind creates nodes
// with it: the nss_slurm step of their setup (nssSlurmSteps) would only
// fail once they have booted. The usual cause is a cached image of an
// official tag after a sind upgrade, as docker reuses it without --pull. An
// image that is not local passes, as docker pulls a current one, and so
// does any image but sind-node's, which that step checks.
func checkIdentityImage(ctx context.Context, client *docker.Client, image string, mode config.IdentityMode) error {
	labels, local, err := client.ImageLabels(ctx, image)
	if err != nil {
		return fmt.Errorf("inspecting image %s: %w", image, err)
	}
	if !local || labels[imageTitleLabel] != sindNodeTitle || labels[identityImageLabel] != "" {
		return nil
	}
	remedy := "rebuild it from the current sind-node Dockerfile"
	if strings.HasPrefix(image, officialImageRepo+":") || strings.HasPrefix(image, officialImageRepo+"@") {
		remedy = "pull a current one with --pull"
	}
	return fmt.Errorf("image %s is a sind-node image from before identity modes, without the nss_slurm and auth/slurm that identity %s needs; %s", image, mode, remedy)
}

// checkIdentityImages runs checkIdentityImage on the images of the nodes
// that need what the cluster's identity mode needs: the managed workers
// with nssSlurm, every managed node with clientIds. With --pull docker
// fetches current images, so there is nothing to check.
func checkIdentityImages(ctx context.Context, client *docker.Client, cfg *config.Cluster) error {
	if cfg.Pull || !cfg.Managed() || !cfg.Identity.UsesNSSSlurm() {
		return nil
	}
	checked := make(map[string]bool)
	for _, n := range cfg.Nodes {
		managed := n.Managed == nil || *n.Managed
		needs := n.Role == config.RoleWorker || cfg.Identity.Mode == config.IdentityClientIDs
		if !managed || !needs || n.Image == "" || checked[n.Image] {
			continue
		}
		checked[n.Image] = true
		if err := checkIdentityImage(ctx, client, n.Image, cfg.Identity.Mode); err != nil {
			return err
		}
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
