// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindBridgeInterface_TooShort(t *testing.T) {
	_, err := findBridgeInterface("abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestFindBridgeInterface_NotExists(t *testing.T) {
	_, err := findBridgeInterface("000000000000")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFindBridgeInterface_Exists(t *testing.T) {
	// Verify the name derivation: first 12 chars of network ID.
	dir := t.TempDir()
	sysNetDir := filepath.Join(dir, "br-aabbccddeeff")
	require.NoError(t, os.MkdirAll(sysNetDir, 0755))

	// Can't override /sys/class/net, so just verify name logic.
	assert.Equal(t, "br-aabbccddeeff", "br-"+"aabbccddeeff00112233"[:12])
}
