// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/GSI-HPC/sind/pkg/config"
)

// realmCookie is the cookie that keeps the realm a user picked in the top
// bar. A list page reads ?realm= first, then this cookie, and shows every
// realm when neither is set.
const realmCookie = "sind_realm"

// realmCookieAge is how long the browser keeps the picked realm.
const realmCookieAge = 365 * 24 * 3600

// pgRealmPages are the pages that the realm picker may return to, by the
// name of the page: the lists of the Realm group of the rail. Any other
// page, a cluster or a node, belongs to one realm already, so the picker
// goes to the clusters of the realm picked.
var pgRealmPages = map[string]string{
	"clusters": "/clusters", "nodes": "/nodes",
	"mesh": "/mesh", "monitoring": "/monitoring", "topology": "/topology",
}

// pgRealmNext returns the address that the picker of a page's view returns
// to.
func pgRealmNext(v *View) string {
	if p, ok := pgRealmPages[v.Page]; ok {
		return p
	}
	return "/clusters"
}

// PgRealmOption is an entry of the realm picker.
type PgRealmOption struct {
	Value    string
	Label    string
	Selected bool
}

// pgRealmOptions returns the entries of the realm picker: every realm of
// the snapshot with its cluster count, the realm of the page when the
// snapshot does not know it, and the entry for all realms.
func pgRealmOptions(v *View) []PgRealmOption {
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

// pgWithRealm returns r with the realm of the cookie as its ?realm=, when
// the query names none and the cookie holds a valid realm name. Pages read
// their realm from the query only, as the live updates that render them
// again have no cookie.
func pgWithRealm(r *http.Request) *http.Request {
	q := r.URL.Query()
	if q.Has("realm") {
		return r
	}
	c, err := r.Cookie(realmCookie)
	if err != nil || c.Value == "" || config.CheckName("realm", c.Value) != nil {
		return r
	}
	r2 := r.Clone(r.Context())
	q.Set("realm", c.Value)
	r2.URL.RawQuery = q.Encode()
	r2.Form, r2.PostForm = nil, nil
	return r2
}

// pgListRoute serves a list page, which reads the realm cookie, as a route:
// a Page cannot see the request.
func pgListRoute(p Page) Route {
	return Route{Pattern: p.Pattern, Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		s.pageHandler(p)(w, pgWithRealm(r))
	}}
}

// pgRealmRoute is POST /realm: it keeps the realm picked in the top bar in
// the cookie sind_realm, and goes back to a list page. An empty realm is
// all realms and removes the cookie. The picker sends it with htmx, which
// is answered with HX-Redirect; a plain form gets a 303.
func pgRealmRoute() Route {
	return Route{Pattern: "POST /realm", Handle: func(s *Server, w http.ResponseWriter, r *http.Request) {
		v, err := s.View(r, "realm", "next")
		if err == nil {
			_, err = pgRealmParam(v)
		}
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		c := &http.Cookie{
			Name: realmCookie, Value: v.Params.Get("realm"), Path: "/", MaxAge: realmCookieAge,
			HttpOnly: true, SameSite: http.SameSiteStrictMode,
		}
		if c.Value == "" {
			c.MaxAge = -1
		}
		http.SetCookie(w, c)
		next := v.Params.Get("next")
		if !slices.Contains(slices.Collect(maps.Values(pgRealmPages)), next) {
			next = "/clusters"
		}
		if r.Header.Get("HX-Request") == "true" {
			// htmx follows a redirect by itself, but swaps what it
			// finds; this one leaves the page.
			w.Header().Set("HX-Redirect", next)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
	}}
}
