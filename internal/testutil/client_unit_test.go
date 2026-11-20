// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !integration

package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSkipWithoutDocker(t *testing.T) {
	ran, skipped := false, false
	t.Run("unit mode", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		SkipWithoutDocker(t)
		ran = true
	})
	assert.True(t, skipped)
	assert.False(t, ran)
}
