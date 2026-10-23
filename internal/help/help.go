// SPDX-License-Identifier: LGPL-3.0-or-later

// Package help holds the help texts of sind: what its words mean, what each
// action of sind ui does, what each page shows and how to do common things.
// The texts are YAML files in this directory, embedded in the binary. sind
// help TOPIC prints them and sind ui shows them as hint cards, in its help
// pane and in the jump box, so that both say the same.
//
// Load checks the files for the faults that a program cannot repair: an
// empty field, an id twice, a reference to an id that does not exist. The
// tests of this package and of cmd/sind check the rest: that every action
// and page of sind ui has an entry, that each docs link exists and that each
// command in the texts is one that sind has.
package help

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed words.yaml actions.yaml pages.yaml howtos.yaml
var files embed.FS

// DocsURL is where the Hugo docs of sind are published; the doc fields are
// paths below its root.
const DocsURL = "https://gsi-hpc.github.io/sind/"

// Kind is what a topic is.
type Kind string

// The kinds of topics.
const (
	KindWord   Kind = "word"
	KindAction Kind = "action"
	KindPage   Kind = "page"
	KindHowto  Kind = "howto"
)

// Word is a term that sind and sind ui use.
type Word struct {
	ID      string   `yaml:"id"`
	Title   string   `yaml:"title"`
	Summary string   `yaml:"summary"`
	Command string   `yaml:"command"`
	Doc     string   `yaml:"doc"`
	See     []string `yaml:"see"`
}

// Action is a button of sind ui that changes something.
type Action struct {
	ID      string `yaml:"id"`
	Title   string `yaml:"title"`
	Summary string `yaml:"summary"`
	// Command is the sind command that the action runs, with placeholders
	// in capitals.
	Command string `yaml:"command"`
	// Undo says how to take the action back, or that nobody can.
	Undo string `yaml:"undo"`
	// Irreversible marks an action that cannot be undone.
	Irreversible bool     `yaml:"irreversible"`
	Doc          string   `yaml:"doc"`
	Words        []string `yaml:"words"`
}

// UndoText says how to take the action back, as a sentence that says so
// ("To undo: delete the workers."), or that nobody can.
func (a Action) UndoText() string {
	if a.Irreversible || a.Undo == "" {
		return a.Undo + "."
	}
	return "To undo: " + strings.ToLower(a.Undo[:1]) + a.Undo[1:] + "."
}

// Page is a page of sind ui.
type Page struct {
	ID      string `yaml:"id"`
	Title   string `yaml:"title"`
	Summary string `yaml:"summary"`
	// Route is the path of the page, with {wildcards}.
	Route  string   `yaml:"route"`
	Doc    string   `yaml:"doc"`
	Words  []string `yaml:"words"`
	Howtos []string `yaml:"howtos"`
}

// Howto is a way to reach a goal, step by step.
type Howto struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Summary  string   `yaml:"summary"`
	Steps    []string `yaml:"steps"`
	Commands []string `yaml:"commands"`
	Doc      string   `yaml:"doc"`
	Words    []string `yaml:"words"`
}

// Hit is an entry that a search finds.
type Hit struct {
	Kind    Kind
	ID      string
	Title   string
	Summary string
}

// Help is the set of all help texts.
type Help struct {
	words   []Word
	actions []Action
	pages   []Page
	howtos  []Howto
}

var (
	defaultOnce sync.Once
	defaultHelp *Help
)

// Default returns the embedded texts, loaded once. It panics when they are
// faulty, which a test of this package prevents.
func Default() *Help {
	defaultOnce.Do(func() {
		defaultHelp = mustLoad(files)
	})
	return defaultHelp
}

// mustLoad is Load for texts that are embedded: a fault is a program error.
func mustLoad(fsys fs.FS) *Help {
	h, err := Load(fsys)
	if err != nil {
		panic(fmt.Sprintf("sind help texts: %v", err))
	}
	return h
}

// Load reads words.yaml, actions.yaml, pages.yaml and howtos.yaml from fsys
// and checks them.
func Load(fsys fs.FS) (*Help, error) {
	h := &Help{}
	for name, dst := range map[string]any{
		"words.yaml": &h.words, "actions.yaml": &h.actions,
		"pages.yaml": &h.pages, "howtos.yaml": &h.howtos,
	} {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(dst); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := h.check(); err != nil {
		return nil, err
	}
	return h, nil
}

var idRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// check returns every fault of the texts, joined.
func (h *Help) check() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	empty := func(where string, fields map[string]string, lists map[string][]string) {
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			if strings.TrimSpace(fields[name]) == "" {
				fail("%s: %s is empty", where, name)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(lists)) {
			for i, s := range lists[name] {
				if strings.TrimSpace(s) == "" {
					fail("%s: %s %d is empty", where, name, i+1)
				}
			}
		}
	}
	seen := map[Kind]map[string]bool{}
	add := func(k Kind, id string) string {
		where := fmt.Sprintf("%s %q", k, id)
		if seen[k] == nil {
			seen[k] = map[string]bool{}
		}
		switch {
		case !idRE.MatchString(id):
			fail("%s: the id must be lower case, digits and hyphens", where)
		case seen[k][id]:
			fail("%s: the id is used twice", where)
		}
		seen[k][id] = true
		return where
	}
	for _, w := range h.words {
		where := add(KindWord, w.ID)
		empty(where, map[string]string{"title": w.Title, "summary": w.Summary}, map[string][]string{"see": w.See})
	}
	for _, a := range h.actions {
		where := add(KindAction, a.ID)
		empty(where, map[string]string{"title": a.Title, "summary": a.Summary, "command": a.Command, "undo": a.Undo, "doc": a.Doc},
			map[string][]string{"words": a.Words})
	}
	for _, p := range h.pages {
		where := add(KindPage, p.ID)
		empty(where, map[string]string{"title": p.Title, "summary": p.Summary, "route": p.Route, "doc": p.Doc},
			map[string][]string{"words": p.Words, "howtos": p.Howtos})
		if !strings.HasPrefix(p.Route, "/") {
			fail("%s: the route must start with /", where)
		}
	}
	for _, t := range h.howtos {
		where := add(KindHowto, t.ID)
		empty(where, map[string]string{"title": t.Title, "summary": t.Summary, "doc": t.Doc},
			map[string][]string{"steps": t.Steps, "commands": t.Commands, "words": t.Words})
		if len(t.Steps) == 0 {
			fail("%s: it has no steps", where)
		}
	}
	// The topics of sind help are words, actions and how-tos: one namespace.
	owner := map[string]Kind{}
	for _, k := range []Kind{KindWord, KindAction, KindHowto} {
		for _, id := range slices.Sorted(maps.Keys(seen[k])) {
			if o, ok := owner[id]; ok {
				fail("%s %q: the id is also a %s", k, id, o)
			}
			owner[id] = k
		}
	}
	ref := func(where string, k Kind, ids []string) {
		for _, id := range ids {
			if !seen[k][id] {
				fail("%s: %s %q does not exist", where, k, id)
			}
		}
	}
	for _, w := range h.words {
		ref(fmt.Sprintf("word %q", w.ID), KindWord, w.See)
	}
	for _, a := range h.actions {
		ref(fmt.Sprintf("action %q", a.ID), KindWord, a.Words)
	}
	for _, p := range h.pages {
		ref(fmt.Sprintf("page %q", p.ID), KindWord, p.Words)
		ref(fmt.Sprintf("page %q", p.ID), KindHowto, p.Howtos)
	}
	for _, t := range h.howtos {
		ref(fmt.Sprintf("howto %q", t.ID), KindWord, t.Words)
	}
	return errors.Join(errs...)
}

// Words returns all words, in file order.
func (h *Help) Words() []Word { return slices.Clone(h.words) }

// Actions returns all actions, in file order.
func (h *Help) Actions() []Action { return slices.Clone(h.actions) }

// Pages returns all pages, in file order.
func (h *Help) Pages() []Page { return slices.Clone(h.pages) }

// Howtos returns all how-tos, in file order.
func (h *Help) Howtos() []Howto { return slices.Clone(h.howtos) }

// Word returns the word with id.
func (h *Help) Word(id string) (Word, bool) {
	i := slices.IndexFunc(h.words, func(w Word) bool { return w.ID == id })
	if i < 0 {
		return Word{}, false
	}
	return h.words[i], true
}

// Action returns the action with id.
func (h *Help) Action(id string) (Action, bool) {
	i := slices.IndexFunc(h.actions, func(a Action) bool { return a.ID == id })
	if i < 0 {
		return Action{}, false
	}
	return h.actions[i], true
}

// Page returns the page with id.
func (h *Help) Page(id string) (Page, bool) {
	i := slices.IndexFunc(h.pages, func(p Page) bool { return p.ID == id })
	if i < 0 {
		return Page{}, false
	}
	return h.pages[i], true
}

// Howto returns the how-to with id.
func (h *Help) Howto(id string) (Howto, bool) {
	i := slices.IndexFunc(h.howtos, func(t Howto) bool { return t.ID == id })
	if i < 0 {
		return Howto{}, false
	}
	return h.howtos[i], true
}

// PageFor returns the page that serves the URL path, such as
// /clusters/sind/dev. A route's {wildcard} matches one segment.
func (h *Help) PageFor(path string) (Page, bool) {
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for _, p := range h.pages {
		want := strings.Split(strings.Trim(p.Route, "/"), "/")
		if len(want) != len(segs) {
			continue
		}
		ok := true
		for i, w := range want {
			wild := strings.HasPrefix(w, "{") && strings.HasSuffix(w, "}")
			if wild && segs[i] == "" || !wild && w != segs[i] {
				ok = false
				break
			}
		}
		if ok {
			return p, true
		}
	}
	return Page{}, false
}

// Topic returns the kind of the word, action or how-to with id, the topics
// of sind help.
func (h *Help) Topic(id string) (Kind, bool) {
	if _, ok := h.Word(id); ok {
		return KindWord, true
	}
	if _, ok := h.Action(id); ok {
		return KindAction, true
	}
	if _, ok := h.Howto(id); ok {
		return KindHowto, true
	}
	return "", false
}

// Commands returns every command line in the texts, for the tests that
// check them against the command tree.
func (h *Help) Commands() []string {
	var out []string
	for _, w := range h.words {
		if w.Command != "" {
			out = append(out, w.Command)
		}
	}
	for _, a := range h.actions {
		out = append(out, a.Command)
	}
	for _, t := range h.howtos {
		out = append(out, t.Commands...)
	}
	return out
}

// DocURL returns the URL of a doc path, such as usage/progress#event-log.
func DocURL(doc string) string { return docURL(doc, "") }

// UIDocURL returns the URL of a doc path for a reader of sind ui: the page
// then shows the steps for the web UI, and keeps showing them.
func UIDocURL(doc string) string { return docURL(doc, "?via=ui") }

// docURL returns the URL of a doc path, with the query before the fragment.
func docURL(doc, query string) string {
	path, frag, hasFrag := strings.Cut(doc, "#")
	u := DocsURL + path + "/" + query
	if hasFrag {
		u += "#" + frag
	}
	return u
}

// Search finds the entries that match every word of q, best first: a title
// that starts with the query, then a title that holds it, then the rest.
// Pages are not searched, as the jump box lists them itself.
func (h *Help) Search(q string) []Hit {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	terms := strings.Fields(q)
	type scored struct {
		Hit
		score int
	}
	var found []scored
	consider := func(k Kind, id, title, summary, extra string) {
		title = strings.TrimSpace(title)
		hay := strings.ToLower(id + " " + title + " " + summary + " " + extra)
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				return
			}
		}
		score := 2
		switch lt := strings.ToLower(title); {
		case strings.HasPrefix(lt, q) || id == q:
			score = 0
		case strings.Contains(lt, q):
			score = 1
		}
		found = append(found, scored{Hit{k, id, title, summary}, score})
	}
	for _, w := range h.words {
		consider(KindWord, w.ID, w.Title, w.Summary, "")
	}
	for _, t := range h.howtos {
		consider(KindHowto, t.ID, t.Title, t.Summary, strings.Join(t.Steps, " ")+" "+strings.Join(t.Commands, " "))
	}
	for _, a := range h.actions {
		consider(KindAction, a.ID, a.Title, a.Summary, a.Command)
	}
	slices.SortStableFunc(found, func(a, b scored) int { return a.score - b.score })
	var out []Hit
	for _, f := range found {
		out = append(out, f.Hit)
	}
	return out
}
