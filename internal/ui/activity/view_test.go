// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

func target(name, state, status string) store.Span {
	return store.Span{Name: name, Node: name, Kind: KindTarget, State: state, Status: status}
}

func TestCountTargets(t *testing.T) {
	deep := store.Span{Kind: KindStep, Children: []store.Span{
		target("c", StateEnded, store.StatusCanceled),
		{Kind: KindCall, Children: []store.Span{target("hidden-below", StateRunning, "")}},
	}}
	step := store.Span{Kind: KindStep, Children: []store.Span{
		target("a", StateEnded, store.StatusOK),
		target("b", StateEnded, store.StatusFailed),
		target("d", StateEnded, store.StatusSkipped),
		target("e", StateRunning, ""),
		target("f", StateQueued, ""),
		target("g", StateEnded, "odd"),
		{Kind: KindWait, Children: []store.Span{{Kind: KindCall}}},
		deep,
	}}
	c := CountTargets(step)
	assert.Equal(t, Counts{Total: 8, OK: 1, Failed: 1, Canceled: 1, Skipped: 1, Running: 2, Queued: 1}, c)
	assert.Equal(t, 4, c.Done())
	assert.Equal(t, Counts{}, CountTargets(store.Span{}))
	// The span itself is not counted.
	assert.Equal(t, Counts{}, CountTargets(target("x", StateRunning, "")))
}

func TestVisibleChildren(t *testing.T) {
	s := store.Span{Children: []store.Span{{Name: "a"}, {Name: "b", Hidden: true}, {Name: "c"}}}
	assert.Equal(t, []string{"a", "c"}, names(VisibleChildren(s)))
	assert.Empty(t, VisibleChildren(store.Span{}))
}

// A hidden span shows once it has run for a second or failed, as in a
// terminal, and so do those below it; a call does not, and the span given is
// not changed.
func TestReveal(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { e := t0.Add(d); return &e }
	mesh := store.Span{Name: "mesh", Kind: KindStep, Hidden: true, Started: t0, Children: []store.Span{
		{Name: "dns", Kind: KindTarget, Hidden: true, Started: t0},
		{Name: "volume", Kind: KindTarget, Hidden: true, Started: t0, Ended: at(10 * time.Millisecond), Status: store.StatusOK},
		{Name: "docker create", Kind: KindCall, Hidden: true, Started: t0},
	}}
	root := store.Span{Children: []store.Span{
		mesh,
		{Name: "quick", Kind: KindStep, Hidden: true, Started: t0, Ended: at(time.Millisecond), Status: store.StatusOK},
		{Name: "broken", Kind: KindStep, Hidden: true, Started: t0, Ended: at(time.Millisecond), Status: store.StatusFailed},
		{Name: "pull images", Kind: KindStep, Started: t0},
	}}

	early := Reveal(root, t0.Add(500*time.Millisecond))
	assert.Equal(t, []string{"broken", "pull images"}, names(VisibleChildren(early)))

	later := Reveal(root, t0.Add(RevealAfter))
	assert.Equal(t, []string{"mesh", "broken", "pull images"}, names(VisibleChildren(later)))
	assert.Equal(t, []string{"dns"}, names(VisibleChildren(later.Children[0])))
	assert.True(t, root.Children[0].Hidden, "the span given is not changed")
	assert.True(t, root.Children[0].Children[0].Hidden)

	assert.Equal(t, store.Span{Name: "leaf"}, Reveal(store.Span{Name: "leaf"}, t0))
}

func TestFailedTargetsGroupByErrorText(t *testing.T) {
	failed := func(name, node, class, err string) store.Span {
		return store.Span{Name: name, Node: node, Kind: KindTarget, State: StateEnded, Status: store.StatusFailed, Class: class, Err: err}
	}
	step := store.Span{Kind: KindStep, Children: []store.Span{
		failed("w1", "w1", "timeout", "node w1: timed out"),
		failed("w10", "w10", "auth", "node w10: denied"),
		failed("w2", "w2", "timeout", "node w2: timed out"),
		failed("img", "", "transport", "pulling img: no answer"),
		{Kind: KindBatch, Children: []store.Span{
			failed("w3", "w3", "target", "node w3: timed out"),
			failed("w4", "w4", "other", "node w4: timed out"),
			// w1 inside w10, and as a whole word twice.
			failed("w1", "w1", "target", "w10 and w1: w1/w10, w1-x"),
			failed("w5", "w5", "", ""),
			failed("w6", "w6", "", ""),
			target("fine", StateEnded, store.StatusOK),
			{Kind: KindStep, Status: store.StatusFailed, Err: "not a target"},
		}},
	}}
	assert.Equal(t, []Failure{
		{Err: "node {}: timed out", Class: "timeout", Nodes: []string{"w1", "w2", "w3", "w4"}},
		{Err: "node {}: denied", Class: "auth", Nodes: []string{"w10"}},
		{Err: "pulling {}: no answer", Class: "transport", Nodes: []string{"img"}},
		{Err: "w10 and {}: {}/w10, w1-x", Class: "target", Nodes: []string{"w1"}},
		{Err: "", Class: "", Nodes: []string{"w5", "w6"}},
	}, FailedTargets(step))
	assert.Empty(t, FailedTargets(store.Span{}))
}

func TestMaskName(t *testing.T) {
	for _, c := range []struct{ text, name, want string }{
		{"node w1: x", "w1", "node {}: x"},
		{"w1", "w1", "{}"},
		{"w10 w1 w1", "w1", "w10 {} {}"},
		{"xw1 w1x w1_ w_w1 -w1", "w1", "xw1 w1x w1_ w_w1 -w1"},
		{"w1.mesh: w1.", "w1", "{}.mesh: {}."},
		{"anything", "", "anything"},
		{"", "w1", ""},
		{"worker-1 worker-10", "worker-1", "{} worker-10"},
	} {
		assert.Equal(t, c.want, maskName(c.text, c.name), c.text)
	}
}

func TestElapsed(t *testing.T) {
	now := at("2026-01-01T00:01:00Z")
	started := at("2026-01-01T00:00:10Z")
	ended := at("2026-01-01T00:00:40Z")
	assert.Equal(t, 50*time.Second, Elapsed(store.Span{Started: started}, now), "still running")
	assert.Equal(t, 30*time.Second, Elapsed(store.Span{Started: started, Ended: &ended}, now))
	assert.Zero(t, Elapsed(store.Span{Queued: started}, now), "queued, never ran")
	assert.Zero(t, Elapsed(store.Span{Queued: started, Ended: &ended}, now))
	assert.Zero(t, Elapsed(store.Span{Started: now.Add(time.Hour)}, now), "never negative")
}

func TestWaitProgress(t *testing.T) {
	now := at("2026-01-01T00:05:00Z")
	wait := func(since string, timeout time.Duration) store.Span {
		return store.Span{Kind: KindWait, Started: at(since), Timeout: timeout}
	}
	used, ok := WaitUsed(wait("2026-01-01T00:04:00Z", 4*time.Minute), now)
	assert.True(t, ok)
	assert.InDelta(t, 0.25, used, 1e-9)
	used, _ = WaitUsed(wait("2026-01-01T00:00:00Z", time.Minute), now)
	assert.Equal(t, 1.0, used, "no more than the limit")
	_, ok = WaitUsed(wait("2026-01-01T00:04:00Z", 0), now)
	assert.False(t, ok, "no limit")
}
