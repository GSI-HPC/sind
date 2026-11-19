// SPDX-License-Identifier: LGPL-3.0-or-later

package mcpconfig

import (
	"io"
	"io/fs"
)

// OpenBackup replaces the creation of a backup file in a test, and returns
// the function that puts the original back.
func OpenBackup(open func(name string, mode fs.FileMode) (io.WriteCloser, error)) (restore func()) {
	orig := openBackup
	openBackup = open
	return func() { openBackup = orig }
}

// Geteuid replaces the user whose links Load follows in a test, and returns
// the function that puts the original back.
func Geteuid(uid int) (restore func()) {
	orig := geteuid
	geteuid = func() int { return uid }
	return func() { geteuid = orig }
}
