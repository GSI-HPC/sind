// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb"
	"github.com/GSI-HPC/go-clikit/localweb/assets"
	"github.com/GSI-HPC/go-clikit/localweb/htmx"
	"github.com/GSI-HPC/go-clikit/localweb/page"
	"github.com/GSI-HPC/go-clikit/localweb/session"
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// contentSecurityPolicy allows only sind ui's own files: no inline script
// or style, no other origin, no framing.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

const (
	// cookiePrefix starts the name of the session cookie; the port
	// follows. Cookies do not tell ports apart, so that another sind ui on
	// another port keeps its own session.
	cookiePrefix = "sind_ui_"
	// csrfHeader carries the session's CSRF token on every request that
	// changes something. app.js adds it to the requests of htmx. A form
	// that a browser sends without script carries it in the field csrf.
	csrfHeader = "X-Sind-CSRF"
)

// openHint is what a refusal adds for the user. It never says what the token
// is or where to find it, only that the terminal has it.
const openHint = "Open the address that sind ui printed in your terminal, or run sind ui again."

const (
	// coalesceEvery is how long changes gather before a page gets them,
	// so that a burst of changes costs one render: about two frames, so
	// that a page moves as soon as a terminal would.
	coalesceEvery = 30 * time.Millisecond
	// keepAliveEvery is how often an idle stream sends a comment, so that
	// nothing between drops it and a gone browser is noticed.
	keepAliveEvery = 25 * time.Second
)

// Server serves sind ui's pages and live updates for one store.
type Server struct {
	env *Env
	reg *Registry
	// sessions lets in the browser that opened the address with the token.
	sessions *session.Manager
	hosts    func(host string) bool
	tmpl     *page.Set
	// assets serves the static files under /static/VERSION/, where VERSION
	// is a hash of all of them: a new sind serves them under a new path, so
	// browsers may cache them for good.
	assets *assets.Set
	// vendor serves htmx, its SSE extension and idiomorph under
	// /htmx/VERSION/.
	vendor *assets.Set
	// styles and scripts are the extra files of every page, as URLs:
	// static/css/*.css and static/js/*.js.
	styles, scripts []string
	log             *slog.Logger
	// ctx ends the live update streams when the server stops.
	ctx context.Context
	now func() time.Time
	mux *http.ServeMux
	// coalesce and keepAlive are coalesceEvery and keepAliveEvery, which
	// tests shorten.
	coalesce, keepAlive time.Duration
}

// serverConfig configures a Server.
type serverConfig struct {
	Env   *Env
	Token string
	// Bound is what the listener bound; the server answers to its names.
	Bound *localweb.Bound
	// Registry is what the server serves; nil is the default registry.
	Registry *Registry
	// FS holds templates/ and static/; nil is the embedded web/.
	FS fs.FS
	// Now is the clock of the pages; nil is time.Now.
	Now func() time.Time
}

func newServer(ctx context.Context, c serverConfig) (*Server, error) {
	if c.Registry == nil {
		c.Registry = defaultRegistry
	}
	if c.FS == nil {
		c.FS = webFS()
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	reg := c.Registry.snapshot()
	static, _ := fs.Sub(c.FS, "static") // "static" is a valid directory name
	a, err := assets.New(static, "/static/", assets.Options{})
	if err != nil {
		return nil, fmt.Errorf("static files: %w", err)
	}
	vendor, _ := assets.New(htmx.Files(), "/htmx/", assets.Options{}) // the kit's own files are all readable
	s := &Server{
		env: c.Env, reg: reg, hosts: c.Bound.Answers, assets: a, vendor: vendor,
		styles: a.URLs("css/*.css"), scripts: a.URLs("js/*.js"),
		log: c.Env.Log, ctx: ctx, now: c.Now, mux: http.NewServeMux(),
		coalesce: coalesceEvery, keepAlive: keepAliveEvery,
	}
	if s.sessions, err = session.New(session.Options{
		Token: c.Token, CookiePrefix: cookiePrefix, CSRFHeader: csrfHeader, Refuse: s.refuse,
	}); err != nil {
		return nil, err
	}
	funcs := template.FuncMap{"fragment": s.fragmentHTML}
	for name, fn := range reg.funcs {
		funcs[name] = fn
	}
	if s.tmpl, err = page.Parse(c.FS, page.Options{Funcs: funcs}, "templates/*.tmpl"); err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	if err := s.routes(); err != nil {
		return nil, err
	}
	return s, nil
}

// check verifies that what is registered fits together: every page and
// fragment has its template, and every fragment a page names exists.
func (s *Server) check() error {
	for _, name := range []string{"layout", "page-notfound", "signin"} {
		if !s.tmpl.Has(name) {
			return fmt.Errorf("template %q is missing", name)
		}
	}
	for _, name := range []string{"rail", "tasks"} {
		if _, ok := s.reg.fragments[name]; !ok {
			return fmt.Errorf("the shell fragment %s is not registered", name)
		}
	}
	for name := range s.reg.fragments {
		if !s.tmpl.Has(name) {
			return fmt.Errorf("fragment %s has no template block of that name", name)
		}
	}
	for _, p := range s.reg.pages {
		if !s.tmpl.Has("page-" + p.Name) {
			return fmt.Errorf("page %s has no template block %q", p.Name, "page-"+p.Name)
		}
		for _, f := range p.Fragments {
			if _, ok := s.reg.fragments[f]; !ok {
				return fmt.Errorf("page %s names the unknown fragment %s", p.Name, f)
			}
		}
	}
	return nil
}

// routes registers the routes of the server and of the registry. A pattern
// that ServeMux refuses is an error, not a panic.
func (s *Server) routes() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("route: %v", r)
		}
	}()
	s.mux.Handle("GET /htmx/", s.vendor)
	s.mux.Handle("GET /events", s.fetchOnly(http.HandlerFunc(s.events)))
	for _, p := range s.reg.pages {
		s.mux.HandleFunc(p.Pattern, s.pageHandler(p))
	}
	for _, rt := range s.reg.routes {
		var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rt.Handle(s, w, r) })
		if rt.FetchOnly {
			h = s.fetchOnly(h)
		}
		s.mux.Handle(rt.Pattern, h)
	}
	s.mux.HandleFunc("/", s.notFound)
	return nil
}

// Handler returns the server's handler with every check in front of it.
// The static files need no session: they hold no data, and the page that
// asks a browser to sign in uses them.
func (s *Server) Handler() http.Handler {
	open := http.NewServeMux()
	open.Handle("GET /static/", s.assets)
	open.Handle("/", s.sessions.Middleware(s.mux))
	h := localweb.SameOrigin(localweb.OriginPolicy{Navigate: true, Refuse: s.refuse})(open)
	return localweb.Headers(contentSecurityPolicy)(localweb.NoStore(localweb.Hosts(s.hosts, localweb.HostsOptions{Refuse: s.refuse})(h)))
}

// fetchOnly lets in only the requests that a page of this server sent
// itself, as its live parts do, and no navigation from another page. It
// guards the requests that start work.
func (s *Server) fetchOnly(next http.Handler) http.Handler {
	return localweb.SameOrigin(localweb.OriginPolicy{Refuse: s.refuse})(next)
}

// refuse answers a request that a check of localweb refuses. A 403 adds the
// line that says where the address is, and the log says why. A browser that
// opens a page gets the 403 as the page signin, which tells how to get the
// address.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, f localweb.Refusal) {
	s.log.Warn("refused a request", "path", r.URL.Path, "why", f.Reason)
	if f.Status == http.StatusForbidden {
		if opensPage(r) && s.signIn(w, f) {
			return
		}
		f.Message += "\n" + openHint
	}
	localweb.Refuse(w, r, f)
}

// opensPage reports whether r is a browser that opens a page: a GET or HEAD
// of a document, and not a request of a page's script.
func opensPage(r *http.Request) bool {
	if !localweb.SafeMethod(r.Method) {
		return false
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" {
		return dest == "document"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// signInPage is what the page signin shows.
type signInPage struct {
	Asset   func(string) string
	Message string
}

// signIn answers with the page signin, and reports whether it could.
func (s *Server) signIn(w http.ResponseWriter, f localweb.Refusal) bool {
	err := s.tmpl.Respond(w, f.Status, "signin", signInPage{Asset: s.assets.URL, Message: f.Message})
	if err != nil {
		s.log.Error("rendering the sign-in page", "err", err)
	}
	return err == nil
}

// Env returns what the server shares with the services.
func (s *Server) Env() *Env { return s.env }

// Store returns the store that the pages read.
func (s *Server) Store() *store.Store { return s.env.Store }

// View returns the view of a request: the current snapshot, the session's
// CSRF token, and as Params the wildcards of the route that matched and
// the values of the named keys, from the query and from the form. A value
// that is too long is an ErrBadRequest.
func (s *Server) View(r *http.Request, keys ...string) (*View, error) {
	_ = r.ParseForm() // a malformed query still yields its valid pairs
	p := Params{}
	for _, k := range wildcards(r.Pattern) {
		p.Set(k, r.PathValue(k))
	}
	for _, k := range keys {
		if vals := r.Form[k]; len(vals) > 0 {
			p[k] = slices.Clone(vals)
		}
	}
	if err := checkParams(p); err != nil {
		return nil, err
	}
	v := s.newView(p)
	v.CSRF = session.CSRF(r.Context())
	return v, nil
}

// checkParams refuses values that are too long, and too many of them.
func checkParams(p Params) error {
	n := 0
	for k, vals := range p {
		n += len(vals)
		for _, val := range vals {
			if len(val) > maxParamLen {
				return fmt.Errorf("the value of %s is longer than %d bytes: %w", k, maxParamLen, ErrBadRequest)
			}
		}
	}
	if n > maxParamValues {
		return fmt.Errorf("more than %d values: %w", maxParamValues, ErrBadRequest)
	}
	return nil
}

// maxParamValues is the most values a request may hand to a page.
const maxParamValues = 64

// newView returns a view of the current snapshot with p as its Params.
func (s *Server) newView(p Params) *View {
	return &View{
		Snap: s.env.Store.Snapshot(), Now: s.now(), Params: p, Addr: s.env.Addr,
		Asset: s.assets.URL, Vendor: s.vendor.URL, HTMXConfig: htmx.Config, Styles: s.styles, Scripts: s.scripts, reg: s.reg,
	}
}

// Write renders the block name with v and writes it as the response with
// status. A render that fails goes to Fail: a fragment of the page that
// returns ErrNotFound makes the page a 404, and anything else a 500.
func (s *Server) Write(w http.ResponseWriter, r *http.Request, status int, name string, v *View) {
	if err := s.respond(w, status, name, v); err != nil {
		s.Fail(w, r, err)
	}
}

// respond writes the block name rendered with v, or returns the error and
// writes nothing.
func (s *Server) respond(w http.ResponseWriter, status int, name string, v *View) error {
	if err := s.tmpl.Respond(w, status, name, v); err != nil {
		return fmt.Errorf("rendering %s: %w", name, err)
	}
	return nil
}

// Fragment answers a request with the fragment name: its Load runs on the
// request's Params (the fragment's own keys) and its block is the response.
func (s *Server) Fragment(w http.ResponseWriter, r *http.Request, name string) {
	f, ok := s.reg.fragments[name]
	if !ok {
		s.Fail(w, r, fmt.Errorf("fragment %s: %w", name, ErrNotFound))
		return
	}
	v, err := s.View(r, f.Params...)
	if err == nil {
		err = s.load(f, v)
	}
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	s.Write(w, r, http.StatusOK, name, v)
}

// fragmentHTML is the template function fragment: it renders the fragment
// name from a copy of v, as live updates do, so that a page holds the same
// HTML that /events would send.
func (s *Server) fragmentHTML(name string, v *View) (template.HTML, error) {
	f, ok := s.reg.fragments[name]
	if !ok {
		return "", fmt.Errorf("unknown fragment %q", name)
	}
	html, err := s.renderFragment(f, v)
	return template.HTML(html), err //nolint:gosec // G203: the output of a template, escaped in context
}

// load runs the Load of f on v.
func (s *Server) load(f Fragment, v *View) error {
	if f.Load == nil {
		return nil
	}
	return f.Load(v)
}

// Fail answers a request whose Load or route failed: ErrNotFound with the
// Not found page and what a Miss in err says; ErrBadRequest with 400 and
// its message, in the layout when a browser navigated to the address;
// anything else with 500 and a line in the log.
func (s *Server) Fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		var m *Miss
		if !errors.As(err, &m) {
			m = &Miss{Links: []MissLink{missOverview}}
		}
		s.errorPage(w, r, http.StatusNotFound, m)
	case errors.Is(err, ErrBadRequest) && navigation(r):
		s.errorPage(w, r, http.StatusBadRequest, &Miss{What: userMessage(err), Links: []MissLink{missOverview}})
	case errors.Is(err, ErrBadRequest):
		http.Error(w, userMessage(err), http.StatusBadRequest)
	default:
		s.fail500(w, r, err)
	}
}

// navigation reports whether a browser asked for r as a page to show, not
// htmx nor a script.
func navigation(r *http.Request) bool {
	return r.Method == http.MethodGet && r.Header.Get("HX-Request") == "" &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}

// userMessage returns what an ErrBadRequest says to the user: the message of
// the innermost error that wraps the sentinel, without the template error
// chain around it and without the sentinel's own text.
func userMessage(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil || next == ErrBadRequest || !errors.Is(next, ErrBadRequest) {
			break
		}
		err = next
	}
	return strings.TrimSuffix(err.Error(), ": "+ErrBadRequest.Error())
}

// fail500 answers with a server error, and logs why.
func (s *Server) fail500(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	http.Error(w, "sind ui could not answer this request; its log says why", http.StatusInternalServerError)
}

// pageHandler serves a page: its Load, then the layout with the page's
// content block.
func (s *Server) pageHandler(p Page) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := s.View(r, s.keys(p.Params, p.Fragments)...)
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		v.Page, v.Title, v.Nav, v.Content = p.Name, p.Title, p.Nav, "page-"+p.Name
		if p.Load != nil {
			if err := p.Load(v); err != nil {
				s.Fail(w, r, err)
				return
			}
		}
		v.Events = s.eventsURL(p.Fragments, v)
		s.Write(w, r, http.StatusOK, "layout", v)
	}
}

// keys returns the query keys that a page reads: its own, and those of the
// fragments it names, so that a page hands its fragments what they declare.
func (s *Server) keys(own, fragments []string) []string {
	keys := slices.Clone(own)
	for _, n := range fragments {
		keys = append(keys, s.reg.fragments[n].Params...)
	}
	return keys
}

// notFound answers an address that no route serves with the Not found
// page, which offers what the address names and the pages whose address
// is close to it.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, missForPath(s.Store().Snapshot(), r.URL.Path))
}

// errorPage answers with the error page in the layout: the Not found page
// for status 404, the page of a bad address for 400. It names the address
// of r and shows what m says.
func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, status int, m *Miss) {
	v := s.newView(Params{})
	v.CSRF = session.CSRF(r.Context())
	v.Title, v.Content = "Not found", "page-notfound"
	if status != http.StatusNotFound {
		v.Title = "Bad address"
	}
	v.Data = &errorPage{Status: status, Address: missAddress(r), What: m.What, Hint: m.Hint, Links: m.Links}
	v.Events = s.eventsURL(nil, v)
	if err := s.respond(w, status, "layout", v); err != nil {
		s.fail500(w, r, err)
	}
}

// eventsURL returns the URL of the live updates of the shell fragments and
// of the named ones, with the Params they declare and the lit rail entry.
func (s *Server) eventsURL(names []string, v *View) string {
	names = slices.Compact(slices.Sorted(slices.Values(append(s.reg.shellFragments(), names...))))
	q := url.Values{"f": names}
	for _, n := range names {
		for _, k := range s.reg.fragments[n].Params {
			if vals, ok := v.Params[k]; ok {
				q[k] = vals
			}
		}
	}
	if v.Nav != "" {
		q.Set("nav", v.Nav)
	}
	return "/events?" + q.Encode()
}
