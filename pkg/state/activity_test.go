// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// stateHome points the state directory at a temporary one, never the real
// home, and returns sind's directory in it.
func stateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", home)
	return filepath.Join(home, "sind")
}

// grow makes the file at path size bytes long, with holes.
func grow(t *testing.T, path string, size int64) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))
	require.NoError(t, os.Truncate(path, size))
}

func TestActivityLogPath(t *testing.T) {
	dir := stateHome(t)
	path, err := ActivityLogPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "activity.jsonl"), path)
	assert.Equal(t, "activity.jsonl", ActivityLogName)

	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	_, err = ActivityLogPath()
	require.Error(t, err)
	_, err = PrepareActivityLog()
	require.Error(t, err)
}

// The directory is made private and the log is made in the mode 0600, and
// both are left as they are the next time, with what is in the log.
func TestPrepareActivityLog_CreatesThePrivateLog(t *testing.T) {
	dir := stateHome(t)

	path, err := PrepareActivityLog()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "activity.jsonl"), path)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	info, err = os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))
	_, err = PrepareActivityLog()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(data))
}

// A state directory that others can enter is made private, as brig does.
func TestPrepareActivityLog_TightensTheDirectory(t *testing.T) {
	dir := stateHome(t)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.Chmod(dir, 0o755))

	_, err := PrepareActivityLog()

	require.NoError(t, err)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

// A log larger than 8 MiB is moved to activity.jsonl.1, replacing the copy
// before it, and a new one is made; one at the limit stays.
func TestPrepareActivityLog_RotatesAtTheLimit(t *testing.T) {
	dir := stateHome(t)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "activity.jsonl")

	grow(t, path, activityLogLimit)
	_, err := PrepareActivityLog()
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.EqualValues(t, activityLogLimit, info.Size(), "8 MiB is not over the limit")
	assert.NoFileExists(t, path+".1")

	grow(t, path, activityLogLimit+1)
	require.NoError(t, os.WriteFile(path+".1", []byte("older"), 0o600))
	_, err = PrepareActivityLog()
	require.NoError(t, err)
	info, err = os.Stat(path)
	require.NoError(t, err)
	assert.Zero(t, info.Size(), "a new log")
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	rotated, err := os.Stat(path + ".1")
	require.NoError(t, err)
	assert.EqualValues(t, activityLogLimit+1, rotated.Size(), "the log that was there, in place of the older copy")
}

func TestRotateLog(t *testing.T) {
	var path string
	seams := func(t *testing.T) {
		savedFlock, savedRename := flock, rename
		t.Cleanup(func() { flock, rename = savedFlock, savedRename })
	}

	t.Run("missing", func(t *testing.T) {
		path = filepath.Join(t.TempDir(), "log")
		require.NoError(t, rotateLog(path, 10))
		assert.NoFileExists(t, path+".lock", "nothing is locked for a log that is not there")
	})
	t.Run("small enough", func(t *testing.T) {
		path = filepath.Join(t.TempDir(), "log")
		require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o600))
		require.NoError(t, rotateLog(path, 10))
		assert.FileExists(t, path)
		assert.NoFileExists(t, path+".1")
	})
	t.Run("another command rotated it while this one waited", func(t *testing.T) {
		path = filepath.Join(t.TempDir(), "log")
		seams(t)
		require.NoError(t, os.WriteFile(path, []byte("0123456789x"), 0o600))
		flock = func(_, how int) error {
			require.Equal(t, unix.LOCK_EX, how)
			return os.WriteFile(path, nil, 0o600)
		}
		require.NoError(t, rotateLog(path, 10))
		assert.NoFileExists(t, path+".1", "the log is the other command's new one")
	})
	t.Run("gone before the rename", func(t *testing.T) {
		path = filepath.Join(t.TempDir(), "log")
		seams(t)
		require.NoError(t, os.WriteFile(path, []byte("0123456789x"), 0o600))
		rename = func(string, string) error { return os.ErrNotExist }
		assert.NoError(t, rotateLog(path, 10))
	})
	t.Run("the rename fails", func(t *testing.T) {
		path = filepath.Join(t.TempDir(), "log")
		seams(t)
		require.NoError(t, os.WriteFile(path, []byte("0123456789x"), 0o600))
		rename = func(string, string) error { return errors.New("read-only file system") }
		assert.EqualError(t, rotateLog(path, 10), "read-only file system")
	})
	t.Run("the lock fails", func(t *testing.T) {
		path = filepath.Join(t.TempDir(), "log")
		seams(t)
		require.NoError(t, os.WriteFile(path, []byte("0123456789x"), 0o600))
		flock = func(int, int) error { return errors.New("no locks") }
		assert.EqualError(t, rotateLog(path, 10), "no locks")
	})
	t.Run("the lock file cannot be opened", func(t *testing.T) {
		path = filepath.Join(t.TempDir(), "log")
		require.NoError(t, os.WriteFile(path, []byte("0123456789x"), 0o600))
		require.NoError(t, os.Mkdir(path+".lock", 0o700))
		assert.Error(t, rotateLog(path, 10))
	})
}

// A log that cannot be used is an error, which the caller goes on without:
// a path that is no file, a link, a file that others can read or write, one
// with another name or another owner, a named pipe, a directory that cannot
// be made, and a log that cannot be moved aside.
func TestPrepareActivityLog_RefusesWhatCannotBeUsed(t *testing.T) {
	logOf := func(t *testing.T) (dir, path string) {
		dir = stateHome(t)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		return dir, filepath.Join(dir, "activity.jsonl")
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir, path string)
		match string
	}{
		{"a directory", func(t *testing.T, _, path string) { require.NoError(t, os.Mkdir(path, 0o700)) }, ""},
		{"a link", func(t *testing.T, dir, path string) {
			target := filepath.Join(dir, "elsewhere")
			require.NoError(t, os.WriteFile(target, nil, 0o600))
			require.NoError(t, os.Symlink(target, path))
		}, ""},
		{"readable by others", func(t *testing.T, _, path string) {
			require.NoError(t, os.WriteFile(path, nil, 0o644))
			require.NoError(t, os.Chmod(path, 0o644))
		}, "can be read or written by others than its owner"},
		{"writable by others", func(t *testing.T, _, path string) {
			require.NoError(t, os.WriteFile(path, nil, 0o600))
			require.NoError(t, os.Chmod(path, 0o620))
		}, "can be read or written by others than its owner"},
		{"another name", func(t *testing.T, dir, path string) {
			require.NoError(t, os.WriteFile(path, nil, 0o600))
			require.NoError(t, os.Link(path, filepath.Join(dir, "other-name")))
		}, "has other names than this one"},
		{"another owner", func(t *testing.T, _, path string) {
			require.NoError(t, os.WriteFile(path, nil, 0o600))
			saved := euid
			t.Cleanup(func() { euid = saved })
			euid = func() int { return os.Geteuid() + 1 }
		}, "is not owned by this user"},
		{"a named pipe without a reader", func(t *testing.T, _, path string) {
			require.NoError(t, unix.Mkfifo(path, 0o600))
		}, ""},
		{"a log that cannot be moved aside", func(t *testing.T, _, path string) {
			grow(t, path, activityLogLimit+1)
			require.NoError(t, os.Mkdir(path+".lock", 0o700))
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, path := logOf(t)
			tc.setup(t, dir, path)

			_, err := PrepareActivityLog()

			require.Error(t, err)
			if tc.match != "" {
				assert.ErrorContains(t, err, tc.match)
			}
		})
	}

	t.Run("a named pipe with a reader", func(t *testing.T) {
		_, path := logOf(t)
		require.NoError(t, unix.Mkfifo(path, 0o600))
		reader, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
		require.NoError(t, err)
		defer func() { _ = reader.Close() }()

		_, err = PrepareActivityLog()

		assert.EqualError(t, err, path+" is not a regular file")
	})
	t.Run("a log that cannot be inspected", func(t *testing.T) {
		logOf(t)
		saved := statOpen
		t.Cleanup(func() { statOpen = saved })
		statOpen = func(*os.File) (os.FileInfo, error) { return nil, errors.New("stale handle") }

		_, err := PrepareActivityLog()

		assert.EqualError(t, err, "stale handle")
	})
	t.Run("a directory that cannot be made", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(home, "sind"), nil, 0o600))
		t.Setenv("XDG_STATE_HOME", home)

		_, err := PrepareActivityLog()

		assert.Error(t, err)
	})
}
