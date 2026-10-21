// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"slices"
)

// maxLine is the longest log line read; longer ones are left out.
const maxLine = 1 << 20

// Tail reads an event log as it grows, and from its start again after it
// was rotated or truncated.
type Tail struct {
	path    string
	red     *Reducer
	info    os.FileInfo
	off     int64
	partial []byte
	// head is the start of the log as it was read, which a log that was
	// made again, at the same inode and as long as before, does not share.
	head    []byte
	started bool
	// old is the rotated copy, path.1, as it was read. A command that had
	// the log open when another rotated it goes on writing to the copy.
	old oldCopy
}

// oldCopy is where Tail stands in the rotated copy of the log.
type oldCopy struct {
	info    os.FileInfo
	off     int64
	partial []byte
}

// headLen is how much of the start of the log Tail remembers.
const headLen = 128

// NewTail returns a reader of the log at path that feeds red.
func NewTail(path string, red *Reducer) *Tail {
	return &Tail{path: path, red: red}
}

// Poll feeds red with the complete lines written since the last poll, and
// keeps the unfinished last line for the next. The first poll reads the
// rotated copy of the log, path.1, first, so that the runs before the last
// rotation count too. Later polls read what was written to the copy since,
// as long as it is the same file, so that a run that was going on when the
// log was rotated still ends.
//
// A log that is another file than before, or shorter, or that starts with
// other bytes, was rotated or truncated: Poll resets red, then reads the rotated copy and the new file
// from their starts, as it did on its first poll. A missing log is an empty
// one.
//
// Poll reports whether it fed red any line or reset it.
func (t *Tail) Poll() bool {
	first := !t.started
	t.started = true
	// A nil file is closed without harm, so that one defer serves a log
	// that is missing too.
	f, err := os.Open(t.path)
	defer func() { _ = f.Close() }()
	var info os.FileInfo
	if err == nil {
		info, err = f.Stat()
	}
	rotated := err == nil && t.info != nil &&
		(!os.SameFile(t.info, info) || info.Size() < t.off || !t.sameHead(f))
	changed := rotated
	if first || rotated {
		if rotated {
			t.red.Reset()
		}
		t.off, t.partial, t.head, t.old = 0, nil, nil, oldCopy{}
	}
	changed = t.readOld() || changed
	if err != nil {
		return changed
	}
	t.info = info
	n, rest, lines := readLines(io.NewSectionReader(f, t.off, math.MaxInt64-t.off), t.red, t.partial)
	t.off += n
	t.partial = rest
	if want := int(min(t.off, headLen)); want > len(t.head) {
		head := make([]byte, want)
		got, _ := f.ReadAt(head, 0)
		t.head = head[:got]
	}
	return changed || lines > 0
}

// sameHead reports whether f starts with the bytes the log started with.
func (t *Tail) sameHead(f *os.File) bool {
	head := make([]byte, len(t.head))
	n, _ := f.ReadAt(head, 0)
	return bytes.Equal(head[:n], t.head)
}

// readOld reads the rotated copy of the log: from its start the first time,
// and then what was written to it since, while it is the file it was. It
// reports whether it fed red a line. A last line without its end is not one
// yet.
func (t *Tail) readOld() bool {
	f, err := os.Open(t.path + ".1")
	defer func() { _ = f.Close() }()
	var info os.FileInfo
	if err == nil {
		info, err = f.Stat()
	}
	if err != nil || t.old.info != nil && !os.SameFile(t.old.info, info) {
		// Gone, or replaced: a copy that replaces it was the log, which
		// a later poll finds rotated and reads again.
		return false
	}
	t.old.info = info
	n, rest, lines := readLines(io.NewSectionReader(f, t.old.off, math.MaxInt64-t.old.off), t.red, t.old.partial)
	t.old.off += n
	t.old.partial = rest
	return lines > 0
}

// readLines feeds the whole lines of r, after partial, to red. It returns
// the bytes read, the unfinished last line and how many lines it fed.
func readLines(r io.Reader, red *Reducer, partial []byte) (n int64, rest []byte, lines int) {
	br := bufio.NewReaderSize(r, 64<<10)
	buf := slices.Clone(partial)
	for {
		chunk, err := br.ReadSlice('\n')
		n += int64(len(chunk))
		if len(buf) <= maxLine {
			buf = append(buf, chunk...)
		}
		switch {
		case err == nil:
			if len(buf) <= maxLine {
				red.Line(bytes.TrimSpace(buf))
				lines++
			}
			buf = buf[:0]
		case errors.Is(err, bufio.ErrBufferFull):
		default:
			// The end of the file, or of what could be read: what is left
			// is a line still being written.
			return n, buf, lines
		}
	}
}
