// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net"
	neturl "net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/GSI-HPC/sind/internal/atomicfile"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/spf13/afero"
	"golang.org/x/sys/unix"
)

// runtimeRecord is what $XDG_RUNTIME_DIR/sind/ui.json holds.
type runtimeRecord struct {
	URL string `json:"url"`
	PID int    `json:"pid"`
}

// instance is the single-instance guard of a user: the directory
// $XDG_RUNTIME_DIR/sind, the lock file in it that serializes starts, and
// the record of the sind ui that runs.
type instance struct {
	dir string
	// flock takes or drops the lock on a file descriptor.
	flock func(fd, how int) error
	lockf *os.File
}

// newInstance returns the guard that lives in runtimeDir, and creates the
// directory it uses, for the user alone. The files in it are private too.
func newInstance(runtimeDir string) (*instance, error) {
	dir := filepath.Join(runtimeDir, "sind")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &instance{dir: dir, flock: unix.Flock}, nil
}

func (i *instance) recordPath() string { return filepath.Join(i.dir, "ui.json") }

// lock waits for the lock that two starts of sind ui take in turn, so that
// they cannot both find no sind ui running and both start one.
func (i *instance) lock() error {
	f, err := os.OpenFile(filepath.Join(i.dir, "ui.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := i.flock(int(f.Fd()), unix.LOCK_EX); err != nil { //nolint:gosec // G115: a file descriptor fits an int
		_ = f.Close()
		return err
	}
	i.lockf = f
	return nil
}

// unlock drops the lock; closing the file does. It does nothing without a
// lock.
func (i *instance) unlock() {
	if i.lockf != nil {
		_ = i.lockf.Close()
		i.lockf = nil
	}
}

// running returns the record of a sind ui that still runs.
func (i *instance) running() (runtimeRecord, bool) {
	r, ok := readRecord(i.recordPath())
	if !ok {
		return r, false
	}
	// Signal 0 checks that the process exists and is ours; a PID used
	// again by another process does not listen at the URL.
	if syscall.Kill(r.PID, 0) != nil {
		return r, false
	}
	u, err := neturl.Parse(r.URL)
	if err != nil {
		return r, false
	}
	c, err := net.DialTimeout("tcp4", u.Host, time.Second)
	if err != nil {
		return r, false
	}
	_ = c.Close()
	return r, true
}

// readRecord reads the record at path.
func readRecord(path string) (runtimeRecord, bool) {
	var r runtimeRecord
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &r) != nil || r.PID <= 0 || r.URL == "" {
		return r, false
	}
	return r, true
}

// publish writes the record of the sind ui that runs, readable only by the
// user.
func (i *instance) publish(r runtimeRecord) error {
	data, _ := json.Marshal(r) // two plain fields: nothing to fail
	return atomicfile.Write(afero.NewOsFs(), i.recordPath(), append(data, '\n'), 0o600)
}

// retire removes the record when it is ours, under the lock.
func (i *instance) retire() {
	if i.lock() != nil {
		return
	}
	defer i.unlock()
	if r, ok := readRecord(i.recordPath()); ok && r.PID == os.Getpid() {
		_ = os.Remove(i.recordPath())
	}
}

// openTemplate is a page that sends the browser on to the URL. The browser
// opens this file rather than the URL itself, so that the token never
// stands in a command line, which other users can read in /proc.
var openTemplate = template.Must(template.New("").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url={{.}}"><title>sind ui</title></head>
<body><p><a href="{{.}}">Open sind ui</a></p></body></html>
`))

// writeOpenFile writes the page that opens url, readable only by the user.
func (i *instance) writeOpenFile(url string) (string, error) {
	var buf bytes.Buffer
	// The template is static and url a string, and a buffer takes any write.
	_ = openTemplate.Execute(&buf, template.URL(url)) //nolint:gosec // G203: our own URL
	path := filepath.Join(i.dir, "ui-open.html")
	return path, atomicfile.Write(afero.NewOsFs(), path, buf.Bytes(), 0o600)
}

// openBrowser opens url in the user's browser, through a private file
// when there is a guard to put it in. xdg-open can stay until the browser
// ends, so it runs on its own; the channel closes when it has returned. A
// failure is only logged: the URL is printed anyway.
func openBrowser(ctx context.Context, exec cmdexec.Executor, i *instance, url string, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	target := url
	if i != nil {
		path, err := i.writeOpenFile(url)
		if err != nil {
			log.Warn("could not write the page that opens sind ui", "err", err)
		} else {
			target = path
		}
	}
	// The browser outlives sind ui's own shutdown.
	ctx = context.WithoutCancel(ctx)
	go func() {
		defer close(done)
		if _, _, err := exec.Run(ctx, "xdg-open", target); err != nil {
			log.Warn("could not open a browser; open the URL above yourself", "err", err)
		}
	}()
	return done
}
