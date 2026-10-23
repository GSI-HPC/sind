// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"embed"
	"io/fs"
)

// web holds the templates and the static files: CSS, JavaScript, htmx and
// the fonts, so that sind ui works offline and loads nothing from elsewhere.
//
//go:embed web
var web embed.FS

// webFS returns the embedded files with web/ as the root: templates/ and
// static/.
func webFS() fs.FS {
	sub, _ := fs.Sub(web, "web") // "web" is a valid directory name
	return sub
}
