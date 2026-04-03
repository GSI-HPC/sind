// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package mesh

import (
	"crypto/rand"
	"fmt"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// testRealm is a per-process unique realm so parallel integration test
// runs don't collide on Docker resource names.
var testRealm = func() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("it-mesh-%x", b)
}()

// lifecycleRealm returns a per-test unique realm for integration tests,
// allowing lifecycle tests to run in parallel within the package.
func lifecycleRealm(_ *cmdexec.Recorder) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("it-mesh-%x", b)
}
