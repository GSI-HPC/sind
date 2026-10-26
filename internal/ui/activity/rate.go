// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"bytes"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/eventlog"
)

// RateWindow is how far back the rate of a span's bytes looks: the bytes
// it moved over the last RateWindow, by the times of its events.
const RateWindow = 5 * time.Second

// sample is the amount of a span at the time of one of its events.
type sample struct {
	at     time.Time
	amount int64
}

// spanKey names a span of a run.
type spanKey struct {
	run  string
	span progress.SpanID
}

// rates keeps the newest amounts of the spans of the runs that work in
// bytes, for their rates; a span is forgotten once it ends.
type rates map[spanKey][]sample

// take notes the amount of the span of a line of the log, if it works in
// bytes and the amount changed.
func (r rates) take(b []byte) {
	if !bytes.Contains(b, []byte(`"bytes"`)) {
		return
	}
	l, err := eventlog.Decode(b)
	if err != nil || l.Event.Unit != progress.UnitBytes {
		return
	}
	e := l.Event
	key := spanKey{l.RunID, e.Span}
	if e.Type == progress.TypeEnd {
		delete(r, key)
		return
	}
	s := r[key]
	if n := len(s); n > 0 && s[n-1].amount == e.Amount {
		return
	}
	s = append(s, sample{e.Time, e.Amount})
	// The oldest sample kept is the newest one from before the window,
	// which the rate is counted from.
	for len(s) > 2 && !s[1].at.After(e.Time.Add(-RateWindow)) {
		s = s[1:]
	}
	r[key] = s
}

// rate is the bytes a second that the span moved over the window that ends
// at its newest sample: 0 while it has fewer than two, and once that sample
// is older than the window at last, the time of the run's newest event, as
// the span has moved nothing since.
func (r rates) rate(run string, span progress.SpanID, last time.Time) float64 {
	s := r[spanKey{run, span}]
	if len(s) < 2 {
		return 0
	}
	first, newest := s[0], s[len(s)-1]
	if last.Sub(newest.at) > RateWindow {
		return 0
	}
	if from := newest.at.Add(-RateWindow); first.at.Before(from) {
		// The amount at the start of the window, on the line between the
		// samples on either side of it.
		next := s[1]
		first.amount += int64(float64(next.amount-first.amount) * float64(from.Sub(first.at)) / float64(next.at.Sub(first.at)))
		first.at = from
	}
	took := newest.at.Sub(first.at).Seconds()
	if took <= 0 {
		return 0
	}
	return float64(newest.amount-first.amount) / took
}

// forget drops the samples of the runs that keep does not hold.
func (r rates) forget(keep map[string]bool) {
	for k := range r {
		if !keep[k.run] {
			delete(r, k)
		}
	}
}
