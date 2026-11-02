// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// runFact is a line of the raw facts of a run.
type runFact struct {
	Key  string
	Val  string
	Mono bool
}

// runFacts is what the log says about the run itself, for a bug report.
type runFacts struct{ Facts []runFact }

// runFactsOf builds the raw facts.
func runFactsOf(rc runCtx) *runFacts {
	run := rc.Run
	f := &runFacts{}
	add := func(key, val string, mono bool) {
		if val != "" {
			f.Facts = append(f.Facts, runFact{key, val, mono})
		}
	}
	add("Run", run.ID, true)
	add("Trace", run.Trace, true)
	add("Caller span", run.Parent, true)
	add("Program", strings.TrimSpace(run.Program+" "+run.Version), false)
	add("Started", runStamp(run.Start), true)
	if run.End != nil {
		add("Ended", runStamp(*run.End), true)
	}
	add("Last event", runStamp(run.Last), true)
	add("Status", run.Status, false)
	add("Class", run.Class, false)
	if run.Root.Exit != nil {
		add("Exit code", fmt.Sprint(*run.Root.Exit), true)
	}
	add("Error", run.Err, true)
	total, hidden, lines, dropped := 0, 0, run.Root.Lines, run.Root.Dropped
	var count func(s store.Span)
	count = func(s store.Span) {
		for _, c := range s.Children {
			total++
			if c.Hidden {
				hidden++
			}
			lines += c.Lines
			dropped += c.Dropped
			count(c)
		}
	}
	count(run.Root)
	add("Spans", fmt.Sprintf("%d below the command, %d of them hidden", total, hidden), false)
	add("Line events", fmt.Sprintf("%d, %d not sent to the sinks", lines, dropped), false)
	return f
}

// runStamp writes a time in full, in the local time zone; "" for none.
func runStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05.000 MST")
}
