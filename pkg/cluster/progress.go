// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"io"

	"github.com/GSI-HPC/go-clikit/fanout"
	"github.com/GSI-HPC/go-clikit/progress"
)

// program names sind in the error a panic in the work of one of its pools
// becomes, "sind panicked; this is a bug, please report it: ...", and in
// front of the line that reports the panic, with or without a Bus.
const program = "sind"

// panicLogKey is the context key of the writer WithPanicLog names.
type panicLogKey struct{}

// WithPanicLog returns ctx with w as where the pools of the power commands
// and of DeleteAll write the line and the stack of a panic in the work for
// one node or cluster while ctx carries no progress Bus, as the command's
// stderr: with a Bus, they write them to its PanicLog, which a display
// shows above itself; with neither, to the process's standard error.
func WithPanicLog(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, panicLogKey{}, w)
}

// panicLog returns the fanout.MapOptions.PanicLog of a pool that runs
// under ctx: nil, which is the Bus's PanicLog, when ctx carries a Bus, and
// the writer WithPanicLog put in ctx otherwise; nil, the process's
// standard error, without either.
func panicLog(ctx context.Context) io.Writer {
	if progress.BusFrom(ctx) != nil {
		return nil
	}
	w, _ := ctx.Value(panicLogKey{}).(io.Writer)
	return w
}

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

// joinFailures returns the fanout.MapOptions.Summarize of a pool that
// tries every item and returns the error of each that failed, as wrap
// words it, joined in the order of the items as errors.Join joins them:
// nil when none failed. Its progress class is canceled when every item
// that failed ended canceled, as the end of the context ends them, and the
// target's otherwise, as fanout.Failure's is; a class of the items' errors
// does not decide it, since one of them is no more the whole's than
// another.
func joinFailures(wrap func(fanout.Failed) error) func(fanout.Summary) error {
	return func(s fanout.Summary) error {
		if len(s.Failed) == 0 {
			return nil
		}
		errs := make([]error, len(s.Failed))
		for i, f := range s.Failed {
			errs[i] = wrap(f)
		}
		class := progress.ClassTarget
		if s.Canceled {
			class = progress.ClassCanceled
		}
		return poolFailure{error: errors.Join(errs...), class: class}
	}
}

// poolFailure is the error of a pool some of whose items failed, as
// joinFailures makes it: their errors, joined, and the pool's class.
type poolFailure struct {
	error
	class progress.Class
}

func (e poolFailure) Unwrap() error { return e.error }

// ProgressClass says why the pool failed as a whole.
func (e poolFailure) ProgressClass() progress.Class { return e.class }
