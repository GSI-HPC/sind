// SPDX-License-Identifier: LGPL-3.0-or-later

package testutil_test

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
)

// TestNoSuch checks that each not-found message, with exit code 1, is an
// error docker.IsNotFound recognises.
func TestNoSuch(t *testing.T) {
	for _, stderr := range []string{
		testutil.NoSuchContainer("c"),
		testutil.NoSuchNetwork("n"),
		testutil.NoSuchVolume("v"),
	} {
		err := cmdexec.WrapExitError(testutil.ExitCode1(t), stderr)
		assert.True(t, docker.IsNotFound(err), stderr)
	}
}
