// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateSlurmKey(t *testing.T) {
	a, b := GenerateSlurmKey(), GenerateSlurmKey()
	assert.Len(t, a, SlurmKeySize)
	assert.NotEqual(t, a, b)
	assert.Equal(t, "/etc/slurm/slurm.key", SlurmKeyPath)
}

func TestGenerateJWTKey(t *testing.T) {
	a, b := GenerateJWTKey(), GenerateJWTKey()
	assert.Len(t, a, JWTKeySize)
	assert.NotEqual(t, a, b)
	assert.Equal(t, "/etc/slurm/jwt_hs256.key", JWTKeyPath)
}
