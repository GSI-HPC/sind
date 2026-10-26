// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pullOutput is what docker pull writes to standard output where it is no
// terminal: no bars, one status line for each change of a layer. The
// layers 1 and 3 are pulled, and the daemon has the second.
const pullOutput = `Using default tag: latest
latest: Pulling from library/busybox
a3ed95caeb02: Pulling fs layer
b4f1e0d2c9a1: Already exists
c5e2f1a3d0b2: Pulling fs layer
a3ed95caeb02: Waiting
a3ed95caeb02: Download complete
a3ed95caeb02: Pull complete
c5e2f1a3d0b2: Verifying Checksum
c5e2f1a3d0b2: Download complete
c5e2f1a3d0b2: Pull complete
Digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
Status: Downloaded newer image for busybox:latest
docker.io/library/busybox:latest
`

// work returns the unit, amount and size the end of the only target of the
// events carries, and the sizes its updates gave, in order.
func work(t *testing.T, w *progresstest.Watcher) (end progress.Event, sizes []int64) {
	t.Helper()
	for _, e := range w.Events() {
		if e.Kind != progress.KindTarget {
			continue
		}
		switch e.Type {
		case progress.TypeUpdate:
			sizes = append(sizes, e.Size)
		case progress.TypeEnd:
			end = e
		}
	}
	return end, sizes
}

// pullUnder pulls img:1 under the running target "img:1" of a watched Bus
// against a docker that answers with result, on a terminal or a pipe, and
// returns the watcher and the pull's error.
func pullUnder(t *testing.T, result mock.Result, terminal bool) (*progresstest.Watcher, error) {
	t.Helper()
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, step := progress.Start(ctx, progress.KindStep, "pull images", progress.Flagged(progress.FlagFold), progress.Total(1))
	ctx, target := progress.Start(ctx, progress.KindTarget, "img:1", progress.Queued(), progress.Node("img:1"))
	target.Run()
	c := NewClient(&mock.Executor{NoTerminal: !terminal, OnCall: func([]string, string) mock.Result { return result }})

	err := c.PullImage(ctx, "img:1")

	target.End(err)
	step.End(err)
	return w, err
}

// Where docker writes no bytes, a pull counts its layers, from the first
// layer that is complete: the size is the layers docker has told of, and
// the amount those that are done, "Pull complete" and "Already exists"; a
// layer counts once however many lines it writes, and the lines that name
// no layer are ignored. A pull that succeeded ends with every layer done.
func TestPullImage_CountsLayers(t *testing.T) {
	w, err := pullUnder(t, mock.Result{Stdout: pullOutput}, false)

	require.NoError(t, err)
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{3}, sizes, "the layers told of when the first is complete")
	assert.Equal(t, progress.UnitItems, end.Unit)
	assert.EqualValues(t, 3, end.Amount)
	assert.EqualValues(t, 3, end.Size)
	assert.Equal(t, progress.StatusOK, end.Status)
}

// A pull that finds the image there writes no layer: it is one item, done.
func TestPullImage_UpToDate(t *testing.T) {
	w, err := pullUnder(t, mock.Result{Stdout: `Using default tag: latest
latest: Pulling from library/busybox
Digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
Status: Image is up to date for busybox:latest
docker.io/library/busybox:latest
`}, true)

	require.NoError(t, err)
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{1}, sizes)
	assert.Equal(t, progress.UnitItems, end.Unit)
	assert.EqualValues(t, 1, end.Amount)
	assert.EqualValues(t, 1, end.Size)
}

// A pull that fails ends where it was, with the layers it had told of.
// Statuses that docker may write in other releases are ignored, and lines
// of standard error count; a layer that is complete counts when it is the
// first line told of it, and once.
func TestPullImage_Fails(t *testing.T) {
	w, err := pullUnder(t, mock.Result{
		Stdout: "a3ed95caeb02: Pulling fs layer\r\n" +
			"b4f1e0d2c9a1: Pull complete\n" +
			"b4f1e0d2c9a1: Pull complete\n" +
			"b4f1e0d2c9a1: Retrying in 5 seconds\n" +
			"zzzzzzzzzzzz: Pull complete\n" +
			"abc: Pull complete\n" +
			"a3ed95caeb02: Download complete",
		Stderr: "c5e2f1a3d0b2: Pull complete\nerror pulling image configuration: download failed\n",
		Err:    errors.New("exit status 1"),
	}, false)

	require.Error(t, err)
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{2, 3}, sizes)
	assert.EqualValues(t, 2, end.Amount, "the layers of the lines of stdout and stderr that are complete")
	assert.EqualValues(t, 3, end.Size)
	assert.Equal(t, progress.StatusFailed, end.Status)
}

// Without a progress span there is nothing to report to, and the pull is
// docker's output alone.
func TestPullImage_WithoutASpan(t *testing.T) {
	var m mock.Executor
	m.AddResult(pullOutput, "", nil)

	require.NoError(t, NewClient(&m).PullImage(t.Context(), "img:1"))

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"pull", "img:1"}, m.Calls[0].Args)
}

// The target's advances carry the layers done, in items, out of the size
// the updates gave, as a display draws a bar from them and the event log
// holds them (amount, size and unit), never a line of docker's output.
func TestPullImage_AdvancesTheTarget(t *testing.T) {
	var tick atomic.Int64
	capture := &progresstest.Capture{}
	bus := progress.NewBus(progress.BusOptions{
		Sinks: []progress.Sink{capture},
		// Each reading of the clock is 150 ms later than the last, so that
		// no advance waits for the 100 ms between two.
		Now: func() time.Time {
			return time.Unix(1_800_000_000, 0).Add(time.Duration(tick.Add(1)) * 150 * time.Millisecond)
		},
	})
	ctx, target := progress.Start(progress.WithBus(t.Context(), bus), progress.KindTarget, "img:1", progress.Queued(), progress.Node("img:1"))
	target.Run()
	c := NewClient(&mock.Executor{NoTerminal: true, OnCall: func([]string, string) mock.Result { return mock.Result{Stdout: pullOutput} }})

	require.NoError(t, c.PullImage(ctx, "img:1"))

	target.End(nil)
	progresstest.Check(t, capture.Events())
	bus.Close()
	var amounts []int64
	for _, e := range capture.Events() {
		if e.Type == progress.TypeAdvance {
			assert.Equal(t, progress.UnitItems, e.Unit)
			assert.NotZero(t, e.Size)
			amounts = append(amounts, e.Amount)
		}
	}
	assert.NotEmpty(t, amounts)
	assert.IsIncreasing(t, amounts)
	for _, e := range capture.Events() {
		assert.Empty(t, e.Text, "no output text in the events")
	}
}

// tty writes lines as docker pull redraws the line of a layer on a
// terminal: up to it, erase it, write it, back down.
func tty(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("\x1b[1A\x1b[2K\r" + l + " \r\x1b[1B")
	}
	return b.String()
}

// Where docker writes the bytes of the layers, on a terminal, a pull counts
// bytes: the amount those downloaded, the size that of the layers to
// download, those the daemon has left out, with the size of the layers
// still waiting guessed from the mean of those known. The share done never
// goes back: when a guess is short, the size is held down until the bytes
// catch up. A pull that succeeded ends with the bytes of every layer.
func TestPullImage_CountsBytes(t *testing.T) {
	var shares []float64
	w, err := pullUnder(t, mock.Result{Stdout: "latest: Pulling from library/busybox\n" + tty(
		"a3ed95caeb02: Already exists",
		"b4f1e0d2c9a1: Pulling fs layer",
		"c5e2f1a3d0b2: Pulling fs layer",
		"d6f3a2b4e1c3: Pulling fs layer",
		"c5e2f1a3d0b2: Waiting",
		"d6f3a2b4e1c3: Waiting",
		"b4f1e0d2c9a1: Downloading  1MB/10MB 9s",
		"c5e2f1a3d0b2: Downloading  1MB/40MB",
		"b4f1e0d2c9a1: Downloading     512B",
		"b4f1e0d2c9a1: Downloading  1.2.3MB/10MB",
		"b4f1e0d2c9a1: Downloading  11MB/10MB",
		"b4f1e0d2c9a1: Download complete",
		"d6f3a2b4e1c3: Downloading  5MB/50MB",
		"c5e2f1a3d0b2: Verifying Checksum",
		"d6f3a2b4e1c3: Downloading  50MB/50MB",
		"b4f1e0d2c9a1: Extracting  5MB/10MB",
		"b4f1e0d2c9a1: Pull complete",
		"c5e2f1a3d0b2: Pull complete",
	) + "Digest: sha256:0123\nStatus: Downloaded newer image for busybox:latest\n"}, true)

	require.NoError(t, err)
	var size int64
	for _, e := range w.Events() {
		if e.Kind != progress.KindTarget {
			continue
		}
		if e.Type == progress.TypeUpdate {
			size = e.Size
		}
		if e.Type == progress.TypeAdvance && size > 0 {
			shares = append(shares, float64(e.Amount)/float64(size))
		}
	}
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{30_000_000, 60_000_000, 75_000_000, 100_000_000}, sizes,
		"guessed, held down, guessed, known")
	assert.IsNonDecreasing(t, shares)
	assert.Equal(t, progress.UnitBytes, end.Unit)
	assert.EqualValues(t, 100_000_000, end.Amount)
	assert.EqualValues(t, 100_000_000, end.Size)
}

// On a terminal, a small layer that is complete before docker wrote the
// bytes of any layer, as the daemon with the containerd image store often
// writes, leaves the unit open: the pull counts bytes once docker writes
// them, after its bar where its terminal is wide.
func TestPullImage_SmallLayerFirstOnATerminal(t *testing.T) {
	w, err := pullUnder(t, mock.Result{Stdout: tty(
		"a3ed95caeb02: Pulling fs layer",
		"b4f1e0d2c9a1: Pulling fs layer",
		"a3ed95caeb02: Download complete",
		"a3ed95caeb02: Pull complete",
		"b4f1e0d2c9a1: Downloading [=>        ]  1MB/10MB",
		"b4f1e0d2c9a1: Download complete",
		"b4f1e0d2c9a1: Pull complete",
	)}, true)

	require.NoError(t, err)
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{10_000_000}, sizes)
	assert.Equal(t, progress.UnitBytes, end.Unit)
	assert.EqualValues(t, 10_000_000, end.Amount)
}

// A line of a layer whose bytes cannot be read, or whose size is none, is a
// change of state only.
func TestDownloadBytes(t *testing.T) {
	for _, tc := range []struct {
		status, rest string
		got, size    int64
		ok           bool
	}{
		{"Downloading", "1.049MB/45.6MB 3s", 1_049_000, 45_600_000, true},
		{"Downloading", "512B/2kB", 512, 2000, true},
		{"Downloading", "[==>                                                ]  1.049MB/21.33MB", 1_049_000, 21_330_000, true},
		{"Downloading", "[==================================================>]  21.33MB/21.33MB", 21_330_000, 21_330_000, true},
		{"Downloading", "[==> 1MB/2MB", 0, 0, false},
		{"Extracting", "1MB/2MB", 0, 0, false},
		{"Downloading", "512B", 0, 0, false},
		{"Downloading", "0B/0B", 0, 0, false},
		{"Downloading", "1MB/1.2.3MB", 0, 0, false},
	} {
		got, size, ok := downloadBytes(tc.status, tc.rest)
		assert.Equal(t, tc.ok, ok, tc.rest)
		if tc.ok {
			assert.Equal(t, tc.got, got, tc.rest)
			assert.Equal(t, tc.size, size, tc.rest)
		}
	}
}
