// SPDX-License-Identifier: LGPL-3.0-or-later

package mcpconfig

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatch_PanicsOnAMissingMember(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "c.json"), "servers")
	require.NoError(t, err)
	assert.PanicsWithValue(t, `mcpconfig: remove /servers/sind: hujson: patch operation 0: value not found`, func() {
		f.patch("remove", "/servers/sind", nil)
	})
}
