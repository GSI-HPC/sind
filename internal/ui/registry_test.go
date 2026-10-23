// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"html/template"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryRefusesWrongRegistrations(t *testing.T) {
	r := NewRegistry()
	r.AddPage(Page{Name: "one", Pattern: "GET /one"})
	r.AddFragment(Fragment{Name: "frag"})
	r.AddService(Service{Name: "svc"})
	r.AddFuncs(template.FuncMap{"mine": func() string { return "" }})

	assert.PanicsWithValue(t, `ui: page name "Bad" is invalid`, func() { r.AddPage(Page{Name: "Bad", Pattern: "GET /x"}) })
	assert.PanicsWithValue(t, `ui: page name "" is invalid`, func() { r.AddPage(Page{Pattern: "GET /x"}) })
	assert.Panics(t, func() { r.AddPage(Page{Name: "post", Pattern: "POST /x"}) }, "a page is a GET")
	assert.Panics(t, func() { r.AddPage(Page{Name: "one", Pattern: "GET /other"}) }, "same name")
	assert.Panics(t, func() { r.AddPage(Page{Name: "two", Pattern: "GET /one"}) }, "same pattern")
	assert.Panics(t, func() { r.AddFragment(Fragment{Name: "Bad name"}) })
	assert.Panics(t, func() { r.AddFragment(Fragment{Name: "frag"}) }, "same name")
	assert.Panics(t, func() { r.AddService(Service{Name: "svc"}) }, "same name")
	assert.Panics(t, func() { r.AddFuncs(template.FuncMap{"mine": func() string { return "" }}) }, "same name")
	assert.Panics(t, func() { r.AddFuncs(template.FuncMap{"include": func() string { return "" }}) }, "reserved")
	assert.Panics(t, func() { r.AddFuncs(template.FuncMap{"fragment": func() string { return "" }}) }, "reserved")
}

func TestFragmentDepends(t *testing.T) {
	pending := func(ts ...store.Topic) []store.Topic { return ts }
	p := Params{"id": {"abc"}}
	watch := func(p Params) []store.Topic { return []store.Topic{store.RunTopic(p.Get("id"))} }
	for _, tc := range []struct {
		name    string
		f       Fragment
		pending []store.Topic
		want    bool
	}{
		{"nothing pending", Fragment{Topics: []store.Topic{store.TopicRuns}}, pending(), false},
		{"exact topic", Fragment{Topics: []store.Topic{store.TopicRuns}}, pending(store.TopicTasks, store.TopicRuns), true},
		{"other topic", Fragment{Topics: []store.Topic{store.TopicRuns}}, pending(store.TopicTasks), false},
		{"prefix", Fragment{Prefixes: []string{"run/"}}, pending(store.RunTopic("zzz")), true},
		{"prefix, other topic", Fragment{Prefixes: []string{"run/"}}, pending(store.TopicRuns), false},
		{"every topic", Fragment{Prefixes: []string{""}}, pending(store.TopicTasks), true},
		{"watched item", Fragment{Watch: watch}, pending(store.RunTopic("abc")), true},
		{"another item", Fragment{Watch: watch}, pending(store.RunTopic("def")), false},
		{"no dependency", Fragment{}, pending(store.TopicTasks), false},
	} {
		assert.Equal(t, tc.want, tc.f.depends(tc.pending, p), tc.name)
	}
}

func TestWildcards(t *testing.T) {
	assert.Equal(t, []string{"realm", "name", "rest"}, wildcards("GET /clusters/{realm}/{name}/files/{rest...}"))
	assert.Empty(t, wildcards("GET /{$}"))
	assert.Empty(t, wildcards(""), "r.Pattern is empty for a request that no pattern matched")
}

func TestRegistrySnapshotIsIndependent(t *testing.T) {
	r := NewRegistry()
	r.AddPage(Page{Name: "one", Pattern: "GET /one"})
	r.AddFragment(Fragment{Name: "frag"})
	r.AddRoute(Route{Pattern: "GET /route"})
	r.AddService(Service{Name: "svc"})
	r.AddFuncs(template.FuncMap{"mine": func() string { return "" }})
	r.SetNavCount("clusters", func(store.Snapshot) NavCount { return NavCount{Text: "1"} })
	c := r.snapshot()

	r.AddPage(Page{Name: "two", Pattern: "GET /two"})
	r.AddFragment(Fragment{Name: "later"})
	r.AddRoute(Route{Pattern: "GET /later"})
	r.AddService(Service{Name: "later"})
	r.AddFuncs(template.FuncMap{"later": func() string { return "" }})
	r.SetNavCount("nodes", func(store.Snapshot) NavCount { return NavCount{} })

	assert.Len(t, c.pages, 1)
	assert.Len(t, c.fragments, 1)
	assert.Len(t, c.routes, 1)
	assert.Len(t, c.services, 1)
	assert.Len(t, c.funcs, 1)
	assert.Len(t, c.navCounts, 1)
}

func TestServicesRunInNameOrder(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"c", "a", "b"} {
		r.AddService(Service{Name: n})
	}
	var names []string
	for _, s := range r.services {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"a", "b", "c"}, names)
}

func TestShellFragments(t *testing.T) {
	r := NewRegistry()
	r.AddFragment(Fragment{Name: "zeta", Shell: true})
	r.AddFragment(Fragment{Name: "plain"})
	r.AddFragment(Fragment{Name: "alpha", Shell: true})
	assert.Equal(t, []string{"alpha", "zeta"}, r.shellFragments())
}

func TestEnvKeepsValues(t *testing.T) {
	type key struct{}
	var e Env
	assert.Nil(t, e.Get(key{}))
	e.Put(key{}, 42)
	assert.Equal(t, 42, e.Get(key{}))
	e.Put(key{}, "replaced")
	assert.Equal(t, "replaced", e.Get(key{}))
}

func TestDefaultRegistryHasTheShell(t *testing.T) {
	var patterns []string
	for _, rt := range defaultRegistry.routes {
		patterns = append(patterns, rt.Pattern)
	}
	assert.Contains(t, patterns, "GET /{$}", "the Overview")
	for _, name := range []string{"rail", "tasks"} {
		f, ok := defaultRegistry.fragments[name]
		require.True(t, ok, name)
		assert.True(t, f.Shell, name)
	}
}

func TestRegisterFunctionsUseTheDefaultRegistry(t *testing.T) {
	saved := defaultRegistry
	defaultRegistry = NewRegistry()
	t.Cleanup(func() { defaultRegistry = saved })

	RegisterPage(Page{Name: "p", Pattern: "GET /p"})
	RegisterFragment(Fragment{Name: "f"})
	RegisterRoute(Route{Pattern: "GET /r"})
	RegisterService(Service{Name: "s"})
	AddFuncs(template.FuncMap{"fn": func() string { return "" }})
	SetNavCount("clusters", func(store.Snapshot) NavCount { return NavCount{Text: "9"} })

	assert.Len(t, defaultRegistry.pages, 1)
	assert.Len(t, defaultRegistry.fragments, 1)
	assert.Len(t, defaultRegistry.routes, 1)
	assert.Len(t, defaultRegistry.services, 1)
	assert.Contains(t, defaultRegistry.funcs, "fn")
	assert.Equal(t, "9", defaultRegistry.navCount("clusters")(store.Snapshot{}, time.Time{}).Text)
	SetNavCountAt("nodes", func(_ store.Snapshot, now time.Time) NavCount { return NavCount{Text: now.Format("2006")} })
	assert.Equal(t, "2026", defaultRegistry.navCount("nodes")(store.Snapshot{}, pgNow).Text)
	assert.Nil(t, defaultRegistry.navCount("monitoring"))
}
