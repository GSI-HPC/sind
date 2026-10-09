// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"

	"github.com/GSI-HPC/go-clikit/progress"
)

// startRollback starts the rollback of a failed Create or WorkerAdd, as the
// progress step "rollback", so that a display shows what runs while the
// command undoes its work. It returns the context the rollback runs under,
// which ignores the cancellation of ctx and which rollbackTimeout bounds,
// and the function that ends the step with the rollback's own error: what
// the rollback could not undo, which the command's error carries already.
func startRollback(ctx context.Context) (context.Context, func(error)) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	ctx, step := progress.Start(ctx, progress.KindStep, "rollback")
	return ctx, func(err error) {
		if err != nil {
			err = rollbackFailure{err}
		}
		step.End(err)
		cancel()
	}
}

// rollbackFailure is the error of a rollback that could not undo
// everything. It is classed by its own error alone, never by the Bus's
// fallback, which a command may have call every failure once it was
// interrupted canceled: the rollback runs on after an interrupt, so what
// it left behind is a failure, not work the interrupt stopped.
type rollbackFailure struct{ error }

func (e rollbackFailure) Unwrap() error { return e.error }

// ProgressClass is the class of the rollback's error without a fallback:
// the target's, unless the error says otherwise, as one that ran out of
// rollbackTimeout says it timed out.
func (e rollbackFailure) ProgressClass() progress.Class { return progress.Classify(e.error, nil) }
