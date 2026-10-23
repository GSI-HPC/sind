// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// version is the sind the runs of the demo say they were made by.
const version = "v0.11.0"

// traceID returns the trace the n-th run of the demo belongs to.
func traceID(n int) progress.TraceContext {
	return progress.TraceContext{Trace: progress.TraceID{0xde, 0x70, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}}
}

// uiTrace returns the trace of the n-th run of the demo as sind ui hands
// it to a command it starts: under a span of sind ui's, with the tracestate
// that marks the run as one of sind ui.
func uiTrace(n int) progress.TraceContext {
	tc := traceID(n)
	tc.Parent, tc.Flags, tc.State = progress.SpanID(0x5a<<56|n), 1, store.UITraceState
	return tc
}

// traceHex is the hexadecimal form of a trace's ID, as TRACEPARENT and the
// event log write it.
func traceHex(tc progress.TraceContext) string { return hex.EncodeToString(tc.Trace[:]) }

// runID returns the ID of the n-th run of the demo: 16 hexadecimal digits.
func runID(n int) string { return fmt.Sprintf("d3e0%012x", n) }

// recording is a run to record: the command, when it started, and what
// ends it.
type recording struct {
	id    string
	trace progress.TraceContext
	title string
	// target is what the command works on, as sind names it in the
	// command span's node; "" for nothing.
	target string
	start  time.Time
	// cancelAfter is when an interrupt ends the run, counted from its
	// start; zero for none.
	cancelAfter time.Duration
	// ui marks a run that a sind ui started, one that has no task now.
	ui   bool
	work func(ctx context.Context, p play) error
}

// targetOf returns the options that give a command span its target, as
// sind does.
func targetOf(target string) []progress.Option {
	if target == "" {
		return nil
	}
	return []progress.Option{progress.Node(target)}
}

// record plays the run on a clock that only the script moves, and returns
// the event log it wrote, with span IDs that repeat from run to run.
func record(r recording) []byte {
	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := &manualClock{t: r.start}
	if r.cancelAfter > 0 {
		clk.cancelAt, clk.cancel = r.start.Add(r.cancelAfter), cancel
	}
	log := progress.NewLog(&out, progress.LogOptions{Program: "sind", Version: version, Run: r.id, AdvanceEvery: -1})
	bus := progress.NewBus(progress.BusOptions{Sinks: []progress.Sink{log}, Now: clk.Now, Trace: r.trace, Program: "sind"})
	ctx, command := progress.Start(progress.WithBus(ctx, bus), progress.KindCommand, r.title, targetOf(r.target)...)
	command.End(r.work(ctx, play{clk: clk}))
	bus.Close()
	// The log writes to a buffer, which does not fail.
	_ = log.Close()
	return normalizeIDs(out.Bytes())
}

var (
	idKey = regexp.MustCompile(`"(span|parent)":"([0-9a-f]{16})"`)
	timeK = regexp.MustCompile(`"time":"([^"]+)"`)
)

// normalizeIDs replaces the random span IDs of a log by 0000000000000001
// and on, in the order they appear. The trace line keeps its parent: that is
// the caller's span.
func normalizeIDs(log []byte) []byte {
	ids := map[string]string{}
	lines := strings.Split(string(log), "\n")
	for i, line := range lines {
		if strings.Contains(line, `"type":"trace"`) {
			continue
		}
		lines[i] = idKey.ReplaceAllStringFunc(line, func(m string) string {
			sub := idKey.FindStringSubmatch(m)
			id, ok := ids[sub[2]]
			if !ok {
				id = fmt.Sprintf("%016x", len(ids)+1)
				ids[sub[2]] = id
			}
			return fmt.Sprintf(`"%s":"%s"`, sub[1], id)
		})
	}
	return []byte(strings.Join(lines, "\n"))
}

// cutAfter returns the lines of the log up to the time t: a run that is
// still going on at t. A line without a time, the trace line, stays.
func cutAfter(log []byte, t time.Time) []byte {
	var out []string
	for _, line := range strings.Split(strings.TrimSuffix(string(log), "\n"), "\n") {
		if m := timeK.FindStringSubmatch(line); m != nil {
			if at, err := time.Parse(time.RFC3339Nano, m[1]); err == nil && at.After(t) {
				break
			}
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n") + "\n")
}
