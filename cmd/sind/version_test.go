// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setVersion(t *testing.T, v, c string) {
	t.Helper()
	oldVersion, oldCommit := version, commit
	t.Cleanup(func() { version, commit = oldVersion, oldCommit })
	version = v
	commit = c
}

func TestVersionCommand_PlainRelease(t *testing.T) {
	setVersion(t, "v1.2.3", "abc1234")

	stdout, _, err := executeCommand("version")
	require.NoError(t, err)
	assert.Equal(t, "sind 1.2.3 (abc1234)\n", stdout)
}

func TestVersionCommand_PlainDevDescribe(t *testing.T) {
	setVersion(t, "0.5.0-3-gabc1234-dirty", "")

	stdout, _, err := executeCommand("version")
	require.NoError(t, err)
	assert.Equal(t, "sind 0.5.0-3-gabc1234-dirty\n", stdout)
}

func TestVersionCommand_PlainNoGit(t *testing.T) {
	setVersion(t, "dev", "")

	stdout, _, err := executeCommand("version")
	require.NoError(t, err)
	assert.Equal(t, "sind dev\n", stdout)
}

func TestVersionCommand_PlainStripsVPrefix(t *testing.T) {
	setVersion(t, "v1.2.3", "abc1234")

	stdout, _, err := executeCommand("version")
	require.NoError(t, err)
	assert.Contains(t, stdout, "1.2.3")
	assert.NotContains(t, stdout, "v1.2.3")
}

func TestVersionCommand_JSONOutput(t *testing.T) {
	setVersion(t, "v1.2.3", "abc1234")

	stdout, _, err := executeCommand("version", "--json")
	require.NoError(t, err)

	var info versionInfo
	require.NoError(t, json.Unmarshal([]byte(stdout), &info))
	assert.Equal(t, "1.2.3", info.Version)
	assert.Equal(t, "abc1234", info.Commit)
	assert.NotEmpty(t, info.GoVer)
	assert.NotEmpty(t, info.Platform)
}

func TestVersionCommand_JSONOutput_NoCommit(t *testing.T) {
	setVersion(t, "dev", "")

	stdout, _, err := executeCommand("version", "--json")
	require.NoError(t, err)

	var raw map[string]string
	require.NoError(t, json.Unmarshal([]byte(stdout), &raw))
	assert.Equal(t, "dev", raw["version"])
	assert.Empty(t, raw["commit"])
}

func TestVersionCommand_ExtraArgs(t *testing.T) {
	_, _, err := executeCommand("version", "extra")
	assert.Error(t, err)
}

func TestResolveVersion_LdflagsWin(t *testing.T) {
	setVersion(t, "v1.2.3", "")

	assert.Equal(t, "v1.2.3", resolveVersion())
}

func TestResolveVersion_TestBinaryIsDev(t *testing.T) {
	setVersion(t, "dev", "")

	// A test binary reports "(devel)" or no main module version.
	assert.Equal(t, "dev", resolveVersion())
}

func TestVersionCommand_ModuleVersion(t *testing.T) {
	setVersion(t, "v0.9.0-0.20260927120000-abcdef123456", "")

	stdout, _, err := executeCommand("version")
	require.NoError(t, err)
	assert.Equal(t, "sind 0.9.0-0.20260927120000-abcdef123456\n", stdout)
}

func TestVersionFlag(t *testing.T) {
	setVersion(t, "v1.2.3", "")

	stdout, _, err := executeCommand("--version")

	require.NoError(t, err)
	assert.Equal(t, "sind 1.2.3\n", stdout)
}
