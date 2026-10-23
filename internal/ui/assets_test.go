// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"io/fs"
	"net/http"
	"testing"

	"github.com/GSI-HPC/go-clikit/localweb/htmx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedAssets(t *testing.T) {
	for _, name := range []string{
		"app.css", "app.js", "icon.svg", "icons.svg",
		"fonts/inter-latin-wght.woff2", "fonts/jetbrains-mono-latin-wght.woff2",
		"licenses/inter-OFL.txt", "licenses/jetbrains-mono-OFL.txt", "licenses/NOTICE.txt",
	} {
		_, err := fs.Stat(webFS(), "static/"+name)
		assert.NoError(t, err, name)
	}
}

func TestEmbeddedAssetsAreServed(t *testing.T) {
	cfg, st := testConfig(t)
	ts := newTestServerFrom(t, cfg, st).login()
	a := ts.srv.assets
	assert.Regexp(t, `^[0-9a-f]{12}$`, a.Version())
	for name, ct := range map[string]string{
		"app.css":                      "text/css; charset=utf-8",
		"app.js":                       "text/javascript; charset=utf-8",
		"icon.svg":                     "image/svg+xml",
		"fonts/inter-latin-wght.woff2": "font/woff2",
		"licenses/NOTICE.txt":          "text/plain; charset=utf-8",
	} {
		w := ts.get(a.URL(name))
		require.Equal(t, http.StatusOK, w.Code, name)
		assert.Equal(t, ct, w.Header().Get("Content-Type"), name)
		assert.Equal(t, "private, max-age=31536000, immutable", w.Header().Get("Cache-Control"), name)
	}
	assert.Equal(t, http.StatusNotFound, ts.get("/static/0123456789ab/app.css").Code, "another version")
}

func TestHTMXIsServedFromTheKit(t *testing.T) {
	cfg, st := testConfig(t)
	ts := newTestServerFrom(t, cfg, st).login()
	for _, name := range []string{htmx.Script, htmx.SSE, htmx.Morph} {
		w := ts.get(ts.srv.vendor.URL(name))
		require.Equal(t, http.StatusOK, w.Code, name)
		assert.Equal(t, "text/javascript; charset=utf-8", w.Header().Get("Content-Type"), name)
		assert.Equal(t, "private, max-age=31536000, immutable", w.Header().Get("Cache-Control"), name)
		assert.Equal(t, http.StatusNotFound, ts.get(ts.srv.assets.URL(name)).Code, "no local copy of "+name)
	}
}

func TestRailIcons(t *testing.T) {
	data, err := fs.ReadFile(webFS(), "static/icons.svg")
	require.NoError(t, err)
	for _, it := range navItems {
		assert.Contains(t, string(data), `<symbol id="`+it.Key+`" `, it.Key)
	}
}
