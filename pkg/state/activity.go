// SPDX-License-Identifier: LGPL-3.0-or-later

package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// ActivityLogName is the name of the activity log, the event log that the
// commands of sind that report progress append to when no flag or variable
// names another, in sind's state directory.
const ActivityLogName = "activity.jsonl"

// The seams of PrepareActivityLog, which tests replace: the flock(2) that
// orders the commands that rotate the log, os.Rename, the user that must
// own the log, and the stat of the log once it is open.
var (
	flock    = unix.Flock
	rename   = os.Rename
	euid     = os.Geteuid
	statOpen = (*os.File).Stat
)

// activityLogLimit is the size above which PrepareActivityLog moves the
// activity log aside, so that it stays under about twice this size: one
// old copy is kept.
const activityLogLimit = 8 << 20

// ActivityLogPath returns the path of the activity log, Dir()/activity.jsonl.
func ActivityLogPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ActivityLogName), nil
}

// PrepareActivityLog makes the activity log ready for a command to append
// to, and returns its path: it creates the state directory, readable by its
// owner alone (mode 0700), moves a log larger than 8 MiB aside to
// activity.jsonl.1, replacing the copy before, and creates the log, in the
// mode 0600, if it is not there. It fails, and the caller goes on without a
// log, when the directory or the file cannot be used: a read-only home, or a
// file that is a link, is another user's or can be read or written by
// others than its owner. The log is a convenience, so no caller makes a
// command fail for the error.
func PrepareActivityLog() (string, error) {
	path, err := ActivityLogPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// A directory that is not this user's keeps its mode; the log's own
	// mode, which checkLog holds to, is what keeps it private.
	_ = os.Chmod(dir, 0o700)
	if err := rotateLog(path, activityLogLimit); err != nil {
		return "", err
	}
	return path, checkLog(path)
}

// rotateLog moves the log at path to path+".1", replacing an older copy,
// when it is larger than limit bytes. Commands that run at once rotate it
// once, as each takes the lock of path+".lock" and looks again, and one
// that has the log open goes on writing to the old copy.
func rotateLog(path string, limit int64) error {
	if fi, err := os.Stat(path); err != nil || fi.Size() <= limit {
		return nil // missing, or small enough
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	// Another command may have rotated it while this one waited.
	if fi, err := os.Stat(path); err != nil || fi.Size() <= limit {
		return nil
	}
	if err := rename(path, path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// checkLog creates the log at path in the mode 0600 if it is not there, and
// fails for one that is not a regular file of this user's, with no other
// name, that nobody else can read or write, as an event log has to be (the
// events name nodes and say why they failed), without following a link at
// its name. It opens without blocking, so that a named pipe put at the
// name without a reader fails at once.
func checkLog(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fi, err := statOpen(f)
	if err != nil {
		return err
	}
	st := fi.Sys().(*syscall.Stat_t)
	switch {
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	case int(st.Uid) != euid():
		return fmt.Errorf("%s is not owned by this user", path)
	case st.Nlink > 1:
		return fmt.Errorf("%s has other names than this one", path)
	case fi.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("%s can be read or written by others than its owner", path)
	}
	return nil
}
