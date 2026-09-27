// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"os"
	"testing"
)

// TestMain runs the tests without the caller's sind settings: an exported
// SIND_REALM, as sind-action sets it in CI, would move every command into
// another realm than the one the tests expect, and XDG_STATE_HOME would
// point the state directory at the developer's own. A test that needs one
// of them sets it with t.Setenv.
//
// Adapted from GSI-HPC/clusterctl internal/cli/main_test.go.
func TestMain(m *testing.M) {
	for _, name := range []string{"SIND_REALM", "XDG_STATE_HOME"} {
		_ = os.Unsetenv(name)
	}
	os.Exit(m.Run())
}
