// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// ErrNotFound is what a Load returns for an item that does not exist: the
// server answers 404 with the "Not found" page.
var ErrNotFound = errors.New("not found")

// ErrBadRequest is what a Load returns for a parameter that does not
// pass its check. Wrap it with fmt.Errorf("...: %w", ErrBadRequest); the
// server answers 400 and says what the error says.
var ErrBadRequest = errors.New("bad request")

// Params are the values a page or a fragment reads from its request: the
// wildcards of the route (as in {name}) and the query. The server keeps
// only the keys that the page or the fragment declares, and no value
// longer than maxParamLen. A Load checks each value again: the browser
// sends whatever it likes.
type Params = url.Values

// maxParamLen is the longest value of a parameter, in bytes.
const maxParamLen = 256

// View is what a template renders from.
type View struct {
	// Page is the name of the page, the empty string in a fragment that
	// /events renders and on the Not found page.
	Page string
	// Title is the page's title, set by Page.Title and changed by Load.
	Title string
	// Nav is the key of the rail entry that is lit. It is the page's Nav,
	// and /events passes it on to the rail fragment.
	Nav string
	// Snap is the snapshot at the start of the render. Load reads it and
	// never changes it.
	Snap store.Snapshot
	// Now is the time to measure from.
	Now time.Time
	// Params are the values of the request that Load reads.
	Params Params
	// Data is what Load sets, for the template.
	Data any
	// CSRF is the session's token, for forms: {{template "csrf-field" .}}.
	CSRF string
	// Addr is host:port that sind ui listens on.
	Addr string
	// Events is the URL of the page's live updates, empty for none.
	Events string
	// Content is the name of the block that holds the page's content.
	Content string
	// Asset returns the URL of a static file by its path below
	// web/static, such as "app.css".
	Asset func(string) string
	// Vendor returns the URL of a script of go-clikit's localweb/htmx by
	// its name, such as htmx.Script.
	Vendor func(string) string
	// HTMXConfig is the configuration of htmx that the layout puts in the
	// page, htmx.Config.
	HTMXConfig string
	// Styles and Scripts are the URLs of the extra files that every page
	// links: web/static/css/*.css and web/static/js/*.js.
	Styles  []string
	Scripts []string

	// reg is the registry of the server that renders the view.
	reg *Registry
}

// A Page is a screen of sind ui at its own address.
type Page struct {
	// Name identifies the page and names its content block
	// "page-NAME". It is lower case, letters, digits and hyphens.
	Name string
	// Pattern is the route as http.ServeMux reads it, with the method:
	// "GET /clusters/{name}". Its wildcards become Params.
	Pattern string
	// Title is the page title. Load may change it.
	Title string
	// Nav is the key of the rail entry to light, one of the NavItem keys;
	// empty lights none.
	Nav string
	// Params are the query keys that the page reads, besides the
	// wildcards of its Pattern.
	Params []string
	// Fragments are the fragments of the page that live updates replace.
	// The shell fragments (the rail, the task dock) are always included.
	Fragments []string
	// Load fills v.Data, and may set v.Title. It may return ErrNotFound or
	// ErrBadRequest. It runs on each request, so it only reads.
	Load func(v *View) error
}

// A Fragment is a block of HTML that live updates replace.
type Fragment struct {
	// Name is the SSE event and the name of the template block.
	Name string
	// Topics are the topics the fragment depends on: it is rendered again
	// when one of them changes.
	Topics []store.Topic
	// Prefixes are topic prefixes it depends on, such as "run/" for every
	// store.RunTopic. The prefix "" is every topic.
	Prefixes []string
	// Watch returns the topics that depend on the request's Params, such
	// as the one run of the run page. It may be nil.
	Watch func(p Params) []store.Topic
	// Params are the query keys the fragment reads; /events passes on no
	// others.
	Params []string
	// Load fills v.Data from v.Snap and v.Params.
	Load func(v *View) error
}

// depends reports whether a change of any topic in changed makes f stale.
func (f Fragment) depends(changed []store.Topic, p Params) bool {
	for _, t := range changed {
		if slices.Contains(f.Topics, t) {
			return true
		}
		for _, prefix := range f.Prefixes {
			if strings.HasPrefix(string(t), prefix) {
				return true
			}
		}
	}
	if f.Watch == nil {
		return false
	}
	return slices.ContainsFunc(f.Watch(p), func(t store.Topic) bool { return slices.Contains(changed, t) })
}

// A Route is a handler for the paths that no page serves.
type Route struct {
	// Pattern is the route as http.ServeMux reads it, with the method:
	// "POST /clusters/{name}/delete".
	Pattern string
	// Handle answers the request. Use s.View, s.Write and s.Fail.
	Handle func(s *Server, w http.ResponseWriter, r *http.Request)
	// FetchOnly lets in only the requests that a page of this server sent
	// itself, as its live parts do, and no navigation from another page. Set
	// it on a route that starts work.
	FetchOnly bool
}

// NavCount is the badge behind a rail entry: a count with its unit, such
// as "1 warning", or a dot when all is well.
type NavCount struct {
	// Text is the count with its unit; empty shows none, or a dot with
	// Class ok.
	Text string
	// Unit, when set, follows Text, which is then the number alone: the
	// bar of a phone, which has no room for it, leaves it out, as "3" of
	// "3 need a look".
	Unit string
	// Class colours it: "", "run", "warn", "bad", or "ok" for the dot.
	Class string
	// Label says what the badge means in words, for screen readers and
	// as its tooltip, such as "1 node needs a look".
	Label string
	// AllRealms says that the count spans more than one realm; the label
	// of the entry's group says so.
	AllRealms bool
}

// NavCountFunc returns the badge of a rail entry from the snapshot, at
// the time of the view.
type NavCountFunc func(sn store.Snapshot, now time.Time) NavCount

// A Service is background work of sind ui.
type Service struct {
	// Name orders the services: Setup and start go by name.
	Name string
	// Setup runs once before the server is built, to make what routes
	// need and to put it in env (Env.Put). It may be nil.
	Setup func(env *Env) error
	// Run runs in its own goroutine until ctx ends, and may be nil.
	Run func(ctx context.Context, env *Env)
}

// Env is what services and routes share: the store, the executor through
// which every external command runs, and the options.
type Env struct {
	Store *store.Store
	// Exec runs external commands, and is the one that tests replace.
	Exec cmdexec.Executor
	Log  *slog.Logger
	// Opts are the options Run was called with, defaults filled in.
	Opts Options
	// Addr is host:port that the server listens on.
	Addr string

	values   sync.Map
	watchers []Watcher
}

// A Watcher is told which topics a live stream (/events) watches: when the
// stream opens it is called with the topics of the stream's fragments, and
// the function it returns is called when the stream ends. A collector uses
// it to read more often what an open page shows, such as the status of a
// cluster whose page is open. The topics are the exact ones of the fragments
// (Fragment.Topics) and those their Watch functions return for the
// stream's Params; a fragment that depends on a prefix names none. The
// function may be called concurrently and must not block.
type Watcher func(topics []store.Topic) (release func())

// AddWatcher adds a Watcher. Call it from Service.Setup, which runs before
// the server is built.
func (e *Env) AddWatcher(w Watcher) { e.watchers = append(e.watchers, w) }

// watch tells every Watcher that a stream watches topics, and returns the
// function that tells them it has ended.
func (e *Env) watch(topics []store.Topic) (release func()) {
	releases := make([]func(), 0, len(e.watchers))
	for _, w := range e.watchers {
		if r := w(slices.Clone(topics)); r != nil {
			releases = append(releases, r)
		}
	}
	return func() {
		for _, r := range releases {
			r()
		}
	}
}

// Put keeps value under key, for a route or another service to Get. The
// key is a value of a type of the caller's own, as with context values.
func (e *Env) Put(key, value any) { e.values.Store(key, value) }

// Get returns what Put kept under key, or nil.
func (e *Env) Get(key any) any {
	v, _ := e.values.Load(key)
	return v
}

// Registry holds what parts of sind ui register. The init functions fill
// the default one, before anything reads it.
type Registry struct {
	pages     []Page
	fragments map[string]Fragment
	routes    []Route
	services  []Service
	navCounts map[string]NavCountFunc
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{fragments: map[string]Fragment{}, navCounts: map[string]NavCountFunc{}}
}

// defaultRegistry is the registry of the init functions.
var defaultRegistry = NewRegistry()

var (
	nameRE     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	wildcardRE = regexp.MustCompile(`\{([^}.]+)(\.\.\.)?\}`)
)

// AddPage adds a page. It panics for a name or a pattern that is invalid
// or already taken, as the init function that calls it is a program error.
func (r *Registry) AddPage(p Page) {
	if !nameRE.MatchString(p.Name) {
		panic(fmt.Sprintf("ui: page name %q is invalid", p.Name))
	}
	if !strings.HasPrefix(p.Pattern, "GET ") {
		panic(fmt.Sprintf("ui: page %s: pattern %q must start with \"GET \"", p.Name, p.Pattern))
	}
	for _, q := range r.pages {
		if q.Name == p.Name || q.Pattern == p.Pattern {
			panic(fmt.Sprintf("ui: page %s or its pattern %q is registered twice", p.Name, p.Pattern))
		}
	}
	r.pages = append(r.pages, p)
}

// AddFragment adds a fragment. It panics for a name that is invalid or
// already taken.
func (r *Registry) AddFragment(f Fragment) {
	if !nameRE.MatchString(f.Name) {
		panic(fmt.Sprintf("ui: fragment name %q is invalid", f.Name))
	}
	if _, ok := r.fragments[f.Name]; ok {
		panic(fmt.Sprintf("ui: fragment %s is registered twice", f.Name))
	}
	r.fragments[f.Name] = f
}

// AddRoute adds a route.
func (r *Registry) AddRoute(rt Route) { r.routes = append(r.routes, rt) }

// AddService adds a service. It panics for a name that is taken.
func (r *Registry) AddService(s Service) {
	if slices.ContainsFunc(r.services, func(o Service) bool { return o.Name == s.Name }) {
		panic(fmt.Sprintf("ui: service %s is registered twice", s.Name))
	}
	r.services = append(r.services, s)
	slices.SortStableFunc(r.services, func(a, b Service) int { return strings.Compare(a.Name, b.Name) })
}

// SetNavCount sets the count behind the rail entry key.
func (r *Registry) SetNavCount(key string, fn NavCountFunc) { r.navCounts[key] = fn }

// wildcards returns the names of the wildcards of a ServeMux pattern.
func wildcards(pattern string) []string {
	var out []string
	for _, m := range wildcardRE.FindAllStringSubmatch(pattern, -1) {
		if m[1] != "$" {
			out = append(out, m[1])
		}
	}
	return out
}

// shellFragments are the fragments that every page includes, in name
// order: the rail and the task dock.
var shellFragments = []string{"rail", "tasks"}
