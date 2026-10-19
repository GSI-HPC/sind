// SPDX-License-Identifier: LGPL-3.0-or-later

package mcpconfig

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatch_PanicsOnAMissingMember(t *testing.T) {
	f, err := Load(afero.NewMemMapFs(), "/c.json", "servers")
	require.NoError(t, err)
	assert.PanicsWithValue(t, `mcpconfig: remove /servers/sind: hujson: patch operation 0: value not found`, func() {
		f.patch("remove", "/servers/sind", nil)
	})
}
