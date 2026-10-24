// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

const (
	runA = "aaaaaaaaaaaaaaaa"
	// traceT and parentP are a trace and a parent span a caller handed on.
	traceT  = "0123456789abcdef0123456789abcdef"
	parentP = "00f067aa0ba902b7"
)

// start gives the reducer the start of a span at the second s.
func start(r *Reducer, run string, s int, id, parent, kind, name string, extra ...string) {
	rest := fmt.Sprintf(`"span":%q,"kind":%q,"name":%q`, id, kind, name)
	if parent != "" {
		rest += fmt.Sprintf(`,"parent":%q`, parent)
	}
	for _, e := range extra {
		rest += "," + e
	}
	r.Take(ev("start", run, fmt.Sprintf("2026-01-01T00:00:%02dZ", s), rest))
}

func TestTreeByParent(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "root", "", "command", "create cluster")
	start(r, runA, 1, "b", "root", "step", "b")
	start(r, runA, 2, "a", "root", "step", "a")
	start(r, runA, 3, "a1", "a", "target", "a1")
	start(r, runA, 4, "b1", "b", "target", "b1")
	start(r, runA, 5, "a2", "a", "target", "a2")
	// A span whose parent is not known hangs below the root, and so does
	// one that names itself, and one without a parent.
	start(r, runA, 6, "orphan", "gone", "step", "orphan")
	start(r, runA, 7, "self", "self", "step", "self")
	start(r, runA, 8, "bare", "", "step", "bare")
	// A cycle that does not reach the root is out of the tree.
	start(r, runA, 9, "x", "y", "step", "x")
	start(r, runA, 9, "y", "x", "step", "y")

	runs := r.Runs()
	require.Len(t, runs, 1)
	root := runs[0].Root
	assert.Equal(t, []string{"b", "a", "orphan", "self", "bare"}, names(root.Children), "in start order")
	assert.Equal(t, []string{"b1"}, names(root.Children[0].Children))
	assert.Equal(t, []string{"a1", "a2"}, names(root.Children[1].Children))
	assert.Equal(t, sid("gone"), root.Children[2].Parent, "the parent the log named")
	assert.Equal(t, at("2026-01-01T00:00:00Z"), runs[0].Start)
}

func TestRunWithoutCommandSpan(t *testing.T) {
	r := NewReducer(5)
	// The start of the command was cut off, as by a rotation.
	start(r, runA, 1, "s1", "root", "step", "nodes")
	assert.Empty(t, r.Runs(), "a run needs its command")
	assert.Equal(t, []string{runA}, r.Changed())
	start(r, runA, 0, "root", "", "command", "create cluster")
	runs := r.Runs()
	require.Len(t, runs, 1)
	assert.Equal(t, []string{"nodes"}, names(runs[0].Root.Children), "the span of before is below it")
}

func TestEventsBeforeTheTraceLine(t *testing.T) {
	r := NewReducer(5)
	r.Take(ev("start", runA, "2026-01-01T00:00:00Z", `"trace":"`+traceT+`","span":"r","kind":"command","name":"x"`))
	assert.Equal(t, traceT, r.Runs()[0].Trace, "the events name the trace too")
	assert.Empty(t, r.Runs()[0].Program)
	r.Take(line("trace", runA, `"trace":"`+traceT+`","parent":"`+parentP+`","traceFlags":"01","program":"sind","version":"v2"`))
	run := r.Runs()[0]
	assert.Equal(t, "sind", run.Program)
	assert.Equal(t, "v2", run.Version)
	assert.Equal(t, parentP, run.Parent)
	assert.Equal(t, "x", run.Title, "the span is kept")
}

func TestKeepDropsEndedRunsFirst(t *testing.T) {
	const b, c, d = "bbbbbbbbbbbbbbbb", "cccccccccccccccc", "dddddddddddddddd"
	r := NewReducer(2)
	start(r, runA, 0, "r", "", "command", "a") // running, the oldest
	start(r, b, 1, "r", "", "command", "b")
	r.Take(ev("end", b, "2026-01-01T00:00:02Z", `"span":"r","status":"ok"`))
	start(r, c, 3, "r", "", "command", "c")
	assert.Equal(t, []string{"c", "a"}, titles(r), "b ended, so b goes, not the older a")
	assert.Contains(t, r.Changed(), b, "the drop is a change of the run")

	// With none ended the oldest goes.
	start(r, d, 4, "r", "", "command", "d")
	assert.Equal(t, []string{"d", "c"}, titles(r))

	// A line of a run that was dropped does not bring it back.
	r.Take(ev("end", runA, "2026-01-01T00:00:05Z", `"span":"r","status":"ok"`))
	r.Take(ev("start", b, "2026-01-01T00:00:06Z", `"span":"late","kind":"step","name":"late"`))
	assert.Equal(t, []string{"d", "c"}, titles(r))
}

func titles(r *Reducer) []string {
	var out []string
	for _, run := range r.Runs() {
		out = append(out, run.Title)
	}
	return out
}

func TestKeepAtLeastOne(t *testing.T) {
	r := NewReducer(0)
	for i := range 40 {
		id := fmt.Sprintf("%016x", i+1)
		start(r, id, i%60, "r", "", "command", id)
	}
	assert.Equal(t, []string{fmt.Sprintf("%016x", 40)}, titles(r), "one run is kept")
}

func TestReset(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "a")
	start(r, "bbbbbbbbbbbbbbbb", 1, "r", "", "command", "b")
	_ = r.Changed()
	r.Reset()
	assert.Empty(t, r.Runs())
	assert.Equal(t, []string{runA, "bbbbbbbbbbbbbbbb"}, r.Changed())
	start(r, runA, 2, "r", "", "command", "a2")
	assert.Equal(t, []string{"a2"}, titles(r), "a run forgotten may start again")
}

func TestTextsAreEscaped(t *testing.T) {
	r := NewReducer(5)
	r.Take(line("trace", runA, `"program":"si\u001b[31mnd","version":"v1\u0000"`))
	r.Take(ev("start", runA, "2026-01-01T00:00:00Z",
		`"span":"r","kind":"command","name":"cre\u0007ate\tcluster","node":"de\u202ev","host":"h\u0085ost","role":"w\u200eorker","batch":"1\n/2","message":"a\r\nb\u001b]0;x\u0007"`))
	r.Take(ev("end", runA, "2026-01-01T00:00:01Z", `"span":"r","kind":"command","name":"cre\u0007ate\tcluster","node":"de\u202ev","host":"h\u0085ost","role":"w\u200eorker","batch":"1\n/2","message":"a\r\nb\u001b]0;x\u0007","status":"failed","class":"target","err":"bad\u0000 news\u202d!"`))
	run := r.Runs()[0]
	assert.Equal(t, `si\x1b[31mnd`, run.Program, "a control character is shown escaped, never acted on")
	assert.Equal(t, `v1\x00`, run.Version)
	assert.Equal(t, `cre\x07ate\tcluster de\u202ev`, run.Title)
	assert.Equal(t, `h\u0085ost`, run.Root.Host)
	assert.Equal(t, `w\u200eorker`, run.Root.Role)
	assert.Equal(t, `1\n/2`, run.Root.Batch)
	assert.Equal(t, `a\nb\x1b]0;x\x07`, run.Root.Message)
	assert.Equal(t, `bad\x00 news\u202d!`, run.Err)
}

func TestSuspendAndResume(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "c")
	r.Take(ev("suspend", runA, "2026-01-01T00:00:01Z", `"span":"r"`))
	run := r.Runs()[0]
	assert.True(t, run.Suspended)
	assert.Equal(t, at("2026-01-01T00:00:01Z"), run.Last)
	r.Take(ev("resume", runA, "2026-01-01T00:00:02Z", ``))
	assert.False(t, r.Runs()[0].Suspended)
	r.Take(ev("suspend", runA, "2026-01-01T00:00:03Z", ``))
	r.Take(ev("end", runA, "2026-01-01T00:00:04Z", `"span":"r","status":"ok"`))
	assert.False(t, r.Runs()[0].Suspended, "a run that ended asks nothing")
}

func TestTitleTellsRunsApart(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "create cluster", `"node":"dev"`)
	start(r, "bbbbbbbbbbbbbbbb", 1, "r", "", "command", "create cluster")
	assert.Equal(t, []string{"create cluster", "create cluster dev"}, titles(r))
}

func TestSpanLifecycle(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "c")
	start(r, runA, 1, "q", "r", "target", "q", `"state":"queued"`)
	start(r, runA, 1, "u", "r", "target", "u", `"state":"queued"`)
	start(r, runA, 1, "n", "r", "target", "n", `"state":"running"`)
	var spans []store.Span
	get := func() { spans = r.Runs()[0].Root.Children }
	get()
	assert.Equal(t, StateQueued, spans[0].State)
	assert.True(t, spans[0].Started.IsZero())
	assert.Equal(t, at("2026-01-01T00:00:01Z"), spans[0].Queued)
	assert.Equal(t, StateRunning, spans[2].State)
	assert.Equal(t, spans[2].Queued, spans[2].Started)

	// run starts a queued span, once; the second changes nothing.
	r.Take(ev("run", runA, "2026-01-01T00:00:03Z", `"span":"q","parent":"r","kind":"target","state":"running"`))
	r.Take(ev("run", runA, "2026-01-01T00:00:04Z", `"span":"q","parent":"r","kind":"target","state":"running"`))
	r.Take(ev("run", runA, "2026-01-01T00:00:04Z", `"span":"n","parent":"r","kind":"target","state":"running"`))
	get()
	assert.Equal(t, StateRunning, spans[0].State)
	assert.Equal(t, at("2026-01-01T00:00:03Z"), spans[0].Started)
	assert.Equal(t, at("2026-01-01T00:00:01Z"), spans[2].Started)

	// A queued span can end without having run.
	r.Take(ev("end", runA, "2026-01-01T00:00:05Z", `"span":"u","parent":"r","status":"canceled","class":"canceled","err":"stopped"`))
	get()
	assert.Equal(t, StateEnded, spans[1].State)
	assert.True(t, spans[1].Started.IsZero())
	require.NotNil(t, spans[1].Ended)
	assert.Equal(t, at("2026-01-01T00:00:05Z"), *spans[1].Ended)

	// Once ended, a span is done: a repeated end or a late event is left
	// out.
	r.Take(ev("end", runA, "2026-01-01T00:00:09Z", `"span":"u","status":"ok","message":"later","exit":3`))
	r.Take(ev("line", runA, "2026-01-01T00:00:09Z", `"span":"u","stream":"stderr"`))
	get()
	assert.Equal(t, store.StatusCanceled, spans[1].Status)
	assert.Equal(t, at("2026-01-01T00:00:05Z"), *spans[1].Ended)
	assert.Empty(t, spans[1].Message)
	assert.Nil(t, spans[1].Exit)
	assert.Zero(t, spans[1].Lines)
	// The run's newest event is still the newest the log had.
	assert.Equal(t, at("2026-01-01T00:00:09Z"), r.Runs()[0].Last)

	// A span first met at an update or an end, as after a cut log.
	r.Take(ev("update", runA, "2026-01-01T00:00:10Z", `"span":"m1","parent":"r","kind":"step","name":"m1","state":"queued","total":4`))
	r.Take(ev("end", runA, "2026-01-01T00:00:11Z", `"span":"m2","parent":"r","kind":"step","name":"m2","state":"ended","status":"ok","exit":0`))
	get()
	assert.Equal(t, StateQueued, spans[3].State)
	assert.Equal(t, 4, spans[3].Total)
	assert.Equal(t, StateEnded, spans[4].State)
	assert.Equal(t, store.StatusOK, spans[4].Status)
	assert.Equal(t, at("2026-01-01T00:00:11Z"), spans[4].Queued)
	require.NotNil(t, spans[4].Exit)
	assert.Zero(t, *spans[4].Exit)
}

func TestLinesAndDropped(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "c")
	start(r, runA, 0, "s", "r", "call", "sol", `"flags":["show-lines"]`)
	for _, extra := range []string{`,"dropped":2`, ``, `,"dropped":-4`, `,"dropped":1`} {
		r.Take(ev("line", runA, "2026-01-01T00:00:01Z", `"span":"s","parent":"r","kind":"call","name":"sol","flags":["show-lines"],"stream":"stdout"`+extra))
	}
	s := r.Runs()[0].Root.Children[0]
	assert.Equal(t, 4, s.Lines)
	assert.Equal(t, 3, s.Dropped)
	assert.True(t, s.ShowLines)
	// The end counts all the span's.
	r.Take(ev("end", runA, "2026-01-01T00:00:02Z", `"span":"s","status":"ok","dropped":5`))
	assert.Equal(t, 5, r.Runs()[0].Root.Children[0].Dropped)
	r.Take(ev("end", runA, "2026-01-01T00:00:02Z", `"span":"r","status":"ok","dropped":-1`))
	assert.Zero(t, r.Runs()[0].Root.Dropped)
}

func TestWorkOfASpan(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "c")
	start(r, runA, 0, "d", "r", "call", "download", `"state":"running"`)
	amount := func() int64 { return r.Runs()[0].Root.Children[0].Amount }

	assert.Zero(t, amount(), "no work yet")
	r.Take(ev("update", runA, "2026-01-01T00:00:01Z", `"span":"d","parent":"r","kind":"call","size":1000,"unit":"bytes"`))
	assert.Zero(t, amount())
	assert.EqualValues(t, 1000, r.Runs()[0].Root.Children[0].Size)
	r.Take(ev("advance", runA, "2026-01-01T00:00:02Z", `"span":"d","parent":"r","kind":"call","amount":400,"size":1000,"unit":"bytes"`))
	assert.EqualValues(t, 400, amount())
	r.Take(ev("advance", runA, "2026-01-01T00:00:03Z", `"span":"d","parent":"r","kind":"call","amount":700,"size":1000,"unit":"bytes","leftOut":9`))
	assert.EqualValues(t, 700, amount())
	// An event leaves out an amount of 0: the download started over.
	r.Take(ev("advance", runA, "2026-01-01T00:00:04Z", `"span":"d","parent":"r","kind":"call","size":1000,"unit":"bytes"`))
	assert.Zero(t, amount())
	r.Take(ev("advance", runA, "2026-01-01T00:00:05Z", `"span":"d","parent":"r","kind":"call","amount":-5,"size":1000,"unit":"bytes"`))
	assert.Zero(t, amount(), "never negative")
	r.Take(ev("end", runA, "2026-01-01T00:00:06Z", `"span":"d","parent":"r","kind":"call","amount":1000,"size":1000,"unit":"bytes","status":"ok"`))
	d := r.Runs()[0].Root.Children[0]
	assert.EqualValues(t, 1000, d.Amount)
	assert.EqualValues(t, 1000, d.Size)
	assert.Equal(t, "bytes", d.Unit)

	// An amount with no unit and no size, as a log of a later release may
	// write, is kept.
	start(r, runA, 7, "x", "r", "step", "x", `"amount":12`)
	assert.EqualValues(t, 12, r.Runs()[0].Root.Children[1].Amount)
}

func TestCounts(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "c")
	start(r, runA, 0, "s", "r", "step", "s", `"total":7,"limit":3`)
	r.Take(ev("update", runA, "2026-01-01T00:00:01Z", `"span":"s","parent":"r","kind":"step","name":"s","total":5,"limit":3,"batch":"2/5","message":"working"`))
	s := r.Runs()[0].Root.Children[0]
	assert.Equal(t, 5, s.Total, "the latest event says")
	assert.Equal(t, 3, s.Limit)
	assert.Equal(t, "2/5", s.Batch)
	assert.Equal(t, "working", s.Message)
}

func TestTimeouts(t *testing.T) {
	r := NewReducer(5)
	start(r, runA, 0, "r", "", "command", "c")
	for i, c := range []struct {
		json string
		want time.Duration
	}{
		{`"timeout":1.5`, 1500 * time.Millisecond},
		{`"timeout":300`, 300 * time.Second},
		{`"timeout":-3`, 0},
		{`"timeout":1e30`, math.MaxInt64},
	} {
		start(r, runA, 1, fmt.Sprintf("w%d", i), "r", "wait", "w", c.json)
	}
	for i, c := range r.Runs()[0].Root.Children {
		want := []time.Duration{1500 * time.Millisecond, 300 * time.Second, 0, time.Duration(math.MaxInt64)}[i]
		assert.Equal(t, want, c.Timeout, c.ID)
	}
}
