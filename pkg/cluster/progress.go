// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"

	"github.com/GSI-HPC/go-clikit/progress"
)

// stopped is the error of work that ended because the context it ran
// under was canceled for another reason than its own failure: a sibling
// in a fail-fast errgroup failed first, or the command was interrupted.
// Its span ends canceled, with the work's own error as its text, whatever
// that error says: a docker command killed by the cancellation fails with
// "signal: killed" alone.
type stopped struct{ error }

func (e stopped) Unwrap() error { return e.error }

// ProgressClass says that the work was stopped, not failed.
func (stopped) ProgressClass() progress.Class { return progress.ClassCanceled }

// endSpan ends span with err, the outcome of work that ran under ctx, the
// context of a fail-fast errgroup or one below it: as canceled (stopped)
// when ctx had been canceled with another cause than err by the time the
// work returned. Work that fails first ends its span before it returns to
// its group, so ctx has not been canceled yet and the span fails with err;
// work whose own deadline ran out, such as a --wait limit, is no
// cancellation and keeps the class of its error.
func endSpan(ctx context.Context, span *progress.Span, err error) {
	if err != nil && errors.Is(ctx.Err(), context.Canceled) && !errors.Is(context.Cause(ctx), err) {
		err = stopped{err}
	}
	span.End(err)
}

// startStep starts the progress step name under ctx, and returns its
// context and the function that ends it with the error of its work, as
// endSpan does.
func startStep(ctx context.Context, name string, opts ...progress.Option) (context.Context, func(error)) {
	ctx, step := progress.Start(ctx, progress.KindStep, name, opts...)
	return ctx, func(err error) { endSpan(ctx, step, err) }
}

// startTargets announces a progress target under ctx for each name, queued,
// with Node set to the name and the role role gives it, and returns their
// contexts and spans, in the order of names. A counted step's targets are
// all announced before the first of them runs (progresstest.Check), so a
// pool starts them all first and marks each running (Span.Run) when its
// work starts.
func startTargets(ctx context.Context, names []string, role func(i int) string) ([]context.Context, []*progress.Span) {
	ctxs := make([]context.Context, len(names))
	spans := make([]*progress.Span, len(names))
	for i, name := range names {
		ctxs[i], spans[i] = progress.Start(ctx, progress.KindTarget, name,
			progress.Queued(), progress.Node(name), progress.Role(role(i)))
	}
	return ctxs, spans
}

// nodeTargets announces a progress target for each node, as startTargets
// does, named by the node's short name and with its role.
func nodeTargets(ctx context.Context, nodes []RunConfig) ([]context.Context, []*progress.Span) {
	names := make([]string, len(nodes))
	for i, nc := range nodes {
		names[i] = nc.ShortName
	}
	return startTargets(ctx, names, func(i int) string { return string(nodes[i].Role) })
}
