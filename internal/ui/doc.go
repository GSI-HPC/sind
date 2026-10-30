// SPDX-License-Identifier: LGPL-3.0-or-later

// Package ui implements sind ui, a local web UI for sind.
//
// It serves server-rendered pages (html/template) with htmx and one
// Server-Sent Events stream per tab for live updates, all embedded in the
// binary. It listens on 127.0.0.1 only, and lets in only the user's own
// processes holding the per-run token from the URL it prints.
//
// # Extending sind ui
//
// A part of sind ui, such as the Clusters page or the task dock, adds itself
// to the server from files of its own, without editing server.go, sse.go or
// the layout. Parts built side by side then touch no common file. A part
// needs up to five things:
//
//  1. Templates, in web/templates/NAME.tmpl. The server parses every file
//     there into one set, so a block name is global: prefix yours with the
//     part's name. A page defines the block "page-NAME" for its content; a
//     fragment defines a block of its own name. Both are executed with a
//     *View.
//  2. A page, registered from an init function with defaultRegistry.AddPage:
//     a Page has a route ("GET /clusters/{name}"), a title, the key of the
//     rail entry to light, the fragments it keeps live, and a Load that fills
//     View.Data from View.Snap (the snapshot) and View.Params (the route's
//     wildcards and the declared query keys). Load may return ErrNotFound
//     (404), or a *Miss, which says what is missing and offers the closest
//     matches on the Not found page, or an error that wraps ErrBadRequest
//     (400), and only reads.
//  3. Fragments, registered with AddFragment. A fragment is a block of
//     HTML that live updates morph into its element. Its page places it with
//     <div hx-swap="morph:innerHTML" sse-swap="NAME">{{fragment "NAME" .}}</div>,
//     so that the elements that stay keep their animations, focus and open
//     state (idiomorph's htmx extension): the template function
//     fragment runs the fragment's Load on a copy of the view and executes
//     its block, so the page holds what /events would send. /events renders
//     the fragment again when a topic it depends on changes (Topics for
//     exact topics, Prefixes for prefixes such as "run/", Watch for topics
//     that depend on the Params), and sends it only when its HTML differs
//     from what that stream sent before. The rail and the two parts of the
//     task dock are part of every page (shellFragments).
//  4. Routes for everything else, registered with AddRoute: dialogs,
//     POST actions, helpers that answer with one fragment. A route is a
//     function of the *Server and the request. A request that changes
//     something has passed the Origin and CSRF checks and has its form
//     parsed (at most 64 KiB) when the route runs; use Server.View for its
//     Params, Server.Write and Server.Fragment to answer, Server.Fail for an
//     error, and the template block "csrf-field" in a form that a browser
//     sends without script.
//  5. Background work, registered with AddService: collectors that write
//     the store, runners. A Service has a Setup, which runs before the server
//     is built and hands what routes need to the other parts through Env.Put,
//     and a Run, which runs until the context ends. Every external command
//     goes through Env.Exec.
//
// A service can also learn what the open pages need. Env.AddWatcher, called
// from Setup, adds a Watcher: each /events stream calls it with the exact
// topics its fragments depend on (Fragment.Topics and what Fragment.Watch
// returns for the stream's Params) when it opens, and calls the function it
// returns when it ends. The collectors count the status topics this way and
// read the status of a cluster only while a page shows it. The collect
// service also puts two plain funcs in Env for other parts: ColRefreshKey
// (func(realm string), read that realm again now) and ColDoctorKey (func(),
// run the host checks again). A part that finds neither (nil) works without
// them.
//
// Styles and scripts need no registration: web/static/css/*.css and
// web/static/js/*.js are linked from the layout, in name order, after app.css
// and app.js, with content-hashed URLs. No inline script or style works: the
// Content-Security-Policy allows files of this origin only. No part adds
// template functions; the counts behind the rail entries come from
// SetNavCount.
//
// Everything registered lives in a Registry. The init functions fill the
// default one, which Run uses; tests build their own with NewRegistry.
package ui
