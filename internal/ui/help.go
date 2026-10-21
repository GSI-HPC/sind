// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"net/http"
	"strings"

	"github.com/GSI-HPC/sind/internal/help"
)

// sind ui explains itself in layers, each opened by the user: hint cards on
// the words and buttons that pages mark, the help pane that ? opens, and the
// help entries of the jump box. The texts are those of sind help TOPIC, from
// package help; help.js does the rest in the browser.
//
// A page marks a word with data-word="ID" (class term-word), and an action's
// button with data-help="ACTION": hovering them shows a card, and ? opens
// the pane at that word or action.

func init() { helpRegister(defaultRegistry) }

// helpRegister adds the routes of the help pane and the jump box to r.
func helpRegister(r *Registry) {
	r.AddRoute(Route{Pattern: "GET /help", Handle: helpHandle})
	r.AddRoute(Route{Pattern: "GET /jump", Handle: jumpHandle})
}

// helpData is what the blocks of help.tmpl render from.
type helpData struct {
	// Kind is page, word, action, howto or search.
	Kind string
	// Crumb names the entry in the pane's header.
	Crumb string

	Page   help.Page
	Word   help.Word
	Action help.Action
	Howto  help.Howto

	// Words and Howtos are the entries that the shown one points at.
	Words  []help.Word
	Howtos []help.Howto
	// Doc is the URL of the docs page of the shown entry.
	Doc string

	Query string
	Hits  []helpHit
}

// helpHit is a result of the help search, with the topic that opens it.
type helpHit struct {
	help.Hit
	Topic string
	Label string
}

// helpTopic names an entry as the pane and the URLs do: "word/realm".
func helpTopic(k help.Kind, id string) string { return string(k) + "/" + id }

// helpKindLabel is how a hit's kind reads in lists.
func helpKindLabel(k help.Kind) string {
	if k == help.KindHowto {
		return "how to"
	}
	return string(k)
}

// helpHandle serves GET /help: with q the results of a search, else the
// entry that topic names, as the body of the pane, or with card=1 as a hint
// card. A topic is a path of sind ui (the help of the page at that path),
// KIND/ID (word, action or howto), or empty for the Overview.
func helpHandle(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r, "topic", "q", "card")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	h := help.Default()
	if q := strings.TrimSpace(v.Params.Get("q")); q != "" {
		d := &helpData{Kind: "search", Crumb: "Search", Query: q}
		for _, hit := range h.Search(q) {
			d.Hits = append(d.Hits, helpHit{hit, helpTopic(hit.Kind, hit.ID), helpKindLabel(hit.Kind)})
		}
		v.Data = d
		s.Write(w, r, http.StatusOK, "help-body", v)
		return
	}
	d, ok := helpEntry(h, v.Params.Get("topic"))
	if !ok {
		http.Error(w, "There is no help for this topic.", http.StatusNotFound)
		return
	}
	v.Data = d
	name := "help-body"
	if v.Params.Get("card") != "" {
		if d.Kind != "word" && d.Kind != "action" {
			http.Error(w, "Only words and actions have a hint card.", http.StatusNotFound)
			return
		}
		name = "help-card"
	}
	s.Write(w, r, http.StatusOK, name, v)
}

// helpEntry resolves topic to the data of its block.
func helpEntry(h *help.Help, topic string) (*helpData, bool) {
	d := &helpData{}
	words := func(ids []string) {
		for _, id := range ids {
			if w, ok := h.Word(id); ok {
				d.Words = append(d.Words, w)
			}
		}
	}
	switch {
	case topic == "" || strings.HasPrefix(topic, "/"):
		path := topic
		if path == "" {
			path = "/"
		}
		p, ok := h.PageFor(path)
		if !ok {
			p, _ = h.Page("overview")
		}
		d.Kind, d.Crumb, d.Page, d.Doc = "page", "This page", p, help.DocURL(p.Doc)
		words(p.Words)
		for _, id := range p.Howtos {
			if t, ok := h.Howto(id); ok {
				d.Howtos = append(d.Howtos, t)
			}
		}
	default:
		kind, id, _ := strings.Cut(topic, "/")
		switch help.Kind(kind) {
		case help.KindWord:
			w, ok := h.Word(id)
			if !ok {
				return nil, false
			}
			d.Kind, d.Crumb, d.Word, d.Doc = "word", "Word", w, help.DocURL(w.Doc)
			words(w.See)
		case help.KindAction:
			a, ok := h.Action(id)
			if !ok {
				return nil, false
			}
			d.Kind, d.Crumb, d.Action, d.Doc = "action", "Action", a, help.DocURL(a.Doc)
			words(a.Words)
		case help.KindHowto:
			t, ok := h.Howto(id)
			if !ok {
				return nil, false
			}
			d.Kind, d.Crumb, d.Howto, d.Doc = "howto", "How do I…", t, help.DocURL(t.Doc)
			words(t.Words)
		default:
			return nil, false
		}
	}
	return d, true
}
