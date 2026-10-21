// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
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

// assets serves the static files under /static/VERSION/, where VERSION is a
// hash of all of them: a new sind serves them under a new path, so browsers
// may cache them for good.
type assets struct {
	fs      fs.FS
	version string
	// styles and scripts are the extra files of every page, as URLs:
	// static/css/*.css and static/js/*.js.
	styles  []string
	scripts []string
}

// newAssets returns the static files of fsys, which holds them in static/.
func newAssets(fsys fs.FS) (*assets, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	a := &assets{fs: fsys, version: hex.EncodeToString(h.Sum(nil))[:12]}
	a.styles = a.extra("css")
	a.scripts = a.extra("js")
	return a, nil
}

// extra returns the URLs of the files with the extension of dir in
// static/dir, in name order.
func (a *assets) extra(dir string) []string {
	files, _ := fs.Glob(a.fs, "static/"+dir+"/*."+dir) // the pattern is valid
	urls := make([]string, len(files))
	for i, f := range files {
		urls[i] = a.url(strings.TrimPrefix(f, "static/"))
	}
	return urls
}

// url returns the path under which the static file name is served.
func (a *assets) url(name string) string {
	return "/static/" + a.version + "/" + name
}

// ServeHTTP serves GET /static/VERSION/NAME.
func (a *assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/static/")
	version, name, ok := strings.Cut(rest, "/")
	if !ok || version != a.version || name == "" || !fs.ValidPath(name) {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(a.fs, path.Join("static", name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", contentType(name))
	_, _ = w.Write(data) //nolint:gosec // G705: an embedded file, served with its own type
}

// contentType returns the media type of a static file by its extension.
// The list is fixed, so the host's mime database plays no part.
func contentType(name string) string {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".json":
		return "application/json"
	case ".txt":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}
