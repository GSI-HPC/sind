// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the tests of the package when one of them leaves a
// goroutine that still runs after all tests are done.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
