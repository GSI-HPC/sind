// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"bytes"
	"time"

	"github.com/GSI-HPC/go-clikit/progress/eventlog"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// HistoryEvery is the least time between two samples that the history of a
// span keeps, but for the newest: a sample that came sooner after the one
// before it is replaced by the next.
const HistoryEvery = 250 * time.Millisecond

// HistoryMax is the most samples the history of a span keeps; a longer one
// keeps every other sample, the newest always.
const HistoryMax = 1200

// history keeps the amounts and the sizes of the spans of the runs that
// work in a unit, from their first event to their end, so that a page can
// draw how fast a span worked at any time, not only now.
type history map[spanKey][]store.Sample

// take notes the amount and the size of the span of a line of the log, if
// it works in a unit.
func (h history) take(b []byte) {
	if !bytes.Contains(b, []byte(`"unit"`)) {
		return
	}
	l, err := eventlog.Decode(b)
	if err != nil || l.Event.Unit == 0 {
		return
	}
	e := l.Event
	key := spanKey{l.RunID, e.Span}
	s := h[key]
	n := len(s)
	next := store.Sample{At: e.Time, Amount: e.Amount, Size: e.Size}
	switch {
	case n > 0 && s[n-1].Amount == next.Amount && s[n-1].Size == next.Size:
		return
	case n > 1 && s[n-1].At.Sub(s[n-2].At) < HistoryEvery:
		s[n-1] = next
	default:
		s = append(s, next)
	}
	if len(s) > HistoryMax {
		thin := s[:0]
		for i := range s {
			if i%2 == 0 || i == len(s)-1 {
				thin = append(thin, s[i])
			}
		}
		s = thin
	}
	h[key] = s
}

// forget drops the samples of the runs that keep does not hold.
func (h history) forget(keep map[string]bool) {
	for k := range h {
		if !keep[k.run] {
			delete(h, k)
		}
	}
}
