// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The history of a span keeps a sample for each change of its amount or
// size, at least HistoryEvery apart: a sample sooner after the one before
// it is replaced by the next, so that the newest amount is always there.
// Its end stays, and so does the history, until the run goes.
func TestHistory(t *testing.T) {
	h := history{}
	key := spanKey{"a", progress.SpanID(3)}
	h.take(byteLine("a", "advance", 0, 0, "bytes"))
	h.take(byteLine("a", "advance", 100, 0, "bytes")) // no change
	h.take(byteLine("a", "advance", 200, 1_000, "bytes"))
	h.take(byteLine("a", "advance", 300, 2_000, "bytes")) // replaces the one before it
	h.take(byteLine("a", "advance", 600, 3_000, "items"))
	h.take(byteLine("a", "end", 700, 4_000, "bytes"))
	at := func(ms int64) time.Time { return rateT0.Add(time.Duration(ms) * time.Millisecond) }
	assert.Equal(t, []store.Sample{
		{At: at(0), Amount: 0, Size: 100_000_000},
		{At: at(300), Amount: 2_000, Size: 100_000_000},
		{At: at(600), Amount: 3_000, Size: 100_000_000},
		{At: at(700), Amount: 4_000, Size: 100_000_000},
	}, h[key])

	h.take([]byte(`{"unit":"bytes"}`))
	h.take(byteLine("b", "advance", 0, 1, ""))
	assert.Len(t, h, 1, "a line that cannot be read, or of no unit, is left out")

	h.forget(map[string]bool{"b": true})
	assert.Empty(t, h)
}

// A history longer than HistoryMax keeps every other sample, the newest
// always.
func TestHistoryThins(t *testing.T) {
	h := history{}
	for i := range int64(HistoryMax + 1) {
		h.take(byteLine("a", "advance", i*1000, i, "bytes"))
	}
	s := h[spanKey{"a", progress.SpanID(3)}]
	require.Len(t, s, HistoryMax/2+1)
	assert.EqualValues(t, 0, s[0].Amount)
	assert.EqualValues(t, 2, s[1].Amount)
	assert.EqualValues(t, HistoryMax, s[len(s)-1].Amount)
}

// The runs carry the history of each span, which a Reset forgets.
func TestReducerCarriesTheHistory(t *testing.T) {
	r := NewReducer(2)
	r.Take(byteLine("a", "start", 0, 0, "bytes"))
	r.Take(byteLine("a", "advance", 1000, 5, "bytes"))
	assert.NotEmpty(t, r.history)
	r.Reset()
	assert.Empty(t, r.history)
}
