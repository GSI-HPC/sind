// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"context"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/doctor"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/spf13/afero"
)

// Doctor runs the host checks (pkg/doctor) once when it starts and again
// each time Request is called, and keeps the report in the store. While a
// check runs, the report says so (store.DoctorReport.Running) next to the
// outcome of the last one.
type Doctor struct {
	Store  *store.Store
	Client *docker.Client
	// Fs is the file system the checks read; nil is the real one.
	Fs afero.Fs
	// Now returns the time of a report; nil is time.Now.
	Now func() time.Time

	// ask holds a request that has not started a run yet.
	ask chan struct{}
}

// NewDoctor returns a Doctor that checks through client.
func NewDoctor(st *store.Store, client *docker.Client) *Doctor {
	return &Doctor{Store: st, Client: client, ask: make(chan struct{}, 1)}
}

// Request makes Run check again. Requests made while a check runs are one
// more check afterwards.
func (d *Doctor) Request() {
	select {
	case d.ask <- struct{}{}:
	default:
	}
}

// Run checks at once and then on request, until ctx ends.
func (d *Doctor) Run(ctx context.Context) {
	for {
		d.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-d.ask:
		}
	}
}

// check runs the host checks and stores the report.
func (d *Doctor) check(ctx context.Context) {
	last := d.Store.Snapshot().Doctor
	running := store.DoctorReport{Running: true}
	if last != nil {
		running.Time, running.Checks = last.Time, last.Checks
	}
	d.Store.SetDoctor(&running)

	fs := d.Fs
	if fs == nil {
		fs = afero.NewOsFs()
	}
	// An invalid SIND_REALM only affects the checks of the host DNS, which
	// the default realm answers just as well.
	realm, err := cluster.ResolveRealm(nil, "")
	if err != nil {
		realm = mesh.DefaultRealm
	}
	checks := doctor.Run(ctx, doctor.Deps{Client: d.Client, Fs: fs, Mesh: mesh.NewHostManager(d.Client, realm)})
	if ctx.Err() != nil {
		// The checks were cut short: leave the last report as it was.
		d.Store.SetDoctor(last)
		return
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	report := store.DoctorReport{Time: now()}
	report.Checks = checks
	d.Store.SetDoctor(&report)
}
