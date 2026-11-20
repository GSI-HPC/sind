// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateMungeKey(t *testing.T) {
	key := GenerateMungeKey()
	assert.Len(t, key, MungeKeySize)
}

func TestGenerateMungeKey_Unique(t *testing.T) {
	key1 := GenerateMungeKey()
	key2 := GenerateMungeKey()
	assert.NotEqual(t, key1, key2)
}
