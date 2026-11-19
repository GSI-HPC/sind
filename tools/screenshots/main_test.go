// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSind writes a sind that runs script with sh, and returns its path.
func fakeSind(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sind")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil { //nolint:gosec // G306: a test double
		t.Fatal(err)
	}
	return path
}

// syncBuffer is a buffer that the reader of sind ui's output and a test
// may use at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestStartUI_Fails checks that startUI returns as soon as sind ui ends
// without its URL, with its exit status, and passes on what it printed.
func TestStartUI_Fails(t *testing.T) {
	sind := fakeSind(t, "echo 'sind: listen tcp 127.0.0.1:8080: bind: address already in use' >&2\nexit 1\n")
	var log syncBuffer
	start := time.Now()
	_, err := startUI(context.Background(), sind, 8080, &log)
	if err == nil || !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("the error is %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("startUI waited for its timeout")
	}
	if !strings.Contains(log.String(), "address already in use") {
		t.Errorf("the log is %q", log.String())
	}

	sind = fakeSind(t, "echo 'no URL' >&2\n")
	if _, err := startUI(context.Background(), sind, 8080, &log); err == nil || !strings.Contains(err.Error(), "printed no URL and ended") {
		t.Errorf("the error of a sind that exits 0 is %v", err)
	}
}

// TestStartUI_URL checks that startUI returns the URL, and passes on every
// other line, before the URL and after it.
func TestStartUI_URL(t *testing.T) {
	sind := fakeSind(t, "printf 'starting\\nsind ui: http://127.0.0.1:1/?token=x\\nPress Ctrl-C to stop.\\n' >&2\nexec sleep 30\n")
	var log syncBuffer
	u, err := startUI(context.Background(), sind, 1, &log)
	if err != nil {
		t.Fatal(err)
	}
	defer u.stop()
	if u.url != "http://127.0.0.1:1/?token=x" {
		t.Errorf("the URL is %q", u.url)
	}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(log.String(), "Press Ctrl-C") && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if got := log.String(); got != "starting\nPress Ctrl-C to stop.\n" {
		t.Errorf("the log is %q", got)
	}

	// A sind that ends right after its URL still gives it.
	sind = fakeSind(t, "echo 'sind ui: http://127.0.0.1:2/' >&2\n")
	u, err = startUI(context.Background(), sind, 2, &log)
	if err != nil {
		t.Fatal(err)
	}
	u.stop()
	if u.url != "http://127.0.0.1:2/" {
		t.Errorf("the URL is %q", u.url)
	}
}
