// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"errors"
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
// against a docker that answers with result, and returns the watcher and
// the pull's error.
func pullUnder(t *testing.T, result mock.Result) (*progresstest.Watcher, error) {
	t.Helper()
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, step := progress.Start(ctx, progress.KindStep, "pull images", progress.Flagged(progress.FlagFold), progress.Total(1))
	ctx, target := progress.Start(ctx, progress.KindTarget, "img:1", progress.Queued(), progress.Node("img:1"))
	target.Run()
	c := NewClient(&mock.Executor{OnCall: func([]string, string) mock.Result { return result }})

	err := c.PullImage(ctx, "img:1")

	target.End(err)
	step.End(err)
	return w, err
}

// A pull counts its layers: the size is the layers docker has told of, and
// the amount those that are done, "Pull complete" and "Already exists"; a
// layer counts once however many lines it writes, and the lines that name
// no layer are ignored. A pull that succeeded ends with every layer done.
func TestPullImage_CountsLayers(t *testing.T) {
	w, err := pullUnder(t, mock.Result{Stdout: pullOutput})

	require.NoError(t, err)
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{1, 2, 3}, sizes, "the size grows as layers are told of")
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
`})

	require.NoError(t, err)
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{1}, sizes)
	assert.Equal(t, progress.UnitItems, end.Unit)
	assert.EqualValues(t, 1, end.Amount)
	assert.EqualValues(t, 1, end.Size)
}

// A pull that fails ends where it was, with the layers it had told of.
// Statuses that docker may write in other releases, and lines of standard
// error, are ignored; a layer that is complete counts when it is the first
// line told of it, and once.
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
	})

	require.Error(t, err)
	end, sizes := work(t, w)
	w.Finish()
	assert.Equal(t, []int64{1, 2, 3}, sizes)
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
	c := NewClient(&mock.Executor{OnCall: func([]string, string) mock.Result { return mock.Result{Stdout: pullOutput} }})

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
