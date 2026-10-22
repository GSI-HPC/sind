// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/GSI-HPC/go-clikit/localweb"
	"github.com/GSI-HPC/go-clikit/localweb/localwebtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveServer serves a test server on a real loopback listener, as Run
// does, so that localwebtest can drive it. base is the address of the
// server without the token.
func liveServer(t *testing.T) (ts *testServer, base string) {
	t.Helper()
	cfg, st := testConfig(t)
	b, err := localweb.Listen(t.Context(), "127.0.0.1:0", localweb.ListenOptions{})
	require.NoError(t, err)
	cfg.Bound, cfg.Env.Addr = b, b.Host()
	ts = newTestServerFrom(t, cfg, st)
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	served := make(chan error, 1)
	go func() { served <- localweb.Serve(ctx, b.Listeners, ts.h, localweb.ServeOptions{}) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-served)
	})
	return ts, "http://" + b.Host()
}

func TestLiveSessionAndCSRF(t *testing.T) {
	ts, base := liveServer(t)
	changed := make(chan string, 1)
	ts.srv.mux.HandleFunc("POST /changes", func(_ http.ResponseWriter, r *http.Request) { changed <- r.PostForm.Get("name") })

	c := localwebtest.Login(t, base, "the-token")
	resp := c.Get(t, "/demo/x")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	// A browser that sends no CSRF token changes nothing.
	form := url.Values{"name": {"dev"}}
	resp = c.PostForm(t, "/changes", form)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Empty(t, changed, "the handler was not reached")

	resp = c.Get(t, csrfPath)
	token, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.NoError(t, err)
	c.Header.Set(csrfHeader, string(token))
	resp = c.PostForm(t, "/changes", form)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "dev", <-changed)
}
