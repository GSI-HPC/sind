// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"fmt"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rateT0 is the time of the first line of the logs of these tests.
var rateT0 = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

// byteLine is a line of a log: an event of the type typ of the span #3 of
// the run r, at ms after rateT0, with amount, in the unit unit.
func byteLine(r, typ string, ms, amount int64, unit string) []byte {
	return fmt.Appendf(nil, `{"v":1,"run":%q,"trace":"4bf92f3577b34da6a3ce929d0e0e4736","seq":1,"time":%q,"type":%q,`+
		`"span":"0000000000000003","kind":"target","name":"img","state":"running","amount":%d,"size":100000000,"unit":%q}`,
		r, rateT0.Add(time.Duration(ms)*time.Millisecond).Format(time.RFC3339Nano), typ, amount, unit)
}

// The rate of a span is the bytes it moved over the last five seconds, by
// the times of its events, counted from the amount the line between two
// samples gives at the start of the window: 0 until a second sample, and
// once the newest is older than the window at the run's newest event.
func TestRates(t *testing.T) {
	r := rates{}
	span := progress.SpanID(3)
	r.take(byteLine("a", "advance", 0, 0, "bytes"))
	assert.Zero(t, r.rate("a", span, rateT0))
	r.take(byteLine("a", "advance", 1000, 10_000_000, "bytes"))
	assert.InDelta(t, 10e6, r.rate("a", span, rateT0), 1)
	r.take(byteLine("a", "advance", 1500, 10_000_000, "bytes")) // no change
	r.take(byteLine("a", "advance", 2000, 30_000_000, "bytes"))
	assert.InDelta(t, 15e6, r.rate("a", span, rateT0.Add(2*time.Second)), 1)
	for ms := int64(3000); ms <= 8000; ms += 1000 {
		r.take(byteLine("a", "advance", ms, 30_000_000+(ms-2000)*5_000, "bytes"))
	}
	assert.Len(t, r[spanKey{"a", span}], 6, "the samples from the start of the window")
	assert.InDelta(t, 5e6, r.rate("a", span, rateT0.Add(8*time.Second)), 1)
	r.take(byteLine("a", "advance", 8500, 62_500_000, "bytes"))
	assert.Len(t, r[spanKey{"a", span}], 7, "the newest sample before the window and those in it")
	assert.InDelta(t, 5e6, r.rate("a", span, rateT0.Add(8*time.Second)), 1)
	assert.Zero(t, r.rate("a", span, rateT0.Add(14*time.Second)), "stalled")
	assert.Zero(t, r.rate("b", span, rateT0), "another run")

	r.take(byteLine("a", "end", 9000, 100_000_000, "bytes"))
	assert.NotContains(t, r, spanKey{"a", span}, "forgotten at its end")
}

// Lines that are not of bytes, or that cannot be read, are left out.
func TestRatesLeftOut(t *testing.T) {
	r := rates{}
	r.take(byteLine("a", "advance", 0, 1, "items"))
	r.take([]byte(`{"unit":"bytes"}`))
	assert.Empty(t, r)
}

// Two samples at the same time give no rate.
func TestRatesSameTime(t *testing.T) {
	r := rates{}
	r.take(byteLine("a", "advance", 0, 1, "bytes"))
	r.take(byteLine("a", "advance", 0, 2, "bytes"))
	assert.Zero(t, r.rate("a", 3, rateT0))
}

// The Reducer gives each span of bytes its rate, forgets the samples of the
// runs it no longer keeps, and all of them when the log is read again.
func TestReducerRates(t *testing.T) {
	red := NewReducer(1)
	start := fmt.Appendf(nil, `{"v":1,"run":"a","trace":"4bf92f3577b34da6a3ce929d0e0e4736","seq":0,"time":%q,"type":"start","span":"0000000000000003","kind":"command","name":"create cluster","state":"running"}`,
		rateT0.Format(time.RFC3339Nano))
	red.Take(start)
	red.Take(byteLine("a", "advance", 0, 0, "bytes"))
	red.Take(byteLine("a", "advance", 2000, 20_000_000, "bytes"))
	runs := red.Runs()
	require.Len(t, runs, 1)
	assert.InDelta(t, 10e6, runs[0].Root.Rate, 1)

	red.Take([]byte(`{"v":1,"run":"b","trace":"5bf92f3577b34da6a3ce929d0e0e4736","seq":0,"time":"2026-10-14T09:31:00Z","type":"start","span":"0000000000000009","kind":"command","name":"delete cluster","state":"running"}`))
	red.Runs()
	assert.Empty(t, red.rates, "run a is no longer kept")

	red.Take(byteLine("b", "advance", 0, 0, "bytes"))
	red.Reset()
	assert.Empty(t, red.rates)
}
