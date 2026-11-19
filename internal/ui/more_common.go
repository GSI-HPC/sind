// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The "more" part: Mesh and DNS, Monitoring, Topology and Doctor. This file
// holds what the four pages share.

func init() {
	defaultRegistry.SetNavCount("monitoring", moreMonitoringCount)
	defaultRegistry.SetNavCount("doctor", moreDoctorCount)
}

// viewEnv returns the value under key in the Env of the view's server, the
// zero value when no service put one of type T there.
func viewEnv[T any](v *View, key any) T {
	t, _ := v.Env(key).(T)
	return t
}

// moreNotice is the plain statement of a page that has nothing to show,
// with an optional link on to what the reader does about it.
type moreNotice struct {
	Text, Href, HrefText string
}

// moreNoRealms says why there is no realm to show.
func moreNoRealms(d store.DockerState) moreNotice {
	switch {
	case !d.Checked:
		return moreNotice{Text: "sind ui has not reached Docker yet. This page fills in when it has."}
	case d.Err != "":
		return moreNotice{Text: "Docker is not reachable: " + d.Err + ".", Href: "/doctor", HrefText: "Open Doctor"}
	default:
		return moreNotice{Text: "No realm yet. A realm exists once it has a cluster.", Href: "/clusters/new", HrefText: "New cluster…"}
	}
}

// moreDockerDown returns why Docker is unreachable, empty when it is not,
// or when nothing has been tried.
func moreDockerDown(d store.DockerState) string {
	if d.Checked {
		return d.Err
	}
	return ""
}

// moreRealms returns the realms a page shows: the one named by ?realm=, or
// every realm. The page answers a realm that does not exist (pgRealmRoute),
// so a live update keeps showing a realm whose last cluster went.
func moreRealms(v *View) []string {
	if r := v.Params.Get("realm"); r != "" {
		return []string{r}
	}
	names := make([]string, len(v.Snap.Realms))
	for i, r := range v.Snap.Realms {
		names[i] = r.Name
	}
	return names
}
