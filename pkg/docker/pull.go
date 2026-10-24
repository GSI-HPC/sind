// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"regexp"
	"strings"
	"sync"

	"github.com/GSI-HPC/go-clikit/progress"
)

// layerLine matches a line of the status docker pull writes where it is no
// terminal, "<layer>: <status>", for the layer's short ID: 12 or more
// hexadecimal digits. docker prints no byte counts there, only these
// changes of state, so a pull is counted in layers, not in bytes. A line
// that is none, such as "latest: Pulling from library/busybox",
// "Digest: sha256:..." or "Status: Downloaded newer image", names no layer.
var layerLine = regexp.MustCompile(`^([0-9a-f]{12,64}): (.+)$`)

// layerStatuses are the statuses of a layer that docker pull writes, and
// whether the layer is done with it: "Pull complete" says it was extracted,
// "Already exists" that the daemon had it. Every other status of a layer,
// including "Download complete", which extraction follows, only says that
// docker knows of it. A status that is not here is ignored, lines of
// layers included, as docker may write others in releases that come.
var layerStatuses = map[string]bool{
	"Pulling fs layer":   false,
	"Waiting":            false,
	"Downloading":        false,
	"Verifying Checksum": false,
	"Download complete":  false,
	"Extracting":         false,
	"Pull complete":      true,
	"Already exists":     true,
}

// layerCount counts the layers of an image that docker pull tells of, and
// reports them as the work, in items, of a progress span: its size is the
// layers docker has told of so far, which grows as the pull goes on, and its
// amount those that are done. It is safe for concurrent use, as the lines of
// the two streams are parsed from different goroutines.
type layerCount struct {
	span *progress.Span

	mu   sync.Mutex
	seen map[string]bool // layer → done
	done int
}

// line parses a line docker pull wrote, and has the span report the layers
// when it changes them. A line that tells nothing of a layer is ignored.
func (l *layerCount) line(_ progress.Stream, line string) {
	m := layerLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return
	}
	complete, known := layerStatuses[m[2]]
	if !known {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]bool{}
	}
	finished, listed := l.seen[m[1]]
	switch {
	case !listed:
		l.seen[m[1]] = complete
		if complete {
			l.done++
		}
		l.span.Update(progress.Work(progress.UnitItems, int64(len(l.seen))))
	case complete && !finished:
		l.seen[m[1]] = true
		l.done++
	default:
		return
	}
	l.span.SetAmount(int64(l.done))
}

// finish reports a pull that succeeded as done: every layer docker told of,
// or the one item that an image the daemon had already is, since docker
// writes no layer for it.
func (l *layerCount) finish() {
	l.mu.Lock()
	defer l.mu.Unlock()
	size := len(l.seen)
	if size == 0 {
		size = 1
		l.span.Update(progress.Work(progress.UnitItems, 1))
	}
	l.span.SetAmount(int64(size))
}
