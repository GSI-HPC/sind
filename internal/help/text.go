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
		paragraph(&b, a.Undo+".")
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

// Index returns the list of topics that sind help can print.
func (h *Help) Index() string {
	var b strings.Builder
	b.WriteString("Topics of sind help. Run sind help TOPIC to read one.\n")
	section := func(title string, rows [][2]string) {
		fmt.Fprintf(&b, "\n%s:\n", title)
		for _, r := range rows {
			fmt.Fprintf(&b, "  %-16s %s\n", r[0], r[1])
		}
	}
	var rows [][2]string
	for _, t := range h.howtos {
		rows = append(rows, [2]string{t.ID, t.Title})
	}
	section("How to", rows)
	rows = nil
	for _, w := range h.words {
		rows = append(rows, [2]string{w.ID, w.Title})
	}
	section("Words", rows)
	rows = nil
	for _, a := range h.actions {
		rows = append(rows, [2]string{a.ID, a.Title})
	}
	section("Actions of sind ui", rows)
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

func more(b *strings.Builder, doc string) {
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
