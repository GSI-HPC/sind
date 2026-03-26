// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/docker"
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
			assert.Equal(t, tt.want, NetworkName(tt.cluster))
		})
	}
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
			assert.Equal(t, tt.want, ContainerName(tt.cluster, tt.shortName))
		})
	}
}

func TestVolumeName(t *testing.T) {
	tests := []struct {
		name       string
		cluster    string
		volumeType string
		want       docker.VolumeName
	}{
		{"config", "dev", "config", "sind-dev-config"},
		{"munge", "dev", "munge", "sind-dev-munge"},
		{"data", "dev", "data", "sind-dev-data"},
		{"default cluster", "default", "config", "sind-default-config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VolumeName(tt.cluster, tt.volumeType))
		})
	}
}
