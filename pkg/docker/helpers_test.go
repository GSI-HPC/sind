// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"github.com/GSI-HPC/sind/internal/testutil/itest"
)

const testContainerName ContainerName = "sind-dev-controller"

// itName returns a unique Docker resource name for integration tests.
func itName(base string) string {
	return "sind-it-" + base + "-" + itest.RunID
}

func itContainerName(base string) ContainerName { return ContainerName(itName(base)) }
func itNetworkName(base string) NetworkName     { return NetworkName(itName(base)) }
func itVolumeName(base string) VolumeName       { return VolumeName(itName(base)) }
