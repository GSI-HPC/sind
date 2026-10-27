// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/config"
)

// The realm of a page that shows one realm or all of them is in its
// address, ?realm=. The realm picker in the top bar and the realm links of
// the lists are plain links and forms to such an address. The browser
// remembers the last realm picked in the cookie sind_realm: a list page
// without ?realm= goes to the address with the remembered realm, so that
// the address always says what the page shows.

// realmCookie is the cookie that remembers the realm picked last.
const realmCookie = "sind_realm"

// realmCookieAge is how long the browser keeps the picked realm.
const realmCookieAge = 365 * 24 * 3600

// pgRealmPages are the pages that show one realm or all of them, by the
// name they are registered with, and their addresses. The realm picker
// stays on such a page.
var pgRealmPages = map[string]string{
	"overview": "/", "clusters": "/clusters", "nodes": "/nodes",
	"more-mesh": "/mesh", "more-monitoring": "/monitoring", "more-topology": "/topology",
	"run-activity": "/activity",
}

// PgRealmNext returns the address that the realm picker in the top bar of
// a page's view goes to: the page itself when it shows one realm or all,
// the list of nodes from a node page, and the list of clusters from any
// other page.
func (v *View) PgRealmNext() string {
	if p, ok := pgRealmPages[v.Page]; ok {
		return p
	}
	if v.Page == "node" {
		return "/nodes"
	}
	return "/clusters"
}

// PgRealmOption is an entry of the realm picker.
type PgRealmOption struct {
	Value    string
	Label    string
	Selected bool
}

// PgRealmOptions returns the entries of the realm picker: every realm of
// the snapshot with its cluster count, the realm of the page when the
// snapshot does not know it, and the entry for all realms.
func (v *View) PgRealmOptions() []PgRealmOption {
	cur := v.Params.Get("realm")
	opts := []PgRealmOption{{Value: "", Label: "all realms", Selected: cur == ""}}
	seen := false
	for _, r := range v.Snap.Realms {
		seen = seen || r.Name == cur
		opts = append(opts, PgRealmOption{
			Value: r.Name, Label: fmt.Sprintf("%s · %d", r.Name, r.Clusters), Selected: r.Name == cur,
		})
	}
	if cur != "" && !seen {
		opts = append(opts, PgRealmOption{Value: cur, Label: cur, Selected: true})
	}
	return opts
}

// PgRealmScope is what a page that shows one realm says about it: the
// realm, and the address of the same page with every realm.
type PgRealmScope struct {
	Realm   string
	ShowAll string
}

// PgRealmScope returns the scope of the view of a page that shows one
// realm or all of them; Realm is empty when it shows every realm. The page
// is the one of the lit rail entry, which a fragment that /events renders
// knows too.
func (v *View) PgRealmScope() PgRealmScope {
	return PgRealmScope{Realm: v.Params.Get("realm"), ShowAll: navHref(v.Nav) + "?realm="}
}

// pgRealmParam returns the realm that v's Params name, "" for all realms,
// or an ErrBadRequest for a name that is no realm name.
func pgRealmParam(v *View) (string, error) {
	realm := v.Params.Get("realm")
	if realm == "" {
		return "", nil
	}
	if err := config.CheckName("realm", realm); err != nil {
		return "", fmt.Errorf("%w: %w", err, ErrBadRequest)
	}
	return realm, nil
}

// pgRealmKnown reports whether the snapshot holds the realm.
func pgRealmKnown(sn store.Snapshot, realm string) bool {
	return slices.Contains(pgRealmNames(sn, ""), realm)
}

// pgRealmRoute serves a page that shows one realm or all of them as a
// route, as a Page cannot see the request's cookies. A ?realm= in the
// address is what the page shows, and the cookie remembers it when it
// names a realm that exists or every realm, and a page of sind ui led
// there (Sec-Fetch-Site: same-origin). Without ?realm=, a page that
// remembers goes to its address with the remembered realm; one that does
// not, the Overview, shows every realm. The title names the realm shown.
// A realm that Docker has no trace of is the Not found page, with the
// realms of a name close to it; while Docker is unknown or down, the page
// shows the realm empty. Only the page checks: its live updates keep
// showing the realm when its last cluster goes. The list pages of the
// Realm group of the rail remember; a page of the event log sets anyRealm,
// as the log keeps the runs of a realm whose last cluster is gone, and
// shows a realm that Docker has no trace of instead of the Not found page.
func pgRealmRoute(p Page, remember, anyRealm bool) Route {
	load := p.Load
	base := pgRealmPages[p.Name]
	p.Load = func(v *View) error {
		if load != nil {
			if err := load(v); err != nil {
				return err
			}
		}
		if realm := v.Params.Get("realm"); realm != "" {
			if d := v.Snap.Docker; !anyRealm && d.Checked && d.Err == "" && !pgRealmKnown(v.Snap, realm) {
				return pgRealmMiss(v.Snap, base, realm)
			}
			v.Title += " · " + realm
		}
		return nil
	}
	return Route{Pattern: p.Pattern, Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sn := s.env.Store.Snapshot()
		old := ""
		if c, err := r.Cookie(realmCookie); err == nil && config.CheckName("realm", c.Value) == nil {
			old = c.Value
		}
		switch {
		case q.Has("realm"):
			// Only a page of sind ui changes what the browser remembers,
			// not a link of another site nor a typed address.
			realm := q.Get("realm")
			ours := r.Header.Get("Sec-Fetch-Site") == "same-origin"
			if ours && realm != old && (realm == "" || pgRealmKnown(sn, realm)) {
				pgRememberRealm(w, realm)
			}
		case remember && old != "" && pgRealmKnown(sn, old):
			q.Set("realm", old)
			http.Redirect(w, r, (&url.URL{Path: r.URL.Path, RawQuery: q.Encode()}).String(), http.StatusFound)
			return
		case remember && old != "" && sn.Docker.Checked && sn.Docker.Err == "":
			// The remembered realm is gone.
			pgRememberRealm(w, "")
		}
		s.pageHandler(p)(w, r)
	}}
}

// pgRememberRealm sets the cookie that remembers the realm, or removes it
// for every realm.
func pgRememberRealm(w http.ResponseWriter, realm string) {
	c := &http.Cookie{
		Name: realmCookie, Value: realm, Path: "/", MaxAge: realmCookieAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	}
	if realm == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}
