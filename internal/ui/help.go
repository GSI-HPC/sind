// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/GSI-HPC/go-clikit/localweb/page"
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
//
// GET /help answers help.js, which asks with the header HX-Request, with
// the part that the pane or a card shows. Opened in the browser, as a link
// or a bookmark does, it is a page of its own in the layout.

func init() { helpRegister(defaultRegistry) }

// helpRegister adds the routes of the help pane and the jump box to r.
func helpRegister(r *Registry) {
	r.AddRoute(Route{Pattern: "GET /help", Handle: helpHandle})
	r.AddRoute(Route{Pattern: "GET /jump", Handle: jumpHandle})
}

// helpData is what the blocks of help.tmpl render from.
type helpData struct {
	// Kind is page, word, action, howto, words (the glossary), keys, index
	// or search.
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

	// Keys are the groups of keys, for Kind keys and page.
	Keys []helpKeyGroup
}

// helpKey is a key, or keys pressed one after the other or together, and
// what it does. Single marks the shortcuts of one key, which the user can
// turn off.
type helpKey struct {
	Keys []string
	// Join is how the keys go together: "then", "+" or "or".
	Join   string
	Text   string
	Single bool
}

// helpKeyGroup is a group of keys, under a heading.
type helpKeyGroup struct {
	Title string
	Keys  []helpKey
}

// helpKeys returns every key of sind ui, by where it works.
func helpKeys() []helpKeyGroup {
	all := helpKeyGroup{Title: "Everywhere", Keys: []helpKey{
		{Keys: []string{"/"}, Text: "Jump to a cluster, node, page or help. On Clusters and Nodes: filter the list.", Single: true},
		{Keys: []string{"Ctrl", "K"}, Join: "+", Text: "Jump, also when single-key shortcuts are off. On a Mac: ⌘ K."},
		{Keys: []string{"?"}, Text: "Help for the item under the pointer or with the focus, else for the open dialog or the page", Single: true},
	}}
	for _, it := range navItems {
		all.Keys = append(all.Keys, helpKey{Keys: []string{"g", it.Chord}, Join: "then", Text: "Go to " + it.Label, Single: true})
	}
	all.Keys = append(all.Keys, helpKey{Keys: []string{"Esc"}, Text: "Close the hint card, the jump box, the help pane or the dialog. A dialog with changes asks first."})
	return []helpKeyGroup{all,
		{Title: "In a dialog", Keys: []helpKey{
			{Keys: []string{"Ctrl", "Enter"}, Join: "+", Text: "Run the command of the dialog. On a Mac: ⌘ Enter."},
		}},
		{Title: "In the jump box", Keys: []helpKey{
			{Keys: []string{"↑", "↓"}, Join: "or", Text: "Move through the results"},
			{Keys: []string{"Enter"}, Text: "Open the result, or pick the cluster of an action"},
			{Keys: []string{"Backspace"}, Text: "In an empty box: back from the choice of a cluster"},
		}},
		{Title: "In the help pane", Keys: []helpKey{
			{Keys: []string{"Tab"}, Text: "Move between the search, the links and the buttons"},
			{Keys: []string{"Esc"}, Text: "Close the pane and go back to where you were"},
		}},
	}
}

// helpPageKeys are the keys that the help of every page lists: jump, help,
// run a dialog and close.
func helpPageKeys() []helpKeyGroup {
	all := helpKeys()
	g, dialog := all[0], all[1]
	return []helpKeyGroup{{Title: "Keys", Keys: []helpKey{g.Keys[0], g.Keys[1], g.Keys[2], dialog.Keys[0], g.Keys[len(g.Keys)-1]}}}
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
// KIND/ID (word, action or howto), words, keys, index, or empty for the
// Overview. A request that is not help.js's gets the help page, with the
// index when it names no topic.
func helpHandle(s *Server, w http.ResponseWriter, r *http.Request) {
	v, err := s.View(r, "topic", "q", "card")
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	h := help.Default()
	full := !page.IsHTMX(r)
	d := helpSearch(h, v.Params.Get("q"))
	if d == nil {
		topic := v.Params.Get("topic")
		if full && topic == "" {
			topic = "index"
		}
		var ok bool
		if d, ok = helpEntry(h, topic); !ok {
			http.Error(w, "There is no help for this topic.", http.StatusNotFound)
			return
		}
	}
	if full {
		pv := v.withData(d)
		pv.Title, pv.Content = "Help", "page-help"
		pv.Events = s.eventsURL(nil, pv)
		s.Write(w, r, http.StatusOK, "layout", pv)
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

// helpSearch returns the results of a search of help for q, or nil for an
// empty q.
func helpSearch(h *help.Help, q string) *helpData {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	d := &helpData{Kind: "search", Crumb: "Search", Query: q}
	for _, hit := range h.Search(q) {
		d.Hits = append(d.Hits, helpHit{hit, helpTopic(hit.Kind, hit.ID), helpKindLabel(hit.Kind)})
	}
	return d
}

// helpGlossary returns every word, by title.
func helpGlossary(h *help.Help) []help.Word {
	words := h.Words()
	slices.SortStableFunc(words, func(a, b help.Word) int {
		return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	return words
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
	case topic == "words":
		d.Kind, d.Crumb, d.Words, d.Doc = "words", "Words", helpGlossary(h), help.UIDocURL("web-ui")
	case topic == "keys":
		d.Kind, d.Crumb, d.Keys, d.Doc = "keys", "Keys", helpKeys(), help.UIDocURL("web-ui#keys")
	case topic == "index":
		d.Kind, d.Crumb, d.Howtos, d.Doc = "index", "All help", h.Howtos(), help.UIDocURL("web-ui")
	case topic == "" || strings.HasPrefix(topic, "/"):
		path := topic
		if path == "" {
			path = "/"
		}
		p, ok := h.PageFor(path)
		if !ok {
			p, _ = h.Page("overview")
		}
		d.Kind, d.Crumb, d.Page, d.Doc, d.Keys = "page", "This page", p, help.UIDocURL(p.Doc), helpPageKeys()
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
			d.Kind, d.Crumb, d.Word, d.Doc = "word", "Word", w, help.UIDocURL(w.Doc)
			words(w.See)
		case help.KindAction:
			a, ok := h.Action(id)
			if !ok {
				return nil, false
			}
			d.Kind, d.Crumb, d.Action, d.Doc = "action", "Action", a, help.UIDocURL(a.Doc)
			words(a.Words)
		case help.KindHowto:
			t, ok := h.Howto(id)
			if !ok {
				return nil, false
			}
			d.Kind, d.Crumb, d.Howto, d.Doc = "howto", "How do I…", t, help.UIDocURL(t.Doc)
			words(t.Words)
		default:
			return nil, false
		}
	}
	return d, true
}
