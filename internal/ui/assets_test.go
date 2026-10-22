// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedAssets(t *testing.T) {
	a, err := newAssets(webFS())
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{12}$`, a.version)
	for _, name := range []string{
		"app.css", "app.js", "icon.svg", "htmx.min.js", "htmx-ext-sse.js", "idiomorph-ext.min.js",
		"fonts/inter-latin-wght.woff2", "fonts/jetbrains-mono-latin-wght.woff2",
		"licenses/htmx-0BSD.txt", "licenses/htmx-ext-sse-0BSD.txt", "licenses/idiomorph-0BSD.txt",
		"licenses/inter-OFL.txt", "licenses/jetbrains-mono-OFL.txt", "licenses/NOTICE.txt",
	} {
		_, err := fs.Stat(webFS(), "static/"+name)
		assert.NoError(t, err, name)
	}
}

func TestAssetsServe(t *testing.T) {
	a, err := newAssets(webFS())
	require.NoError(t, err)
	serve := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		return w
	}
	for name, ct := range map[string]string{
		"app.css":                               "text/css; charset=utf-8",
		"app.js":                                "text/javascript; charset=utf-8",
		"icon.svg":                              "image/svg+xml",
		"fonts/inter-latin-wght.woff2":          "font/woff2",
		"licenses/NOTICE.txt":                   "text/plain; charset=utf-8",
		"licenses/htmx-0BSD.txt":                "text/plain; charset=utf-8",
		"fonts/jetbrains-mono-latin-wght.woff2": "font/woff2",
	} {
		w := serve(a.url(name))
		require.Equal(t, http.StatusOK, w.Code, name)
		assert.Equal(t, ct, w.Header().Get("Content-Type"), name)
		assert.Equal(t, "private, max-age=31536000, immutable", w.Header().Get("Cache-Control"), name)
		assert.NotEmpty(t, w.Body.Bytes(), name)
	}
	for _, target := range []string{
		"/static/0123456789ab/app.css", // another version
		"/static/" + a.version + "/nope.js",
		"/static/" + a.version + "/",
		"/static/" + a.version,
		"/static/" + a.version + "/../templates/layout.tmpl",
		"/static/" + a.version + "/fonts",
	} {
		assert.Equal(t, http.StatusNotFound, serve(target).Code, target)
	}
}

func TestContentTypes(t *testing.T) {
	for name, want := range map[string]string{
		"a.png": "image/png", "a.json": "application/json", "a.woff2": "font/woff2",
		"a.bin": "application/octet-stream", "noext": "application/octet-stream",
	} {
		assert.Equal(t, want, contentType(name), name)
	}
}

func TestAssetVersionFollowsContent(t *testing.T) {
	files := func(css string) fstest.MapFS {
		return fstest.MapFS{"static/app.css": {Data: []byte(css)}}
	}
	a1, err := newAssets(files("a{}"))
	require.NoError(t, err)
	a2, err := newAssets(files("a{}"))
	require.NoError(t, err)
	a3, err := newAssets(files("b{}"))
	require.NoError(t, err)
	assert.Equal(t, a1.version, a2.version)
	assert.NotEqual(t, a1.version, a3.version)
}

func TestExtraStylesAndScripts(t *testing.T) {
	a, err := newAssets(fstest.MapFS{
		"static/app.css":     {Data: []byte("x")},
		"static/css/b.css":   {Data: []byte("b")},
		"static/css/a.css":   {Data: []byte("a")},
		"static/css/note.md": {Data: []byte("not a stylesheet")},
		"static/js/z.js":     {Data: []byte("z")},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{a.url("css/a.css"), a.url("css/b.css")}, a.styles)
	assert.Equal(t, []string{a.url("js/z.js")}, a.scripts)
}

func TestAssetsWithoutStaticDirectory(t *testing.T) {
	_, err := newAssets(fstest.MapFS{})
	assert.Error(t, err)
}

func TestAssetsUnreadableFile(t *testing.T) {
	_, err := newAssets(failingFS{fstest.MapFS{"static/app.css": {Data: []byte("x")}}})
	assert.ErrorIs(t, err, fs.ErrPermission)
}

// failingFS lists its files but cannot read them.
type failingFS struct{ fstest.MapFS }

func (f failingFS) Open(name string) (fs.File, error) {
	file, err := f.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	if info, err := file.Stat(); err == nil && !info.IsDir() {
		_ = file.Close()
		return nil, fs.ErrPermission
	}
	return file, nil
}

func (failingFS) ReadFile(string) ([]byte, error) { return nil, fs.ErrPermission }
