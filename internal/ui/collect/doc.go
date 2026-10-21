// SPDX-License-Identifier: LGPL-3.0-or-later

// Package collect fills the store of sind ui with what Docker and the host
// know: the realms, clusters, nodes, networks and volumes, the mesh, the
// status of the clusters whose page is open, the realm locks, the
// monitoring stacks and the host checks.
//
// sind keeps no state of its own, so the collectors read cheaply and
// often, read expensively only for what a page shows, and let Docker
// events say when to read again. Events streams `docker events` once and
// publishes what it sees, debounced, on a Bus; each collector subscribes
// and reads again for the realm (and cluster) an event names. Every
// collector also reads on a timer, which catches what no event reports.
//
// The collectors do not notify when they read the same data again (the
// setters of the store compare), so an idle host sends nothing to the
// browsers. Every loop takes its intervals from fields, which tests set
// short, and ends when its context does.
package collect
