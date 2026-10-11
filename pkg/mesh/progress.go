// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"context"
	"errors"

	"github.com/GSI-HPC/go-clikit/progress"
)

// The parts of the mesh, the targets of the progress step "mesh" that
// EnsureMesh reports, and the reasons a part is skipped when it was in
// place already.
const (
	partNetwork = "network"
	partDNS     = "dns"
	partVolume  = "volume"
	partRelay   = "relay"

	skipExists  = "exists"
	skipRunning = "running"
)

// part is the progress target of a part of the mesh, and the context its
// work runs under.
type part struct {
	ctx  context.Context
	span *progress.Span
}

// startPart announces the part name under ctx, queued until it runs.
func startPart(ctx context.Context, name string) part {
	ctx, span := progress.Start(ctx, progress.KindTarget, name, progress.Queued(), progress.Node(name))
	return part{ctx: ctx, span: span}
}

// run marks the part running, sees to it with work under its target, and
// ends the target with how that went: skipped for reason when work changed
// nothing, canceled when work failed because the group it runs in stopped
// it, as the failure of another part cancels the group's context, and
// otherwise failed with work's error, or ok. It returns that error.
func (p part) run(reason string, work func(ctx context.Context) (changed bool, err error)) error {
	p.span.Run()
	changed, err := work(p.ctx)
	switch {
	case err != nil && errors.Is(p.ctx.Err(), context.Canceled) && !errors.Is(context.Cause(p.ctx), err):
		p.span.End(stopped{err})
	case err != nil:
		p.span.End(err)
	case !changed:
		p.span.Skip(reason)
	default:
		p.span.End(nil)
	}
	return err
}

// stopped is the error of a part whose work its group, or an interrupt,
// stopped: a docker command killed by the cancellation fails with
// "signal: killed" alone, which is no failure of the part.
type stopped struct{ error }

func (e stopped) Unwrap() error { return e.error }

// ProgressClass says that the work was stopped, not failed.
func (stopped) ProgressClass() progress.Class { return progress.ClassCanceled }
