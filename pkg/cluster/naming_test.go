// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
)

func TestNetworkName(t *testing.T) {
	tests := []struct {
		cluster string
		want    docker.NetworkName
	}{
		{"dev", "sind-dev-net"},
		{"default", "sind-default-net"},
		{"test-cluster", "sind-test-cluster-net"},
	}
	for _, tt := range tests {
		t.Run(tt.cluster, func(t *testing.T) {
			assert.Equal(t, tt.want, NetworkName(mesh.DefaultRealm, tt.cluster))
		})
	}
}

func TestCustomRealm_NamingFunctions(t *testing.T) {
	realm := "myrealm"
	assert.Equal(t, docker.NetworkName("myrealm-dev-net"), NetworkName(realm, "dev"))
	assert.Equal(t, docker.ContainerName("myrealm-dev-controller"), ContainerName(realm, "dev", "controller"))
	assert.Equal(t, docker.ContainerName("myrealm-dev-worker-0"), ContainerName(realm, "dev", "worker-0"))
	assert.Equal(t, docker.VolumeName("myrealm-dev-config"), VolumeName(realm, "dev", VolumeConfig))
	assert.Equal(t, "myrealm-dev-", ContainerPrefix(realm, "dev"))
	assert.Equal(t, "myrealm-dev", ComposeProject(realm, "dev"))
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		name      string
		cluster   string
		shortName string
		want      docker.ContainerName
	}{
		{"controller", "dev", "controller", "sind-dev-controller"},
		{"submitter", "dev", "submitter", "sind-dev-submitter"},
		{"worker-0", "dev", "worker-0", "sind-dev-worker-0"},
		{"worker-1", "dev", "worker-1", "sind-dev-worker-1"},
		{"default cluster", "default", "controller", "sind-default-controller"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ContainerName(mesh.DefaultRealm, tt.cluster, tt.shortName))
		})
	}
}

func TestVolumeName(t *testing.T) {
	tests := []struct {
		name       string
		cluster    string
		volumeType VolumeType
		want       docker.VolumeName
	}{
		{"config", "dev", VolumeConfig, "sind-dev-config"},
		{"munge", "dev", VolumeMunge, "sind-dev-munge"},
		{"data", "dev", VolumeData, "sind-dev-data"},
		{"default cluster", "default", VolumeConfig, "sind-default-config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VolumeName(mesh.DefaultRealm, tt.cluster, tt.volumeType))
		})
	}
}
