// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/display"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// EnsureMesh is the hidden progress step "mesh", with a target for each
// part of the mesh, all announced before the first runs; a part in place
// already is skipped, and the parts after one that failed end canceled
// with the step. The docker commands are calls under their part, which
// show their lines, and the error of the part that failed is the step's.
func TestEnsureMesh_ReportsItsParts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fakeDocker)
		want  string
	}{
		{"new", func(*fakeDocker) {}, `step mesh total=4 [hidden,fold,show-lines]: ok
  target dns,relay amount=2000000 size=2000000 unit=bytes [hidden,show-lines]: ok
  target network,volume [hidden,show-lines]: ok
`},
		{"new, the images there", func(f *fakeDocker) { f.images[DNSImage], f.images[SSHImage()] = true, true }, `step mesh total=4 [hidden,fold,show-lines]: ok
  target dns,network,relay,volume [hidden,show-lines]: ok
`},
		{"in place", func(f *fakeDocker) { f.withPinnedMesh(DefaultRealm, docker.StateRunning) }, `step mesh total=4 [hidden,fold,show-lines]: ok
  target dns,relay [hidden,show-lines]: skipped: running
  target network,volume [hidden,show-lines]: skipped: exists
`},
		{"stopped", func(f *fakeDocker) { f.withPinnedMesh(DefaultRealm, docker.StateExited) }, `step mesh total=4 [hidden,fold,show-lines]: ok
  target dns,relay [hidden,show-lines]: ok
  target network,volume [hidden,show-lines]: skipped: exists
`},
		{"relay fails", func(f *fakeDocker) { f.fail["start sind-ssh"] = failure() }, `step mesh total=4 [hidden,fold,show-lines]: failed (target): starting SSH container: connection refused
  target dns amount=2000000 size=2000000 unit=bytes [hidden,show-lines]: ok
  target network,volume [hidden,show-lines]: ok
  target relay amount=2000000 size=2000000 unit=bytes [hidden,show-lines]: failed (target): starting SSH container: connection refused
`},
		{"network fails", func(f *fakeDocker) { f.fail["network inspect"] = failure() }, `step mesh total=4 [hidden,fold,show-lines]: failed (target): checking mesh network: connection refused
  target dns,relay,volume [hidden,show-lines]: canceled (canceled): not finished
  target network [hidden,show-lines]: failed (target): checking mesh {}: connection refused
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, _ := newFakeDocker(t)
			tc.setup(f)
			ctx, w := progresstest.Watch(t.Context(), t)

			err := NewManager(c, DefaultRealm).EnsureMesh(ctx)

			assert.Equal(t, strings.Contains(tc.want, "failed"), err != nil, "error: %v", err)
			w.Finish()
			calls := 0
			for _, e := range w.Events() {
				if e.Type == progress.TypeStart && e.Kind == progress.KindCall {
					calls++
					assert.Equal(t, progress.FlagHidden|progress.FlagShowLines, e.Flags, e.Name)
				}
			}
			assert.Positive(t, calls)
			assert.Equal(t, tc.want, testutil.WithoutCalls(w.Events()))
		})
	}
}

// A part whose work fails after another part's failure canceled the group
// they run in ends canceled, with its error as the text: the docker
// command the cancellation killed fails with "signal: killed" alone. The
// part that failed first fails.
func TestPart_StoppedByTheGroup(t *testing.T) {
	ctx, w := progresstest.Watch(t.Context(), t)
	ctx, step := progress.Start(ctx, progress.KindStep, "mesh", progress.Flagged(progress.FlagFold), progress.Total(2))
	gctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	dns, volume := startPart(gctx, partDNS), startPart(gctx, partVolume)
	failed := errors.New("creating DNS container: no space left on device")

	require.Equal(t, failed, dns.run(skipRunning, func(context.Context) (bool, error) { return true, failed }))
	cancel(failed)
	killed := errors.New("creating SSH volume: signal: killed")
	require.Equal(t, killed, volume.run(skipExists, func(context.Context) (bool, error) { return true, killed }))
	step.End(failed)

	assert.Equal(t, `step mesh total=2 [fold]: failed (target): creating DNS container: no space left on device
  target dns: failed (target): creating DNS container: no space left on device
  target volume: canceled (canceled): creating SSH {}: signal: killed
`, w.Finish())
}

// screenClock is the clock of a Bus and of the Tree that draws its events
// on a screen, which a test moves on.
type screenClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *screenClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *screenClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// onScreen is a display.Tree that draws the events of a Bus on a screen,
// at the time of a clock that the test moves on.
type onScreen struct {
	clock   *screenClock
	screen  *progresstest.Screen
	tree    *display.Tree
	capture *progresstest.Capture
	bus     *progress.Bus
	command *progress.Span
}

// treeOnScreen returns ctx under the command "create cluster" of a Bus
// whose events a Tree draws on a screen.
func treeOnScreen(t *testing.T) (context.Context, *onScreen) {
	t.Helper()
	s := &onScreen{
		clock:   &screenClock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)},
		screen:  &progresstest.Screen{Width: 100},
		capture: &progresstest.Capture{Lines: true},
	}
	term := display.NewTerminal(s.screen, display.TerminalOptions{Size: func() (int, int, error) { return 100, 32, nil }})
	s.tree = display.NewTree(term, display.TreeOptions{Now: s.clock.Now})
	s.bus = progress.NewBus(progress.BusOptions{Sinks: []progress.Sink{s.tree, s.capture}, Now: s.clock.Now})
	ctx, command := progress.Start(progress.WithBus(t.Context(), s.bus), progress.KindCommand, "create cluster")
	s.command = command
	return ctx, s
}

// draw moves the clock on by d, draws a frame and returns what the screen
// shows.
func (s *onScreen) draw(d time.Duration) string {
	s.clock.add(d)
	s.tree.Draw()
	return s.screen.String()
}

// end ends the command a second later, checks the events, closes the Bus
// and the Tree, and returns what the screen shows.
func (s *onScreen) end(t *testing.T) string {
	t.Helper()
	s.clock.add(time.Second)
	s.command.End(nil)
	progresstest.Check(t, s.capture.Events())
	s.bus.Close()
	s.tree.Close()
	return s.screen.String()
}

// pullingExecutor is the docker CLI of a fake daemon that does not have
// the images of the mesh: the pull of the relay's image writes its first
// line, and a frame of the tree is drawn two seconds later.
type pullingExecutor struct {
	*mock.Executor
	screen *onScreen
	frame  string
}

func (e *pullingExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	if strings.Join(args, " ") == "pull "+SSHImage() {
		stdout := progress.Tee(ctx, io.Discard, progress.StreamStdout, nil)
		_, _ = io.WriteString(stdout, "latest: Pulling from gsi-hpc/sind-node\n")
		e.frame = e.screen.draw(2 * time.Second)
	}
	return e.Executor.Run(ctx, name, args...)
}

// On a host that has no mesh, the tree draws the last line of the pull of
// the relay's image in the row of the relay, under the step "mesh", whose
// row counts the parts and the bytes of the pulls, and leaves the line of
// the step once it ends.
func TestEnsureMesh_TreeShowsThePullInThePartsRow(t *testing.T) {
	ctx, s := treeOnScreen(t)
	exec := &pullingExecutor{Executor: &mock.Executor{OnCall: newFake(t).onCall}, screen: s}

	require.NoError(t, NewManager(docker.NewClient(exec), DefaultRealm).EnsureMesh(ctx))

	assert.Equal(t, `create cluster · 0:02.0
  mesh  3/4 · 1 running · 1.9 MiB · 75% · 0.0 B/s
    ▸ relay  2.0s  latest: Pulling from gsi-hpc/sind-node
    ✓ dns,network,volume
`, exec.frame)
	assert.Equal(t, "✓ mesh  2.0s  4 ok  3.8 MiB at 1.9 MiB/s\n", s.end(t))
}

// teeingExecutor is the docker CLI of a fake daemon that does not have the
// relay's image, whose output reaches the progress spans as OSExecutor's
// does: standard error always, standard output unless it is data. The pull
// of the relay's image writes its lines to standard output, the docker
// create of the relay the container's ID, and a frame of the tree is drawn
// at each docker command after the pull while the relay's part runs.
type teeingExecutor struct {
	*mock.Executor
	screen *onScreen
	pulled bool
	frames []string
}

func (e *teeingExecutor) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	stdout, stderr, err := e.Executor.Run(ctx, name, args...)
	joined := strings.Join(args, " ")
	if strings.HasPrefix(joined, "create --name sind-ssh ") {
		stdout = strings.Repeat("0123456789abcdef", 4) + "\n"
	}
	if !cmdexec.StdoutIsData(ctx) {
		_, _ = io.WriteString(progress.Tee(ctx, io.Discard, progress.StreamStdout, nil), stdout)
	}
	_, _ = io.WriteString(progress.Tee(ctx, io.Discard, progress.StreamStderr, nil), stderr)
	if e.pulled && strings.Contains(joined, "sind-ssh") {
		e.frames = append(e.frames, e.screen.draw(time.Second))
	}
	e.pulled = e.pulled || joined == "pull "+SSHImage()
	return stdout, stderr, err
}

// The relay's row shows the last line of the pull for as long as the part
// runs, not the container's ID its docker create writes after it, nor the
// JSON of the inspects that follow.
func TestEnsureMesh_TreeShowsNoDataInThePartsRow(t *testing.T) {
	ctx, s := treeOnScreen(t)
	exec := &teeingExecutor{Executor: &mock.Executor{OnCall: newFake(t).onCall}, screen: s}

	require.NoError(t, NewManager(docker.NewClient(exec), DefaultRealm).EnsureMesh(ctx))

	require.NotEmpty(t, exec.frames)
	for _, frame := range exec.frames {
		assert.Contains(t, frame, "▸ relay", frame)
		assert.Contains(t, frame, "Status: Downloaded newer image for ", frame)
	}
	s.end(t)
}

// On a host whose mesh is in place, the step leaves nothing on the tree:
// it is hidden, and over within a second.
func TestEnsureMesh_TreeLeavesNothingForAMeshInPlace(t *testing.T) {
	ctx, s := treeOnScreen(t)
	f, c, _ := newFakeDocker(t)
	f.withPinnedMesh(DefaultRealm, docker.StateRunning)

	require.NoError(t, NewManager(c, DefaultRealm).EnsureMesh(ctx))

	assert.Equal(t, "create cluster · 0:01.0\n", s.draw(time.Second))
	assert.Empty(t, s.end(t))
}
