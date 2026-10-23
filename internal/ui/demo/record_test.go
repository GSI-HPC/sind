// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRecord_Repeats(t *testing.T) {
	rec := recording{
		id: runID(3), trace: traceID(3), title: "create cluster", start: t0,
		work: create(createSpec{image: "i", mesh: true, nodes: nodeSet("dev", 14*time.Second, 19*time.Second)}),
	}
	first, second := record(rec), record(rec)
	assert.Equal(t, string(first), string(second), "the same bytes: random span IDs are replaced")
	assert.Contains(t, string(first), `"run":"`+runID(3)+`"`)
	assert.Contains(t, string(first), `"trace":"`+traceHex(traceID(3))+`"`)
	assert.Contains(t, string(first), `"span":"0000000000000001"`)
}

func TestIDs(t *testing.T) {
	assert.Len(t, runID(7), 16)
	assert.NotEqual(t, runID(1), runID(2))
	assert.Len(t, traceHex(traceID(5)), 32)
	assert.NotEqual(t, traceHex(traceID(1)), traceHex(traceID(2)))
}

func TestUITrace_MarksTheRunAsOneOfSindUI(t *testing.T) {
	rec := recording{id: runID(4), trace: uiTrace(4), title: "create worker", start: t0,
		work: func(context.Context, play) error { return nil }}
	log := string(record(rec))
	assert.Equal(t, traceHex(traceID(4)), traceHex(uiTrace(4)), "the same trace")
	assert.Contains(t, log, `"traceState":"sind=ui"`)
	assert.Contains(t, log, `"parent":"5a00000000000004"`)
}

func TestNormalizeIDs_KeepsTheTraceParent(t *testing.T) {
	log := `{"v":1,"type":"trace","run":"r","parent":"00f067aa0ba902b7"}
{"v":1,"type":"start","run":"r","span":"aaaaaaaaaaaaaaaa","parent":"bbbbbbbbbbbbbbbb"}
{"v":1,"type":"end","run":"r","span":"aaaaaaaaaaaaaaaa"}
`
	got := string(normalizeIDs([]byte(log)))
	assert.Contains(t, got, `"parent":"00f067aa0ba902b7"`)
	assert.Contains(t, got, `"span":"0000000000000001","parent":"0000000000000002"`)
	assert.Contains(t, got, `"span":"0000000000000001"}`, "the same span, the same ID")
}

func TestCutAfter(t *testing.T) {
	log := record(recording{
		id: runID(1), trace: traceID(1), title: "x", start: t0,
		work: func(ctx context.Context, p play) error { return p.generic(ctx, "step") },
	})
	cut := cutAfter(log, t0.Add(500*time.Millisecond))
	r := reduceLog(t, cut)
	assert.Nil(t, r.End, "the run was still going at that time")
	assert.Less(t, len(cut), len(log))
	assert.True(t, strings.HasSuffix(string(cut), "\n"))
	assert.Contains(t, string(cut), `"type":"trace"`, "the line without a time stays")

	full := cutAfter(log, t0.Add(time.Hour))
	assert.NotNil(t, reduceLog(t, full).End)
}

func TestCutAfter_BadTime(t *testing.T) {
	log := []byte(`{"time":"not a time"}` + "\n" + `{"time":"2026-10-14T09:30:00Z"}` + "\n")
	assert.Equal(t, string(log), string(cutAfter(log, t0)), "a line whose time does not parse stays")
}
