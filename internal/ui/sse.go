// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/GSI-HPC/go-clikit/localweb/session"
	"github.com/GSI-HPC/go-clikit/localweb/sse"
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
// localweb/sse does the streaming; this renders the fragments.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	frags, v, err := s.eventsRequest(r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	defer s.env.watch(fragmentTopics(frags, v.Params))()
	err = sse.Serve(w, r, s.env.Store.Notifier(), func(changed []store.Topic) ([]sse.Event, error) {
		return s.renderEvents(frags, v, changed)
	}, sse.Options{Coalesce: s.coalesce, KeepAlive: s.keepAlive, Done: s.ctx.Done()})
	if err != nil {
		s.Fail(w, r, err)
	}
}

// renderEvents renders the fragments of a stream from the current snapshot:
// all of them when changed is nil, else those that depend on a topic in
// changed. A fragment that fails to render after the first time is skipped
// and logged, so that the others go on.
func (s *Server) renderEvents(frags []Fragment, v *View, changed []store.Topic) ([]sse.Event, error) {
	snap := s.env.Store.Snapshot()
	var events []sse.Event
	for _, f := range frags {
		if changed != nil && !f.depends(changed, v.Params) {
			continue
		}
		fv := *v
		fv.Snap, fv.Now = snap, s.now()
		html, err := s.renderFragment(f, &fv)
		switch {
		case err == nil:
			events = append(events, sse.Event{Name: f.Name, Data: html})
		case changed == nil:
			return nil, err
		default:
			s.log.Error("rendering fragment", "fragment", f.Name, "err", err)
		}
	}
	return events, nil
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
	return s.tmpl.Render(f.Name, &fv)
}
