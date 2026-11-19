// SPDX-License-Identifier: LGPL-3.0-or-later

package help

import (
	"fmt"
	"slices"
	"strings"
)

// width is the line length of the text that sind help prints.
const width = 78

// Text returns the topic id as plain text for a terminal, as sind help ID
// prints it.
func (h *Help) Text(id string) (string, bool) {
	k, ok := h.Topic(id)
	if !ok {
		return "", false
	}
	var b strings.Builder
	switch k {
	case KindWord:
		w, _ := h.Word(id)
		heading(&b, w.Title, "word")
		paragraph(&b, w.Summary)
		terminal(&b, w.Command)
		h.related(&b, w.See)
		more(&b, w.Doc)
	case KindAction:
		a, _ := h.Action(id)
		heading(&b, a.Title, "action of sind ui")
		paragraph(&b, a.Summary)
		paragraph(&b, a.UndoText())
		terminal(&b, a.Command)
		h.related(&b, a.Words)
		more(&b, a.Doc)
	default:
		t, _ := h.Howto(id)
		heading(&b, t.Title, "how to")
		paragraph(&b, t.Summary)
		for i, s := range t.Steps {
			b.WriteString(wrap(fmt.Sprintf("%d. %s", i+1, s), "", "   "))
		}
		b.WriteString("\n")
		terminal(&b, t.Commands...)
		h.related(&b, t.Words)
		more(&b, t.Doc)
	}
	return strings.TrimRight(b.String(), "\n") + "\n", true
}

// Index returns the list of topics that sind help can print: every topic
// but those for which skip, unless nil, reports true. sind help prints the
// help of a command for a topic of the command's name, so it skips those.
// The titles line up in one column, after the longest id.
func (h *Help) Index(skip func(id string) bool) string {
	type section struct {
		title string
		rows  [][2]string
	}
	var sections []section
	add := func(title string, row func(i int) [2]string, n int) {
		s := section{title: title}
		for i := range n {
			if r := row(i); skip == nil || !skip(r[0]) {
				s.rows = append(s.rows, r)
			}
		}
		sections = append(sections, s)
	}
	add("How to", func(i int) [2]string { return [2]string{h.howtos[i].ID, h.howtos[i].Title} }, len(h.howtos))
	add("Words", func(i int) [2]string { return [2]string{h.words[i].ID, h.words[i].Title} }, len(h.words))
	add("Actions of sind ui", func(i int) [2]string { return [2]string{h.actions[i].ID, h.actions[i].Title} }, len(h.actions))
	idWidth := 0
	for _, s := range sections {
		for _, r := range s.rows {
			idWidth = max(idWidth, len(r[0]))
		}
	}
	var b strings.Builder
	b.WriteString("Topics of sind help. Run sind help TOPIC to read one.\n")
	for _, s := range sections {
		fmt.Fprintf(&b, "\n%s:\n", s.title)
		for _, r := range s.rows {
			fmt.Fprintf(&b, "  %-*s %s\n", idWidth, r[0], r[1])
		}
	}
	return b.String()
}

func heading(b *strings.Builder, title, kind string) {
	fmt.Fprintf(b, "%s (%s)\n\n", title, kind)
}

func paragraph(b *strings.Builder, s string) {
	b.WriteString(wrap(s, "", ""))
	b.WriteString("\n")
}

func terminal(b *strings.Builder, cmds ...string) {
	cmds = slices.DeleteFunc(slices.Clone(cmds), func(c string) bool { return c == "" })
	if len(cmds) == 0 {
		return
	}
	b.WriteString("In a terminal:\n")
	for _, c := range cmds {
		fmt.Fprintf(b, "  $ %s\n", c)
	}
	b.WriteString("\n")
}

func (h *Help) related(b *strings.Builder, ids []string) {
	if len(ids) == 0 {
		return
	}
	b.WriteString("Related: " + strings.Join(ids, ", ") + "\n\n")
}

// more links the doc page doc, unless there is none.
func more(b *strings.Builder, doc string) {
	if doc == "" {
		return
	}
	fmt.Fprintf(b, "More in the docs: %s\n", DocURL(doc))
}

// wrap breaks s into lines of at most width characters; the first line
// starts with first, the others with rest. It ends with a newline.
func wrap(s, first, rest string) string {
	var b strings.Builder
	line := first
	empty := true
	for _, w := range strings.Fields(s) {
		if !empty && len(line)+1+len(w) > width {
			b.WriteString(line + "\n")
			line, empty = rest, true
		}
		if !empty {
			line += " "
		}
		line += w
		empty = false
	}
	b.WriteString(line + "\n")
	return b.String()
}
