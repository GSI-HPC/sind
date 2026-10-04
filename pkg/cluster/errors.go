// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"errors"
	"fmt"
	"time"
)

// Errors that library callers can test for with errors.Is. The errors
// pkg/cluster returns wrap them and carry their own message.
var (
	// ErrClusterExists reports that resources with the names of the
	// cluster to create exist already (PreflightCheck, Create).
	ErrClusterExists = errors.New("cluster resources exist")
	// ErrClusterNotFound reports that a cluster, or its controller, does
	// not exist in the realm (GetStatus, WorkerAdd).
	ErrClusterNotFound = errors.New("cluster not found")
	// ErrNodeNotFound reports that a named node does not exist in its
	// cluster (power operations, WorkerRemove).
	ErrNodeNotFound = errors.New("node not found")
	// ErrNodesConfMissing reports that the controller has no
	// sind-nodes.conf, so sind cannot add managed workers (WorkerAdd).
	ErrNodesConfMissing = errors.New("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration; use --unmanaged to add nodes without modifying Slurm config")
	// ErrNotReady reports that the nodes or Slurm of a Create or WorkerAdd
	// did not become ready within its wait limit (config.Cluster.Wait,
	// WorkerAddOptions.Wait).
	ErrNotReady = errors.New("not ready")
)

// rollbackTimeout bounds the rollback of a failed Create or WorkerAdd. The
// rollback runs on a context that ignores the caller's cancellation (an
// interrupted create still cleans up), so without a bound a hung Docker
// daemon would hold it forever; what is left then is reported.
const rollbackTimeout = 5 * time.Minute

// sentinelError is an error with its own message that wraps a sentinel.
type sentinelError struct {
	msg      string
	sentinel error
}

func (e *sentinelError) Error() string { return e.msg }

func (e *sentinelError) Unwrap() error { return e.sentinel }

// errorWith returns an error with the formatted message that wraps
// sentinel, so that errors.Is matches it and the message stays as it was.
func errorWith(sentinel error, format string, args ...any) error {
	return &sentinelError{msg: fmt.Sprintf(format, args...), sentinel: sentinel}
}
