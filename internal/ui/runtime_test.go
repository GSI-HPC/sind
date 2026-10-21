// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestInstance(t *testing.T) *instance {
	t.Helper()
	i, err := newInstance(t.TempDir())
	require.NoError(t, err)
	return i
}

func writeRecordFile(t *testing.T, i *instance, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(i.recordPath(), []byte(content), 0o600))
}

func TestNewInstanceCreatesAPrivateDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	i, err := newInstance(runtimeDir)
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(runtimeDir, "sind"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.Equal(t, filepath.Join(runtimeDir, "sind", "ui.json"), i.recordPath())

	_, err = newInstance(runtimeDir) // again, with the directory there
	assert.NoError(t, err)

	file := filepath.Join(runtimeDir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	_, err = newInstance(file)
	assert.Error(t, err)
}

func TestNewInstanceRefusesADirectoryThatIsNotPrivate(t *testing.T) {
	open := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(open, "sind"), 0o700))
	require.NoError(t, os.Chmod(filepath.Join(open, "sind"), 0o755))
	_, err := newInstance(open)
	assert.ErrorContains(t, err, "has mode 0755; it must be 0700")

	linked := t.TempDir()
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(linked, "sind")))
	_, err = newInstance(linked)
	assert.ErrorContains(t, err, "is no directory", "a link to a directory is none")

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	assert.NoError(t, checkPrivateDir(dir, os.Getuid()))
	assert.ErrorContains(t, checkPrivateDir(dir, os.Getuid()+1), "belongs to another user")
	assert.ErrorIs(t, checkPrivateDir(filepath.Join(dir, "gone"), os.Getuid()), os.ErrNotExist)
}

func TestInstanceLockSerializesStarts(t *testing.T) {
	dir := t.TempDir()
	first, err := newInstance(dir)
	require.NoError(t, err)
	second, err := newInstance(dir)
	require.NoError(t, err)

	require.NoError(t, first.lock())
	info, err := os.Stat(filepath.Join(first.dir, "ui.lock"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got := make(chan error, 1)
	go func() { got <- second.lock() }()
	select {
	case err := <-got:
		t.Fatalf("the second start got the lock (%v) while the first held it", err)
	case <-time.After(100 * time.Millisecond):
	}
	first.unlock()
	select {
	case err := <-got:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("the second start did not get the lock")
	}
	second.unlock()
	second.unlock() // without a lock: nothing happens
}

func TestInstanceLockErrors(t *testing.T) {
	i := newTestInstance(t)
	i.flock = func(int, int) error { return errors.New("no locks here") }
	assert.ErrorContains(t, i.lock(), "no locks here")
	assert.Nil(t, i.lockf, "the file is closed again")

	require.NoError(t, os.RemoveAll(i.dir))
	i2 := &instance{dir: i.dir, flock: i.flock}
	assert.ErrorIs(t, i2.lock(), os.ErrNotExist)
}

func TestInstanceRunning(t *testing.T) {
	i := newTestInstance(t)
	_, ok := i.running()
	assert.False(t, ok, "no record")

	l, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	live := `{"url":"http://` + l.Addr().String() + `/?token=t","pid":` + strconv.Itoa(os.Getpid()) + `}`
	writeRecordFile(t, i, live)
	r, ok := i.running()
	assert.True(t, ok)
	assert.Equal(t, os.Getpid(), r.PID)
	assert.Contains(t, r.URL, "token=t")

	// Another user listens at the URL: our sind ui has gone, and its port
	// was taken.
	port := l.Addr().(*net.TCPAddr).Port
	i.procRoot = fakeProcNetTCP(t, fmt.Sprintf(
		"   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000  4242        0 101 1\n", port))
	_, ok = i.running()
	assert.False(t, ok, "another user's listener")
	i.procRoot = t.TempDir()
	_, ok = i.running()
	assert.False(t, ok, "no table to name the listener's owner")
	i.procRoot = "/proc"

	// Our own process, but nothing listens at the URL any more.
	require.NoError(t, l.Close())
	_, ok = i.running()
	assert.False(t, ok, "nothing listens")

	// A process that is gone: the PID may be another process's by now.
	writeRecordFile(t, i, `{"url":"http://127.0.0.1:1/","pid":`+strconv.Itoa(math.MaxInt32)+`}`)
	_, ok = i.running()
	assert.False(t, ok, "no such process")

	writeRecordFile(t, i, `{"url":"http://%zz/","pid":`+strconv.Itoa(os.Getpid())+`}`)
	_, ok = i.running()
	assert.False(t, ok, "unparsable URL")

	writeRecordFile(t, i, `{"url":"http://localhost:1/","pid":`+strconv.Itoa(os.Getpid())+`}`)
	_, ok = i.running()
	assert.False(t, ok, "no address")
}

// fakeProcNetTCP returns a /proc whose net/tcp has rows below its header.
func fakeProcNetTCP(t *testing.T, rows string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "net"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "net", "tcp"), []byte("header\n"+rows), 0o600))
	return root
}

func TestReadRecord(t *testing.T) {
	i := newTestInstance(t)
	for name, content := range map[string]string{
		"not json":   "nope",
		"no pid":     `{"url":"http://127.0.0.1:1/"}`,
		"zero pid":   `{"url":"http://127.0.0.1:1/","pid":0}`,
		"negative":   `{"url":"http://127.0.0.1:1/","pid":-4}`,
		"no url":     `{"pid":4}`,
		"empty file": "",
	} {
		writeRecordFile(t, i, content)
		_, ok := readRecord(i.recordPath())
		assert.False(t, ok, name)
	}
	writeRecordFile(t, i, `{"url":"http://127.0.0.1:1/","pid":4}`)
	r, ok := readRecord(i.recordPath())
	assert.True(t, ok)
	assert.Equal(t, runtimeRecord{URL: "http://127.0.0.1:1/", PID: 4}, r)
}

func TestInstancePublishAndRetire(t *testing.T) {
	i := newTestInstance(t)
	require.NoError(t, i.publish(runtimeRecord{URL: "http://127.0.0.1:1/?token=t", PID: os.Getpid()}))
	info, err := os.Stat(i.recordPath())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	data, err := os.ReadFile(i.recordPath())
	require.NoError(t, err)
	assert.JSONEq(t, `{"url":"http://127.0.0.1:1/?token=t","pid":`+strconv.Itoa(os.Getpid())+`}`, string(data))

	// The lock is dropped when retire returns, so a second call can take it.
	i.retire()
	assert.NoFileExists(t, i.recordPath())
	i.retire()

	// A record of another sind ui stays.
	require.NoError(t, i.publish(runtimeRecord{URL: "http://127.0.0.1:1/", PID: os.Getpid() + 1}))
	i.retire()
	assert.FileExists(t, i.recordPath())

	// Without the lock nothing is touched.
	require.NoError(t, i.publish(runtimeRecord{URL: "http://127.0.0.1:1/", PID: os.Getpid()}))
	i.flock = func(int, int) error { return errors.New("no locks here") }
	i.retire()
	assert.FileExists(t, i.recordPath())
}

func TestInstancePublishFails(t *testing.T) {
	i := newTestInstance(t)
	require.NoError(t, os.MkdirAll(filepath.Join(i.recordPath(), "inside"), 0o700))
	assert.Error(t, i.publish(runtimeRecord{URL: "http://127.0.0.1:1/", PID: 1}))
}

func TestWriteOpenFile(t *testing.T) {
	dir := t.TempDir()
	path, err := writeOpenFile(dir, "http://127.0.0.1:7878/?token=a-b_c")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "ui-open.html"), path)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `content="0;url=http://127.0.0.1:7878/?token=a-b_c"`)
	assert.Contains(t, string(data), `<a href="http://127.0.0.1:7878/?token=a-b_c">Open sind ui</a>`)

	// The page is served from the file, so the file is the page: no script.
	assert.NotContains(t, string(data), "<script")

	require.NoError(t, os.RemoveAll(dir))
	_, err = writeOpenFile(dir, "http://127.0.0.1:7878/")
	assert.Error(t, err)
}

func TestOpenBrowser(t *testing.T) {
	url := "http://127.0.0.1:7878/?token=secret"
	t.Run("through a private file", func(t *testing.T) {
		dir := t.TempDir()
		var args []string
		m := &mock.Executor{OnCall: func(a []string, _ string) mock.Result { args = a; return mock.Result{} }}
		<-openBrowser(context.Background(), m, dir, url, discardLog())
		require.Len(t, m.Calls, 1)
		assert.Equal(t, "xdg-open", m.Calls[0].Name)
		assert.Equal(t, []string{filepath.Join(dir, "ui-open.html")}, args)
		assert.NotContains(t, strings.Join(args, " "), "secret", "the token is in no command line")
	})
	t.Run("no page, no browser", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "gone") // the file cannot be written
		var logs strings.Builder
		log := slog.New(slog.NewTextHandler(&logs, nil))
		m := &mock.Executor{}
		<-openBrowser(context.Background(), m, dir, url, log)
		assert.Empty(t, m.Calls, "the URL never stands in a command line")
		assert.Contains(t, logs.String(), "could not write the page")
	})
	t.Run("failures are only logged", func(t *testing.T) {
		var logs strings.Builder
		log := slog.New(slog.NewTextHandler(&logs, nil))
		m := &mock.Executor{OnCall: func([]string, string) mock.Result { return mock.Result{Err: errors.New("no xdg-open")} }}
		<-openBrowser(context.Background(), m, t.TempDir(), url, log)
		require.Len(t, m.Calls, 1)
		assert.Contains(t, logs.String(), "could not open a browser")
	})
	t.Run("outlives the context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		m := &mock.Executor{OnCall: func([]string, string) mock.Result { return mock.Result{} }}
		cancel()
		<-openBrowser(ctx, m, t.TempDir(), url, discardLog())
		assert.Len(t, m.Calls, 1)
	})
}
