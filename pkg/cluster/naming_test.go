// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
)

func TestCustomRealm_NamingFunctions(t *testing.T) {
	realm := "myrealm"
	assert.Equal(t, docker.NetworkName("myrealm-dev-net"), NetworkName(realm, "dev"))
	assert.Equal(t, docker.ContainerName("myrealm-dev-controller"), ContainerName(realm, "dev", "controller"))
	assert.Equal(t, docker.ContainerName("myrealm-dev-worker-0"), ContainerName(realm, "dev", "worker-0"))
	assert.Equal(t, docker.VolumeName("myrealm-dev-config"), VolumeName(realm, "dev", VolumeConfig))
	assert.Equal(t, "myrealm-dev-", ContainerPrefix(realm, "dev"))
	assert.Equal(t, "myrealm-dev", ComposeProject(realm, "dev"))
}
