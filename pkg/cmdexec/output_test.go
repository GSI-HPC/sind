// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStdoutIsData(t *testing.T) {
	assert.False(t, StdoutIsData(t.Context()))
	assert.True(t, StdoutIsData(WithStdoutAsData(t.Context())))
}
