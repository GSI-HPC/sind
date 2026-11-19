// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import "github.com/GSI-HPC/sind/pkg/docker"

// The resource names of DefaultRealm, which the tests expect; the code
// takes them from RealmNames or the Manager methods.
const (
	defaultNetworkName  docker.NetworkName   = "sind-mesh"
	defaultDNSContainer docker.ContainerName = "sind-dns"
	defaultSSHContainer docker.ContainerName = "sind-ssh"
	defaultSSHVolume    docker.VolumeName    = "sind-ssh-config"
)
