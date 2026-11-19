// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"crypto/rand"
	"fmt"
)

const testContainerName ContainerName = "sind-dev-controller"

// testID is a per-process random suffix so parallel integration test
// runs don't collide on Docker resource names.
var testID = func() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}()

// itName returns a unique Docker resource name for integration tests.
func itName(base string) string {
	return "sind-it-" + base + "-" + testID
}

func itContainerName(base string) ContainerName { return ContainerName(itName(base)) }
func itNetworkName(base string) NetworkName     { return NetworkName(itName(base)) }
func itVolumeName(base string) VolumeName       { return VolumeName(itName(base)) }
