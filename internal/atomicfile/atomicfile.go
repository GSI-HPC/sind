// SPDX-License-Identifier: LGPL-3.0-or-later

// Package atomicfile replaces a file in one step, so that a reader sees
// either the old file or the new one, never part of one.
package atomicfile

import (
	"io/fs"
	"path/filepath"

	"github.com/spf13/afero"
)

// Write writes data to path on fsys through a new temporary file in the
// same directory, which gets perm and is then renamed over path. The
// directory must exist. On an error the temporary file is removed and
// path is left as it was.
func Write(fsys afero.Fs, path string, data []byte, perm fs.FileMode) error {
	tmp, err := afero.TempFile(fsys, filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = fsys.Chmod(tmp.Name(), perm)
	}
	if err == nil {
		err = fsys.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = fsys.Remove(tmp.Name())
	}
	return err
}
