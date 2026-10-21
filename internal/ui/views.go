// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bytes"
	"html/template"
	"io/fs"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// templates holds the parsed templates, one set with the blocks of every
// file in web/templates.
type templates struct {
	set *template.Template
}

// newTemplates parses templates/*.tmpl of fsys with funcs, to which it adds
// include. include executes a block by name and returns its HTML, so that
// the layout can place the content block of the page that is rendered.
func newTemplates(fsys fs.FS, funcs template.FuncMap) (*templates, error) {
	t := &templates{}
	fm := template.FuncMap{}
	for name, fn := range funcs {
		fm[name] = fn
	}
	fm["include"] = func(name string, data any) (template.HTML, error) {
		html, err := t.render(name, data)
		return template.HTML(html), err //nolint:gosec // G203: the output of a template, escaped in context
	}
	set, err := template.New("").Funcs(fm).ParseFS(fsys, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}
	t.set = set
	return t, nil
}

// has reports whether the block name exists.
func (t *templates) has(name string) bool { return t.set.Lookup(name) != nil }

// render executes the block name with data.
func (t *templates) render(name string, data any) (string, error) {
	var buf bytes.Buffer
	err := t.set.ExecuteTemplate(&buf, name, data)
	return buf.String(), err
}

// NavItem is an entry of the rail.
type NavItem struct {
	// Key is what Page.Nav names.
	Key   string
	Label string
	Href  string
	// Glyph is the character before the label.
	Glyph string
	// Group is the heading it stands under.
	Group string
}

// navItems are the entries of the rail, in order. The pages that they link
// to come from other parts of sind ui.
var navItems = []NavItem{
	{"overview", "Overview", "/", "◎", "Overview"},
	{"clusters", "Clusters", "/clusters", "▦", "Realm"},
	{"nodes", "Nodes", "/nodes", "▪", "Realm"},
	{"mesh", "Mesh and DNS", "/mesh", "⋔", "Realm"},
	{"monitoring", "Monitoring", "/monitoring", "◔", "Realm"},
	{"topology", "Topology", "/topology", "⋈", "Realm"},
	{"doctor", "Doctor", "/doctor", "✓", "Host"},
	{"activity", "Activity", "/activity", "≡", "Host"},
}

// navGroup is a heading of the rail and the entries below it.
type navGroup struct {
	Title string
	Items []navEntry
}

// navEntry is an entry of the rail as the template draws it.
type navEntry struct {
	NavItem
	On    bool
	Count NavCount
}

// rail returns the rail's groups for the view: the entry v.Nav is lit, and
// each entry has the count that SetNavCount gave it.
func (r *Registry) rail(v *View) []navGroup {
	var groups []navGroup
	for _, it := range navItems {
		e := navEntry{NavItem: it, On: it.Key == v.Nav}
		if fn := r.navCount(it.Key); fn != nil {
			e.Count = fn(v.Snap)
		}
		if i := len(groups) - 1; i >= 0 && groups[i].Title == it.Group {
			groups[i].Items = append(groups[i].Items, e)
			continue
		}
		groups = append(groups, navGroup{Title: it.Group, Items: []navEntry{e}})
	}
	return groups
}

// navCount returns the count function of the rail entry key, or nil.
func (r *Registry) navCount(key string) func(store.Snapshot) NavCount {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.navCounts[key]
}
