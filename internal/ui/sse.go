// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb/session"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// maxFragments is the most fragments one stream may name.
const maxFragments = 32

// events streams a page's live updates as Server-Sent Events: each event
// is named after a fragment and carries its HTML, which htmx's SSE
// extension swaps into the element with that sse-swap name. The query
// names the fragments (f), the lit rail entry (nav) and the Params that
// the fragments declare.
//
// The stream opens with every fragment, as the page may have been rendered
// before a change the stream does not hear of. Then changes gather for
// coalesceEvery, and each fragment that depends on a changed topic is
// rendered, and sent when its HTML differs from what the stream sent last.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	// Subscribe before the baseline snapshot, so that no change falls
	// between them.
	changes, unsubscribe := s.env.Store.Subscribe()
	defer unsubscribe()
	frags, v, err := s.eventsRequest(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	defer s.env.watch(fragmentTopics(frags, v.Params))()
	sent := map[string][sha256.Size]byte{}
	first := make([]string, len(frags))
	for i, f := range frags {
		html, err := s.renderFragment(f, v)
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		first[i] = html
		sent[f.Name] = sha256.Sum256([]byte(html))
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// The browser reconnects after this long when the stream breaks.
	_, _ = io.WriteString(w, "retry: 2000\n\n")
	for i, f := range frags {
		if err := writeEvent(w, f.Name, first[i]); err != nil {
			return
		}
	}
	flusher.Flush()

	pending := map[store.Topic]bool{}
	var timer <-chan time.Time
	keepAlive := time.NewTicker(s.keepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case t := <-changes:
			pending[t] = true
			if timer == nil {
				timer = time.After(s.coalesce)
			}
			continue
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			continue
		case <-timer:
		}
		timer = nil
		snap := s.env.Store.Snapshot()
		for _, f := range frags {
			if !f.depends(pending, v.Params) {
				continue
			}
			fv := *v
			fv.Snap, fv.Now = snap, s.now()
			html, err := s.renderFragment(f, &fv)
			if err != nil {
				s.log.Error("rendering fragment", "fragment", f.Name, "err", err)
				continue
			}
			sum := sha256.Sum256([]byte(html))
			if sum == sent[f.Name] {
				continue
			}
			sent[f.Name] = sum
			if err := writeEvent(w, f.Name, html); err != nil {
				return
			}
		}
		clear(pending)
		flusher.Flush()
	}
}

// fragmentTopics returns the exact topics that frags depend on for the
// Params p, each once, in the order the fragments name them.
func fragmentTopics(frags []Fragment, p Params) []store.Topic {
	var out []store.Topic
	add := func(ts ...store.Topic) {
		for _, t := range ts {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	for _, f := range frags {
		add(f.Topics...)
		if f.Watch != nil {
			add(f.Watch(p)...)
		}
	}
	return out
}

// eventsRequest checks the query of /events and returns the fragments it
// names and the view they render from. The view has the Params that the
// fragments declare, and no others.
func (s *Server) eventsRequest(r *http.Request) ([]Fragment, *View, error) {
	q := r.URL.Query()
	names := slices.Compact(slices.Sorted(slices.Values(q["f"])))
	switch {
	case len(names) == 0:
		return nil, nil, fmt.Errorf("no fragments named: %w", ErrBadRequest)
	case len(names) > maxFragments:
		return nil, nil, fmt.Errorf("more than %d fragments named: %w", maxFragments, ErrBadRequest)
	}
	p := Params{}
	frags := make([]Fragment, len(names))
	for i, n := range names {
		f, ok := s.reg.fragments[n]
		if !ok {
			return nil, nil, fmt.Errorf("unknown fragment: %w", ErrBadRequest)
		}
		frags[i] = f
		for _, k := range f.Params {
			if vals, ok := q[k]; ok {
				p[k] = slices.Clone(vals)
			}
		}
	}
	if err := checkParams(p); err != nil {
		return nil, nil, err
	}
	nav := q.Get("nav")
	if len(nav) > maxParamLen {
		return nil, nil, fmt.Errorf("the value of nav is longer than %d bytes: %w", maxParamLen, ErrBadRequest)
	}
	v := s.newView(p)
	v.Nav, v.CSRF = nav, session.CSRF(r.Context())
	return frags, v, nil
}

// renderFragment renders f from a copy of v.
func (s *Server) renderFragment(f Fragment, v *View) (string, error) {
	fv := *v
	if err := s.load(f, &fv); err != nil {
		return "", err
	}
	return s.tmpl.render(f.Name, &fv)
}

// writeEvent writes one Server-Sent Event, a data line for each line of
// the HTML in data. A carriage return also ends a line of the stream, so it
// is written as a character reference, which HTML reads the same.
func writeEvent(w io.Writer, name, data string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "event: %s\n", name)
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r", "&#13;"), "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	_, err := io.WriteString(w, b.String())
	return err
}
