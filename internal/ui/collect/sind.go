// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"cmp"
	"context"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// The defaults of Sind.
const (
	// sindPoll is how often everything is read when no event says so.
	sindPoll = 15 * time.Second
	// sindRetry is how often Docker is asked again while it does not
	// answer.
	sindRetry = 30 * time.Second
)

// Sind reads the sind data of Docker into the store: the realms and, for
// each realm, its clusters, nodes, networks, volumes and mesh. It reads
// the realms that an event names, and everything every Poll, or every
// Retry while Docker does not answer, which it reports with
// store.SetDocker.
type Sind struct {
	Store  *store.Store
	Client *docker.Client
	Bus    *Bus
	Log    *slog.Logger

	// Poll and Retry replace the defaults (15 s, 30 s) when set.
	Poll, Retry time.Duration

	// known are the realms the store has data of.
	known map[string]bool
	// host is what docker info says of Docker's host, read once.
	host *docker.DaemonInfo
}

func (s *Sind) logger() *slog.Logger { return logOr(s.Log) }

// logOr returns l, or a logger that discards.
func logOr(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.New(slog.DiscardHandler)
}

// Run reads until ctx ends.
func (s *Sind) Run(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer sub.Close()
	s.known = map[string]bool{}
	var (
		all      = true
		pending  = map[string]bool{}
		deadline time.Time
	)
	for {
		if all || len(pending) > 0 {
			down := s.pass(ctx, all, pending)
			if all {
				every := cmp.Or(s.Poll, sindPoll)
				if down {
					every = cmp.Or(s.Retry, sindRetry)
				}
				deadline = time.Now().Add(every)
			}
			all = false
			clear(pending)
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			all = true
		case <-sub.C():
			timer.Stop()
			for _, ev := range sub.Drain() {
				switch {
				case ev.Realm == "":
					all = true
				case ev.Kind != KindLock:
					pending[ev.Realm] = true
				}
			}
		}
	}
}

// pass reads the realm list and then the data of every realm (all) or of the
// realms in pending. It reports whether Docker did not answer.
func (s *Sind) pass(ctx context.Context, all bool, pending map[string]bool) (down bool) {
	realms, err := cluster.GetRealms(ctx, s.Client)
	if err != nil {
		if ctx.Err() == nil {
			s.Store.SetDocker(store.DockerState{Checked: true, Err: doctor.ErrorLine(err)})
		}
		return true
	}
	s.Store.SetDocker(s.dockerState(ctx))
	s.Store.SetRealms(deref(realms))

	present := map[string]bool{}
	for _, r := range realms {
		present[r.Name] = true
	}
	targets := pending
	if all {
		targets = maps.Clone(present)
		for r := range s.known {
			targets[r] = true
		}
	}
	var wg sync.WaitGroup
	for r := range targets {
		if !present[r] {
			s.forget(r)
			continue
		}
		s.known[r] = true
		wg.Go(func() { s.readRealm(ctx, r) })
	}
	wg.Wait()
	return false
}

// dockerState is the state of a Docker that answers, with the size of its
// host: docker info runs until it has said it once.
func (s *Sind) dockerState(ctx context.Context) store.DockerState {
	if s.host == nil {
		s.host, _ = s.Client.Info(ctx)
	}
	d := store.DockerState{Checked: true}
	if s.host != nil {
		d.MemTotal, d.NCPU = s.host.MemTotal, s.host.NCPU
	}
	return d
}

// forget removes what the store holds of a realm that is gone.
func (s *Sind) forget(realm string) {
	delete(s.known, realm)
	s.Store.SetClusters(realm, nil)
	s.Store.SetNodes(realm, nil)
	s.Store.SetNetworks(realm, nil)
	s.Store.SetVolumes(realm, nil)
	s.Store.SetMesh(realm, store.Mesh{})
}

// readRealm reads the clusters, nodes, networks, volumes and mesh of a
// realm. What cannot be read stays as the store has it.
func (s *Sind) readRealm(ctx context.Context, realm string) {
	var wg sync.WaitGroup
	wg.Go(func() {
		if v, err := cluster.GetClusters(ctx, s.Client, realm); s.ok(ctx, realm, "clusters", err) {
			s.Store.SetClusters(realm, deref(v))
		}
	})
	wg.Go(func() {
		if v, err := cluster.GetAllNodes(ctx, s.Client, realm); s.ok(ctx, realm, "nodes", err) {
			s.Store.SetNodes(realm, deref(v))
		}
	})
	wg.Go(func() {
		if v, err := cluster.GetNetworks(ctx, s.Client, realm); s.ok(ctx, realm, "networks", err) {
			s.Store.SetNetworks(realm, deref(v))
		}
	})
	wg.Go(func() {
		if v, err := cluster.GetVolumes(ctx, s.Client, realm); s.ok(ctx, realm, "volumes", err) {
			s.Store.SetVolumes(realm, deref(v))
		}
	})
	wg.Go(func() {
		if m, ok := readMesh(ctx, s.Client, realm); ok {
			s.Store.SetMesh(realm, m)
		}
	})
	wg.Wait()
}

// ok reports that err is nil, and logs what failed otherwise.
func (s *Sind) ok(ctx context.Context, realm, what string, err error) bool {
	if err != nil && ctx.Err() == nil {
		s.logger().Debug("reading "+what, "realm", realm, "err", err)
	}
	return err == nil
}

// noMesh is what mesh.Manager.GetInfo says for a realm without mesh.
const noMesh = "no mesh found for realm"

// readMesh reads the mesh of a realm: its infrastructure, DNS records and
// the containers outside sind that are on its network. A realm without
// mesh has the zero Mesh; a mesh that cannot be read fully has Err.
func readMesh(ctx context.Context, client *docker.Client, realm string) (store.Mesh, bool) {
	mgr := mesh.NewHostManager(client, realm)
	info, err := mgr.GetInfo(ctx)
	if err != nil {
		if strings.Contains(err.Error(), noMesh) {
			return store.Mesh{}, true
		}
		return store.Mesh{Err: doctor.ErrorLine(err)}, ctx.Err() == nil
	}
	m := store.Mesh{Info: info}
	var errs []string
	if m.Records, err = mgr.GetDNSRecords(ctx); err != nil {
		errs = append(errs, doctor.ErrorLine(err))
	}
	external, err := cluster.ExternalContainers(ctx, client, mgr)
	if err != nil {
		errs = append(errs, doctor.ErrorLine(err))
	}
	for _, name := range external {
		m.External = append(m.External, string(name))
	}
	m.Err = strings.Join(errs, "; ")
	return m, ctx.Err() == nil
}

// deref copies the values that a getter returns by pointer.
func deref[T any](in []*T) []T {
	out := make([]T, len(in))
	for i, p := range in {
		out[i] = *p
	}
	return out
}
