// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/GSI-HPC/go-clikit/progress/cliprogress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// activityState gives the test a state directory of its own, with neither
// SIND_PROGRESS_LOG set nor a display, and returns the activity log's path.
func activityState(t *testing.T) string {
	t.Helper()
	onTerminals(t, nil)
	require.NoError(t, os.Unsetenv(envProgressLog)) // t.Setenv restores it
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", home)
	return filepath.Join(home, "sind", "activity.jsonl")
}

// runCut runs the command whose docker commands all fail, as the tests of
// the event log do, with args before it, and returns its stderr.
func runCut(t *testing.T, args ...string) string {
	t.Helper()
	var stderr bytes.Buffer
	code := run(powerCutFails(t.Context(), t, nil), append(args, "power", "cut", "worker-0"), &stderr)
	assert.Equal(t, exitFailure, code)
	return stderr.String()
}

// Without --progress-log and SIND_PROGRESS_LOG, a command that reports
// progress writes its event log to activity.jsonl in the state directory,
// a file for its owner alone in a directory for its owner alone, and a
// second command appends to it. It changes nothing on stderr.
func TestTheDefaultActivityLog(t *testing.T) {
	log := activityState(t)
	t.Setenv(envProgressLog, "")
	baseline := runCut(t)

	require.NoError(t, os.Unsetenv(envProgressLog))
	stderr := runCut(t)

	assert.Equal(t, withoutTimestamps(baseline), withoutTimestamps(stderr), "the log changes nothing on stderr")
	info, err := os.Stat(log)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	info, err = os.Stat(filepath.Dir(log))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	lines := readJSONLines(t, log)
	require.GreaterOrEqual(t, len(lines), 3)
	assert.Equal(t, "trace", lines[0]["type"])
	assert.Equal(t, "sind", lines[0]["program"])
	assert.Equal(t, "power cut", lines[1]["name"])
	assert.Equal(t, "command", lines[1]["kind"])
	assert.Equal(t, "failed", lines[len(lines)-1]["status"])

	runCut(t)
	assert.Greater(t, len(readJSONLines(t, log)), len(lines), "a second command appends")
}

// A trace context in TRACEPARENT is continued by the default log, as by one
// that is named, so that a caller finds the run of the command it started,
// and docker is handed none.
func TestTheDefaultActivityLogContinuesTheTrace(t *testing.T) {
	log := activityState(t)
	t.Setenv("TRACEPARENT", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	var traceparent string
	var stderr bytes.Buffer

	code := run(powerCutFails(t.Context(), t, func() { traceparent = os.Getenv("TRACEPARENT") }),
		[]string{"power", "cut", "worker-0"}, &stderr)

	assert.Equal(t, exitFailure, code)
	assert.Empty(t, traceparent, "docker inherits no trace context")
	first := readJSONLines(t, log)[0]
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", first["trace"])
	assert.Equal(t, "b7ad6b7169203331", first["parent"])
}

// A flag or a variable that is given wins, and an empty one turns the log
// off: --progress-log "" and SIND_PROGRESS_LOG= write no log at all, and
// the flag wins over the variable, as it does for the log that is named.
func TestTheDefaultActivityLogIsOverridden(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  *string
		args []string
		log  func(dir string) string // where the log is, if anywhere
	}{
		{"the flag, empty", nil, []string{"--progress-log", ""}, nil},
		{"the variable, empty", ptr(""), nil, nil},
		{"the flag, empty, over the variable", ptr("{dir}/env.jsonl"), []string{"--progress-log", ""}, nil},
		{"the flag", nil, []string{"--progress-log", "{dir}/flag.jsonl"}, func(d string) string { return filepath.Join(d, "flag.jsonl") }},
		{"the variable", ptr("{dir}/env.jsonl"), nil, func(d string) string { return filepath.Join(d, "env.jsonl") }},
		{"the flag over the variable", ptr("{dir}/env.jsonl"), []string{"--progress-log", "{dir}/flag.jsonl"}, func(d string) string { return filepath.Join(d, "flag.jsonl") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := activityState(t)
			dir := t.TempDir()
			expand := func(s string) string { return string(bytes.ReplaceAll([]byte(s), []byte("{dir}"), []byte(dir))) }
			if tc.env != nil {
				t.Setenv(envProgressLog, expand(*tc.env))
			}
			var args []string
			for _, a := range tc.args {
				args = append(args, expand(a))
			}

			runCut(t, args...)

			assert.NoFileExists(t, log, "the default log is not written")
			assert.NoDirExists(t, filepath.Dir(log), "and its directory is not made")
			if tc.log != nil {
				assert.NotEmpty(t, readJSONLines(t, tc.log(dir)))
			}
		})
	}
}

func ptr(s string) *string { return &s }

// A log that cannot be prepared or used is left out without a word: stderr
// is what it is without one, and the command fails for its own reasons
// only. That holds for a state directory that cannot be made, a log that
// others can read, and a log that cannot be moved aside.
func TestTheDefaultActivityLogFailsNothing(t *testing.T) {
	log := activityState(t)
	t.Setenv(envProgressLog, "")
	baseline := withoutTimestamps(runCut(t))
	require.NoError(t, os.Unsetenv(envProgressLog))

	for name, setup := range map[string]func(t *testing.T){
		"no state directory": func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "file")
			require.NoError(t, os.WriteFile(home, nil, 0o600))
			t.Setenv("XDG_STATE_HOME", home)
		},
		"a log that others read": func(t *testing.T) {
			require.NoError(t, os.MkdirAll(filepath.Dir(log), 0o700))
			require.NoError(t, os.WriteFile(log, []byte("kept"), 0o644))
			require.NoError(t, os.Chmod(log, 0o644))
		},
		"a log that cannot be moved aside": func(t *testing.T) {
			require.NoError(t, os.MkdirAll(filepath.Dir(log), 0o700))
			require.NoError(t, os.WriteFile(log, nil, 0o600))
			require.NoError(t, os.Truncate(log, 8<<20+1))
			require.NoError(t, os.Mkdir(log+".lock", 0o700))
		},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			assert.Equal(t, baseline, withoutTimestamps(runCut(t)))
		})
	}
}

// A log that has grown past 8 MiB is moved to activity.jsonl.1 before the
// command appends to it.
func TestTheDefaultActivityLogIsRotated(t *testing.T) {
	log := activityState(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(log), 0o700))
	require.NoError(t, os.WriteFile(log, []byte("old\n"), 0o600))
	require.NoError(t, os.Truncate(log, 8<<20+1))

	runCut(t)

	rotated, err := os.Stat(log + ".1")
	require.NoError(t, err)
	assert.EqualValues(t, 8<<20+1, rotated.Size())
	assert.Equal(t, "trace", readJSONLines(t, log)[0]["type"], "the log is a new one")
}

// The notes of cliprogress that name the default log are dropped, the
// others are passed on: what the log says of itself is never the command's.
func TestDropNotesAbout(t *testing.T) {
	var out bytes.Buffer
	w := dropNotesAbout{w: &out, path: "/state/sind/activity.jsonl"}

	for _, note := range []string{
		"sind: the progress log /state/sind/activity.jsonl stops short: no space left on device\n",
		"sind: the default activity log names a progress log that cannot be used: x; no log is written\n",
	} {
		n, err := w.Write([]byte(note))
		require.NoError(t, err)
		assert.Equal(t, len(note), n)
	}
	assert.Empty(t, out.String())

	note := "sind: SIND_PROGRESS is \"plian\"; it takes one of auto; no progress is shown\n"
	n, err := w.Write([]byte(note))
	require.NoError(t, err)
	assert.Equal(t, len(note), n)
	assert.Equal(t, note, out.String())
}

// withActivityLog leaves the options alone for a flag or a variable that is
// given, and names the default log, as the one that the notes call it, when
// neither is.
func TestWithActivityLog(t *testing.T) {
	log := activityState(t)

	o := withActivityLog(t.Context(), cliprogress.Options{Stderr: io.Discard})
	assert.Equal(t, log, o.Log.Env)
	assert.Equal(t, activityLogName, o.Log.Variable)
	assert.NotNil(t, o.Notes)

	given := cliprogress.Options{Log: cliprogress.Setting{Given: true, Value: "x"}}
	assert.Equal(t, given, withActivityLog(t.Context(), given))
}
