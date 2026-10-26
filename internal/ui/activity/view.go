// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// Counts are the targets below a span by how they stand: those that ended
// by their status, and those that have not by their state. A target that
// ended with a status the log does not name counts in Total alone.
type Counts struct {
	Total    int
	OK       int
	Failed   int
	Canceled int
	Skipped  int
	Running  int
	Queued   int
}

// Done is how many targets have ended with a status.
func (c Counts) Done() int { return c.OK + c.Failed + c.Canceled + c.Skipped }

// CountTargets counts the spans of kind target below s, at any depth, not
// s itself.
func CountTargets(s store.Span) Counts {
	var c Counts
	walk(s, func(t store.Span) {
		if t.Kind != KindTarget {
			return
		}
		c.Total++
		switch {
		case t.State == StateQueued:
			c.Queued++
		case t.State == StateRunning:
			c.Running++
		case t.Status == store.StatusOK:
			c.OK++
		case t.Status == store.StatusFailed:
			c.Failed++
		case t.Status == store.StatusCanceled:
			c.Canceled++
		case t.Status == store.StatusSkipped:
			c.Skipped++
		}
	})
	return c
}

// walk calls fn with each span below s, depth first, in start order.
func walk(s store.Span, fn func(store.Span)) {
	for _, c := range s.Children {
		fn(c)
		walk(c, fn)
	}
}

// VisibleChildren returns the children of s that a display shows: those
// without the hidden flag.
func VisibleChildren(s store.Span) []store.Span {
	var out []store.Span
	for _, c := range s.Children {
		if !c.Hidden {
			out = append(out, c)
		}
	}
	return out
}

// RevealAfter is how long a hidden span runs before a display shows it, as
// the terminal's tree of go-clikit does.
const RevealAfter = time.Second

// Reveal returns root with the hidden flag taken off each span below it,
// other than a call, that failed or has run for RevealAfter or longer by
// now, as the terminal shows a hidden step that takes long, such as the
// step "mesh" while docker pulls the images of the mesh. A call stays
// hidden: the terminal shows it only as what a target waits for. root
// itself is not changed.
func Reveal(root store.Span, now time.Time) store.Span {
	if len(root.Children) == 0 {
		return root
	}
	children := make([]store.Span, len(root.Children))
	for i, c := range root.Children {
		if c.Hidden && c.Kind != KindCall && (c.Status == store.StatusFailed || Elapsed(c, now) >= RevealAfter) {
			c.Hidden = false
		}
		children[i] = Reveal(c, now)
	}
	root.Children = children
	return root
}

// Failure is a group of failed targets that failed with the same error.
type Failure struct {
	// Err is the error text with the target's own name replaced by "{}",
	// as the terminal groups them: "node {}: timed out".
	Err string
	// Class is the class of the group's first target.
	Class string
	// Nodes are the targets' nodes, or their names for a target without
	// one, in the order they started.
	Nodes []string
}

// FailedTargets groups the targets below s that failed by their error
// text, in the order of each group's first target.
func FailedTargets(s store.Span) []Failure {
	var out []Failure
	walk(s, func(t store.Span) {
		if t.Kind != KindTarget || t.Status != store.StatusFailed {
			return
		}
		node := t.Node
		if node == "" {
			node = t.Name
		}
		err := maskName(t.Err, node)
		for i := range out {
			if out[i].Err == err {
				out[i].Nodes = append(out[i].Nodes, node)
				return
			}
		}
		out = append(out, Failure{Err: err, Class: t.Class, Nodes: []string{node}})
	})
	return out
}

// maskName replaces each whole occurrence of name in text with "{}": one
// that is not part of a longer name, such as worker-1 in worker-10.
func maskName(text, name string) string {
	if name == "" {
		return text
	}
	var b strings.Builder
	for {
		i := strings.Index(text, name)
		if i < 0 {
			break
		}
		end := i + len(name)
		if (i > 0 && isNameChar(text[i-1])) || (end < len(text) && isNameChar(text[end])) {
			b.WriteString(text[:i+1])
			text = text[i+1:]
			continue
		}
		b.WriteString(text[:i])
		b.WriteString("{}")
		text = text[end:]
	}
	b.WriteString(text)
	return b.String()
}

// isNameChar reports whether c can be part of a node's name.
func isNameChar(c byte) bool {
	return c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Elapsed is how long s has run: from when it began to run to its end, or
// to now while it goes on. A span that has not begun to run, as a queued
// one that ended without running, has run for no time.
func Elapsed(s store.Span, now time.Time) time.Duration {
	if s.Started.IsZero() {
		return 0
	}
	end := now
	if s.Ended != nil {
		end = *s.Ended
	}
	return max(end.Sub(s.Started), 0)
}

// Levels of the time a wait has used of its limit.
const (
	WaitOK       = "ok"
	WaitWarn     = "warn"
	WaitCritical = "crit"
)

// WaitUsed is the part of its limit, from 0 to 1, that the wait s has used
// by now. It is false for a span without a Timeout.
func WaitUsed(s store.Span, now time.Time) (float64, bool) {
	if s.Timeout <= 0 {
		return 0, false
	}
	return min(float64(Elapsed(s, now))/float64(s.Timeout), 1), true
}

// WaitLevel names how far a wait is: warn from half of its limit, critical
// from four fifths.
func WaitLevel(used float64) string {
	switch {
	case used >= 0.8:
		return WaitCritical
	case used >= 0.5:
		return WaitWarn
	}
	return WaitOK
}

// SlowestWait returns the wait below s that has used the most of its
// limit, and that part. It is false when no wait below s has a limit.
func SlowestWait(s store.Span, now time.Time) (store.Span, float64, bool) {
	var (
		slowest store.Span
		most    float64
		found   bool
	)
	walk(s, func(w store.Span) {
		if w.Kind != KindWait {
			return
		}
		if used, ok := WaitUsed(w, now); ok && (!found || used > most) {
			slowest, most, found = w, used, true
		}
	})
	return slowest, most, found
}
