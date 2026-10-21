// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appendTo appends b to the file at path, creating it.
func appendTo(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.Write(b)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func logLines(run, title string) []byte {
	var b bytes.Buffer
	b.Write(line("trace", run, `"trace":"t","program":"sind"`))
	b.WriteString("\n")
	b.Write(ev("start", run, "2026-01-01T00:00:00Z", `"span":"r","kind":"command","name":"`+title+`"`))
	b.WriteString("\n")
	b.Write(ev("end", run, "2026-01-01T00:00:01Z", `"span":"r","status":"ok"`))
	b.WriteString("\n")
	return b.Bytes()
}

// TestTailByteByByte writes each recorded log one byte at a time, polling
// after each, and holds that no line is lost, cut or read twice: the runs
// are those of the log read at once.
func TestTailByteByByte(t *testing.T) {
	for _, name := range []string{"golden-v1.jsonl", "create-ok.jsonl", "create-failed.jsonl", "create-canceled.jsonl", "interleaved.jsonl"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, name)
			path := filepath.Join(t.TempDir(), "activity.jsonl")
			red := NewReducer(50)
			tail := NewTail(path, red)
			assert.False(t, tail.Poll(), "a missing log is empty")

			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			require.NoError(t, err)
			defer func() { _ = f.Close() }()
			polls := 0
			for i := range data {
				_, err := f.Write(data[i : i+1])
				require.NoError(t, err)
				// The poll reports the bytes that end a line, no other.
				if got := tail.Poll(); got != (data[i] == '\n') {
					t.Fatalf("byte %d (%q): Poll() = %v", i, data[i], got)
				}
				polls++
			}
			assert.Equal(t, len(data), polls)
			want := reduce(t, name)
			assert.Equal(t, want, red.Runs())
			assert.NotEmpty(t, want)
			assert.False(t, tail.Poll(), "nothing new")
		})
	}
}

func TestTailMissingLogAndRotatedCopyFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.jsonl")

	// Only the rotated copy exists: its runs show on the first poll. A
	// last line cut off in it is no line.
	appendTo(t, path+".1", logLines(runA, "old"))
	appendTo(t, path+".1", []byte(`{"v":1,"type":"start","run":"zzzz`))
	red := NewReducer(10)
	tail := NewTail(path, red)
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"old"}, titles(red))
	assert.False(t, tail.Poll(), "the rotated copy is read once")

	// The log appears.
	appendTo(t, path, logLines("bbbbbbbbbbbbbbbb", "new"))
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"new", "old"}, titles(red))
}

func TestTailRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "activity.jsonl")
	appendTo(t, path, logLines(runA, "one"))
	red := NewReducer(10)
	tail := NewTail(path, red)
	require.True(t, tail.Poll())
	_ = red.Changed()
	// A line half written when the log is rotated is not carried over.
	appendTo(t, path, []byte(`{"v":1,"type":"sta`))
	require.False(t, tail.Poll())

	// The log becomes path.1, a new one starts.
	require.NoError(t, os.Rename(path, path+".1"))
	appendTo(t, path, logLines("bbbbbbbbbbbbbbbb", "two"))
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"two", "one"}, titles(red), "the old run from the rotated copy, once")
	assert.ElementsMatch(t, []string{runA, "bbbbbbbbbbbbbbbb"}, red.Changed())
	assert.False(t, tail.Poll())

	// The new log goes on from where it was.
	appendTo(t, path, logLines("cccccccccccccccc", "three"))
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"three", "two", "one"}, titles(red))
}

func TestTailTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	appendTo(t, path, logLines(runA, "one"))
	appendTo(t, path, logLines("bbbbbbbbbbbbbbbb", "two"))
	red := NewReducer(10)
	tail := NewTail(path, red)
	require.True(t, tail.Poll())
	require.Equal(t, []string{"two", "one"}, titles(red))

	// The same file, shorter than what was read: the log starts over, and
	// the runs of before, which are not in it, go.
	require.NoError(t, os.Truncate(path, 0))
	appendTo(t, path, logLines("cccccccccccccccc", "three"))
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"three"}, titles(red))
}

func TestTailLogRemovedAndCreatedAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	appendTo(t, path, logLines(runA, "one"))
	red := NewReducer(10)
	tail := NewTail(path, red)
	require.True(t, tail.Poll())
	require.NoError(t, os.Remove(path))
	assert.False(t, tail.Poll(), "gone, nothing is read, nothing is forgotten")
	assert.Equal(t, []string{"one"}, titles(red))
	appendTo(t, path, logLines("bbbbbbbbbbbbbbbb", "two"))
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"two"}, titles(red))
}

func TestTailLogMadeAgainInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	appendTo(t, path, logLines(runA, "one"))
	red := NewReducer(10)
	tail := NewTail(path, red)
	require.True(t, tail.Poll())

	// The same file, as long as before: only its start tells.
	require.NoError(t, os.WriteFile(path, logLines("bbbbbbbbbbbbbbbb", "two"), 0o600))
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"two"}, titles(red))
}

func TestTailLongLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity.jsonl")
	red := NewReducer(10)
	tail := NewTail(path, red)

	// A line longer than the buffer of the reader, still a line.
	big := string(ev("start", runA, "2026-01-01T00:00:00Z", `"span":"r","kind":"command","name":"big","message":"`+strings.Repeat("x", 200<<10)+`"`))
	appendTo(t, path, []byte(big+"\n"))
	require.True(t, tail.Poll())
	require.Len(t, red.Runs(), 1)
	assert.Len(t, red.Runs()[0].Root.Message, 200<<10)

	// A line over the limit is left out, whole, as it is written in
	// pieces over several polls, and the line after it is read.
	huge := strings.Repeat("y", maxLine+100)
	appendTo(t, path, []byte(`{"v":1,"type":"start","run":"bbbbbbbbbbbbbbbb","time":"2026-01-01T00:00:00Z","span":"r","kind":"command","name":"huge","message":"`))
	assert.False(t, tail.Poll())
	appendTo(t, path, []byte(huge))
	assert.False(t, tail.Poll())
	appendTo(t, path, []byte(huge))
	assert.False(t, tail.Poll())
	appendTo(t, path, []byte("\"}\n"))
	assert.False(t, tail.Poll(), "left out")
	assert.Equal(t, []string{"big"}, titles(red))
	appendTo(t, path, logLines("cccccccccccccccc", "after"))
	assert.True(t, tail.Poll())
	assert.Equal(t, []string{"after", "big"}, titles(red))
}

func TestTailUnreadableLog(t *testing.T) {
	red := NewReducer(10)
	// A directory opens but cannot be read.
	assert.False(t, NewTail(t.TempDir(), red).Poll())
	// A path below a file is not a missing log either.
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	assert.False(t, NewTail(filepath.Join(file, "activity.jsonl"), red).Poll())
	assert.Empty(t, red.Runs())
}
