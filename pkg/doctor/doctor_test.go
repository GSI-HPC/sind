// SPDX-License-Identifier: LGPL-3.0-or-later

package doctor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		major int
		minor int
	}{
		{"28.0.0", 28, 0},
		{"29.3.1", 29, 3},
		{"28.0.0-beta.1", 28, 0},
	}
	for _, tt := range tests {
		major, minor, err := ParseVersion(tt.input)
		require.NoError(t, err, tt.input)
		assert.Equal(t, tt.major, major, tt.input)
		assert.Equal(t, tt.minor, minor, tt.input)
	}
}

func TestParseVersion_Invalid(t *testing.T) {
	_, _, err := ParseVersion("bogus")
	assert.Error(t, err)

	_, _, err = ParseVersion("28")
	assert.Error(t, err)
}

func TestCheckDockerVersion(t *testing.T) {
	assert.NoError(t, CheckDockerVersion("28.0.0"))
	assert.NoError(t, CheckDockerVersion("29.3.1"))
	assert.NoError(t, CheckDockerVersion("28.0.0-beta.1"))
}

func TestCheckDockerVersion_TooOld(t *testing.T) {
	err := CheckDockerVersion("27.5.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires >= 28.0")
}

func TestCheckDockerVersion_Invalid(t *testing.T) {
	err := CheckDockerVersion("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse version")
}
