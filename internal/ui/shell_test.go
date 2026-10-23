// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
)

func TestShellSkipLinkAndThemeToggle(t *testing.T) {
	ts := newTestServer(t).login()
	body := ts.get("/demo/x").Body.String()

	// The skip link is the first thing of the page that takes the focus,
	// and the content can take it.
	assert.Contains(t, body, "<body>\n<a class=\"skip\" href=\"#main\">Skip to content</a>\n<div class=\"app\"")
	assert.Contains(t, body, `<main class="main" id="main" tabindex="-1">`)
	// The theme button is a toggle that says its state.
	assert.Contains(t, body, `data-theme-toggle aria-label="Dark theme" aria-pressed="false"`)
}

func TestRailIsListsWithLabels(t *testing.T) {
	ts := newTestServer(t).login()
	body := ts.get("/demo/x").Body.String()
	assert.NotContains(t, body, "<h6", "no headings before the page's h1")
	assert.Contains(t, body, `<span class="rgl" id="rail-overview">Overview</span>`+"\n"+`<ul class="rg rg-1" aria-labelledby="rail-overview">`)
	assert.Contains(t, body, `<ul class="rg rg-2" aria-labelledby="rail-realm">`, "two of the Realm entries are in a phone's bar")
	assert.Contains(t, body, `<li class="sec"><a class="nav" href="/mesh">`)
	assert.Contains(t, body, `<li><a class="nav" href="/doctor">`)
	// More opens the sheet of the entries that a phone's bar leaves out.
	assert.Contains(t, body, `<button type="button" class="nav more" data-rail-more aria-expanded="false" aria-controls="rail-sheet">`)
	assert.Contains(t, body, `<div class="rail-sheet" id="rail-sheet" hidden><ul aria-label="More pages"><li><a class="nav" href="/mesh">`)
	assert.Contains(t, body, `icons.svg#more"></use></svg>More</button>`, "More has no count when the sheet has none to show")
}

func TestRailMore(t *testing.T) {
	reg := NewRegistry()
	view := func(nav string) *View {
		return &View{Nav: nav, Asset: func(p string) string { return "/s/" + p }}
	}
	d := reg.rail(view("clusters"))
	assert.False(t, d.More.On)
	assert.Equal(t, "/s/icons.svg#more", d.More.Icon)
	assert.Equal(t, "/s/icons.svg#clusters", d.Groups[1].Items[0].Icon)
	assert.Equal(t, []int{1, 2, 1}, []int{d.Groups[0].Main, d.Groups[1].Main, d.Groups[2].Main})
	var sheet []string
	for _, e := range d.Sheet {
		sheet = append(sheet, e.Key)
	}
	assert.Equal(t, []string{"mesh", "monitoring", "topology", "activity"}, sheet)
	assert.True(t, reg.rail(view("topology")).More.On, "a page of the sheet lights More")

	// More shows the most urgent count of the sheet: a failure before work
	// that goes on, and a plain count never.
	reg.SetNavCount("monitoring", func(store.Snapshot) NavCount { return NavCount{Text: "4"} })
	assert.Equal(t, NavCount{}, reg.rail(view("")).More.Count)
	reg.SetNavCount("activity", func(store.Snapshot) NavCount { return NavCount{Text: "2 running", Class: "run"} })
	m := reg.rail(view("")).More
	assert.Equal(t, NavCount{Text: "▸", Class: "run"}, m.Count)
	assert.Equal(t, "Activity: 2 running", m.Note)
	reg.SetNavCount("mesh", func(store.Snapshot) NavCount { return NavCount{Text: "1", Class: "bad"} })
	m = reg.rail(view("")).More
	assert.Equal(t, NavCount{Text: "!", Class: "bad"}, m.Count, "the failure of Mesh stays over the work of Activity")
	assert.Equal(t, "Mesh and DNS: 1", m.Note)
}

func TestRailMoreCountInThePage(t *testing.T) {
	cfg, st := testConfig(t)
	cfg.Registry.SetNavCount("activity", func(store.Snapshot) NavCount { return NavCount{Text: "1 running", Class: "run"} })
	ts := newTestServerFrom(t, cfg, st).login()
	body := ts.get("/demo/x").Body.String()
	assert.Contains(t, body, `More<span class="n run" aria-hidden="true">▸</span><span class="vh">, Activity: 1 running</span></button>`)
}
