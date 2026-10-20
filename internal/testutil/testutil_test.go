// SPDX-License-Identifier: LGPL-3.0-or-later

package testutil

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNDJSON(t *testing.T) {
	type entry struct {
		Name string `json:"name"`
	}
	got := NDJSON(entry{Name: "a"}, entry{Name: "b"})
	assert.Equal(t, `{"name":"a"}`+"\n"+`{"name":"b"}`+"\n", got)
}

func TestNDJSON_Single(t *testing.T) {
	got := NDJSON(PsEntry{ID: "abc", Names: "c1"})
	assert.Contains(t, got, `"ID":"abc"`)
	assert.Contains(t, got, `"Names":"c1"`)
}

func TestTarArchive(t *testing.T) {
	archive := TarArchive("hello.txt", "world")
	tr := tar.NewReader(bytes.NewReader([]byte(archive)))
	hdr, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "hello.txt", hdr.Name)
	assert.Equal(t, int64(5), hdr.Size)
	content, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))
}

func TestImage(t *testing.T) {
	t.Setenv("SIND_TEST_IMAGE", "")
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:latest", Image())
	t.Setenv("SIND_TEST_IMAGE", "local/sind-node:ci")
	assert.Equal(t, "local/sind-node:ci", Image())
}

func TestArgValue(t *testing.T) {
	args := []string{"--name", "foo", "--count", "3"}

	val, ok := ArgValue(args, "--name")
	assert.True(t, ok)
	assert.Equal(t, "foo", val)

	val, ok = ArgValue(args, "--count")
	assert.True(t, ok)
	assert.Equal(t, "3", val)

	_, ok = ArgValue(args, "--missing")
	assert.False(t, ok)
}

func TestArgValue_Empty(t *testing.T) {
	_, ok := ArgValue(nil, "--flag")
	assert.False(t, ok)
}

func TestArgValues(t *testing.T) {
	args := []string{"--label", "a=1", "--name", "foo", "--label", "b=2"}
	got := ArgValues(args, "--label")
	assert.Equal(t, []string{"a=1", "b=2"}, got)
}

func TestArgValues_None(t *testing.T) {
	got := ArgValues([]string{"--name", "foo"}, "--label")
	assert.Nil(t, got)
}

func TestExitCode1(t *testing.T) {
	err := ExitCode1(t)
	assert.Equal(t, 1, err.ExitCode())
}
