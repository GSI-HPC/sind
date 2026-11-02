// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// Miss is an ErrNotFound that says what is missing and where to go
// instead. The Not found page shows it: a Load or a route returns it in
// place of a plain ErrNotFound when it knows better than "nothing here".
// errors.Is(m, ErrNotFound) holds.
type Miss struct {
	// What says what is missing, in one sentence.
	What string
	// Hint is a second sentence, or empty.
	Hint string
	// Links are the ways forward, the closest match first.
	Links []MissLink
}

// MissLink is a way forward of a Miss.
type MissLink struct {
	Label string
	URL   string
	// Note follows the link as plain text, such as "in realm ci".
	Note string
}

// Error returns What.
func (m *Miss) Error() string { return m.What }

// Is makes a Miss an ErrNotFound.
func (m *Miss) Is(target error) bool { return target == ErrNotFound }

// errorPage is what the error page shows: the Not found page, and the page
// of a bad request that a browser navigated to.
type errorPage struct {
	// Status is 404 or 400.
	Status int
	// Address is the path and query that failed, cut to maxAddressLen.
	Address string
	*Miss
}

// NotFound reports whether the page is the Not found page.
func (e *errorPage) NotFound() bool { return e.Status == http.StatusNotFound }

// maxAddressLen is the longest address the error page shows, in bytes.
const maxAddressLen = 200

// missAddress returns the address of r as the error page shows it.
func missAddress(r *http.Request) string {
	a := r.URL.RequestURI()
	if len(a) > maxAddressLen {
		a = strings.ToValidUTF8(a[:maxAddressLen], "") + "…"
	}
	return a
}

// missOverview is the way back that every error page offers last.
var missOverview = MissLink{Label: "Overview", URL: "/"}

// missForPath returns what the Not found page offers for an address that
// no route serves: the node, cluster or realm that an address of the
// cluster and node pages names (pgPathLinks), then the pages of the rail
// whose address is close to it.
func missForPath(sn store.Snapshot, path string) *Miss {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	byName := map[string]NavItem{}
	var names []string
	for _, it := range navItems {
		if it.Href == "/" {
			continue
		}
		name := strings.TrimPrefix(it.Href, "/")
		byName[name] = it
		names = append(names, name)
	}
	m := &Miss{Links: pgPathLinks(sn, segs)}
	for _, name := range missClosest(strings.ToLower(segs[0]), names, 2) {
		it := byName[name]
		m.Links = append(m.Links, MissLink{Label: it.Label, URL: it.Href})
	}
	m.Links = append(m.Links, missOverview)
	return m
}

// navHref returns the address of the rail entry key, "/" for none.
func navHref(key string) string {
	for _, it := range navItems {
		if it.Key == key {
			return it.Href
		}
	}
	return "/"
}

// missClosest returns the candidates that are close to name, the closest
// first, at most n of them: a candidate within a third of name's length
// in edits (at least one edit), and, for a name of three or more letters,
// a candidate that starts with name or that name starts with.
func missClosest(name string, candidates []string, n int) []string {
	type hit struct {
		s string
		d int
	}
	limit := max(1, utf8.RuneCountInString(name)/3)
	var hits []hit
	for _, c := range candidates {
		d := missEdits(name, c)
		prefix := len(name) >= 3 && len(c) >= 3 && (strings.HasPrefix(c, name) || strings.HasPrefix(name, c))
		if d <= limit || prefix {
			hits = append(hits, hit{c, d})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return cmp.Compare(a.d, b.d) })
	out := make([]string, 0, min(n, len(hits)))
	for _, h := range hits[:min(n, len(hits))] {
		out = append(out, h.s)
	}
	return out
}

// missEdits returns the edit distance of a and b: the fewest letters to
// add, remove or change to make one the other.
func missEdits(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
