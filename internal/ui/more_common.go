// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"fmt"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The "more" part: Mesh and DNS, Monitoring, Topology and Doctor. This file
// holds what the four pages share.

// moreEnv is the Env of the running server, which Setup of the "more"
// service keeps for the Doctor page to look for the key "collect.doctor":
// a page's Load sees only its View.
var moreEnv atomic.Pointer[Env]

func init() {
	RegisterService(Service{
		Name:  "more",
		Setup: func(env *Env) error { moreEnv.Store(env); return nil },
	})
	SetNavCount("monitoring", moreMonitoringCount)
	SetNavCount("doctor", moreDoctorCount)
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
		return moreNotice{Text: "No realm yet. A realm exists once it has a cluster.", Href: "/dialogs/create-cluster", HrefText: "New cluster…"}
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
// every realm. An unknown realm is ErrNotFound.
func moreRealms(v *View) ([]string, error) {
	if r := v.Params.Get("realm"); r != "" {
		if _, ok := v.Snap.Realm(r); !ok {
			return nil, pgRealmMiss(v.Snap, navHref(v.Nav), r)
		}
		return []string{r}, nil
	}
	names := make([]string, len(v.Snap.Realms))
	for i, r := range v.Snap.Realms {
		names[i] = r.Name
	}
	return names, nil
}

// moreDialogURL is the address of a dialog of the actions part.
func moreDialogURL(action string, kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	return "/dialogs/" + action + "?" + q.Encode()
}

// moreAgo says how long before now t was, as "just now", "3 min ago".
func moreAgo(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 10*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d d ago", int(d.Hours()/24))
}
