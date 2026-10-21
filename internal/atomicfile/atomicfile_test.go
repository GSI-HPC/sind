// SPDX-License-Identifier: LGPL-3.0-or-later

package atomicfile_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/atomicfile"
)

// names returns the names of the entries of dir.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	require.NoError(t, atomicfile.Write(afero.NewOsFs(), path, []byte("new"), 0o600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
	assert.Equal(t, []string{"f"}, names(t, dir), "no temporary file is left")
}

func TestWrite_Errors(t *testing.T) {
	fsys := afero.NewOsFs()
	dir := t.TempDir()

	// No directory to create the temporary file in.
	require.Error(t, atomicfile.Write(fsys, filepath.Join(dir, "missing", "f"), nil, 0o600))

	// A directory in the way of the rename.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "d", "x"), 0o755))
	require.Error(t, atomicfile.Write(fsys, filepath.Join(dir, "d"), []byte("new"), 0o600))
	assert.Equal(t, []string{"d"}, names(t, dir), "the temporary file is removed")
}
