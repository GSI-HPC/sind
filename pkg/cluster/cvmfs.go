// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"slices"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// CVMFS on the nodes (storage.cvmfs).
const (
	// CVMFSPath is where the nodes mount CVMFS, and where the Docker host
	// has it for the bind mount.
	CVMFSPath = "/cvmfs"
	// CVMFSVolumeDriver is the name of the Docker volume plugin that
	// provides CVMFS.
	CVMFSVolumeDriver = "cvmfs"
	// CVMFSVolume is the plugin volume the nodes mount. All clusters share
	// it, so sind never removes it.
	CVMFSVolume docker.VolumeName = "cvmfs"
)

// DetectCVMFS picks how the nodes mount CVMFS: config.StorageVolume when the
// cvmfs Docker volume plugin is installed and enabled, otherwise
// config.StorageHostPath, a bind mount of the Docker host's /cvmfs. The bind
// mount is tried in a throwaway container of image first, so a host without
// /cvmfs, or one whose /cvmfs Docker cannot propagate into containers, fails
// before any node is created.
func DetectCVMFS(ctx context.Context, client *docker.Client, image string) (config.StorageType, error) {
	plugins, err := client.VolumePlugins(ctx)
	if err != nil {
		return "", fmt.Errorf("listing volume plugins: %w", err)
	}
	// docker resolves volume-driver=cvmfs to the plugin named cvmfs:latest.
	if slices.Contains(plugins, CVMFSVolumeDriver) || slices.Contains(plugins, CVMFSVolumeDriver+":latest") {
		return config.StorageVolume, nil
	}
	// --entrypoint, as custom node images may set one.
	probeArgs := append(cvmfsMountArgs(config.StorageHostPath), "--entrypoint", "true")
	if _, err := client.RunEphemeralWith(ctx, probeArgs, image, false); err != nil {
		return "", fmt.Errorf("storage.cvmfs: no %q volume plugin is enabled and the Docker host's %s cannot be bind-mounted; "+
			"install CVMFS on the Docker host or the %q Docker volume plugin: %w", CVMFSVolumeDriver, CVMFSPath, CVMFSVolumeDriver, err)
	}
	return config.StorageHostPath, nil
}

// cvmfsMountArgs returns the docker flags that mount CVMFS read-only at
// /cvmfs with the given backend, or none for an empty backend. The bind
// mount is a slave of the host's /cvmfs: repositories that the host's autofs
// mounts on first access, even from inside a container, appear in the
// containers too.
func cvmfsMountArgs(backend config.StorageType) []string {
	switch backend {
	case config.StorageVolume:
		return []string{"--mount", "type=volume,volume-driver=" + CVMFSVolumeDriver + ",source=" + string(CVMFSVolume) + ",target=" + CVMFSPath + ",readonly"}
	case config.StorageHostPath:
		return []string{"--mount", "type=bind,source=" + CVMFSPath + ",target=" + CVMFSPath + ",readonly,bind-propagation=rslave"}
	}
	return nil
}

// cvmfsSource returns what the nodes mount at /cvmfs with the given backend:
// the plugin volume or the host path.
func cvmfsSource(backend config.StorageType) string {
	if backend == config.StorageVolume {
		return string(CVMFSVolume)
	}
	return CVMFSPath
}
