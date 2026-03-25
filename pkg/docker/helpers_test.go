// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"archive/tar"
	"bytes"
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

// copyFromTar builds a tar archive containing a single file, as docker cp would return.
func copyFromTar(content string) string {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte(content)
	_ = tw.WriteHeader(&tar.Header{Name: "file", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()
	return buf.String()
}

// inspectRunning returns mock docker inspect JSON for a running container.
func inspectRunning(name string) string {
	return `[{"Id":"abc123","Name":"/` + name + `","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`
}

// inspectExited returns mock docker inspect JSON for an exited container.
func inspectExited(name string) string {
	return `[{"Id":"abc123","Name":"/` + name + `","State":{"Status":"exited"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`
}

// inspectPaused returns mock docker inspect JSON for a paused container.
func inspectPaused(name string) string {
	return `[{"Id":"abc123","Name":"/` + name + `","State":{"Status":"paused"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`
}
