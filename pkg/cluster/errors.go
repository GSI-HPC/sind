// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"errors"
	"fmt"
	"time"
)

// Errors that library callers can test for with errors.Is. The errors
// pkg/cluster returns wrap them and carry their own message.
//
// The rule for the two not-found sentinels: a function that takes the name
// of a cluster that must exist fails with an error that wraps
// ErrClusterNotFound when the realm holds no such cluster, and one that
// takes node names fails with ErrNodeNotFound for a node the cluster does
// not have. WorkerRemove follows the rule too. The exceptions: Delete of a
// cluster that does not exist is no error, and NodeServices, which takes
// one node, reports a missing cluster as a missing node.
var (
	// ErrClusterExists reports that resources with the names of the
	// cluster to create exist already (preflightCheck, Create).
	ErrClusterExists = errors.New("cluster resources exist")
	// ErrClusterNotFound reports that a cluster, or its controller, does
	// not exist in the realm: GetStatus, GetNodes, GetNode, GetAuthKey,
	// EnterTarget, GetSlurmInfo, CheckNode, WaitNodes, WorkerAdd,
	// WorkerRemove and the Power functions, among others. Delete returns
	// nil instead.
	ErrClusterNotFound = errors.New("cluster not found")
	// ErrNodeNotFound reports that a named node does not exist in its
	// cluster: GetNode, CheckNode, NodeServices, WaitNodes, WorkerRemove
	// and the Power functions, among others.
	ErrNodeNotFound = errors.New("node not found")
	// ErrNodesConfMissing reports that the controller has no
	// sind-nodes.conf, so sind cannot add managed workers (WorkerAdd);
	// unmanaged ones (WorkerAddOptions.Unmanaged) leave the Slurm
	// configuration alone.
	ErrNodesConfMissing = errors.New("sind-nodes.conf not found on controller: managed workers require sind-generated Slurm configuration")
	// ErrNotReady reports that the nodes or Slurm of a Create or WorkerAdd
	// did not become ready within its wait limit (config.Cluster.Wait,
	// WorkerAddOptions.Wait).
	ErrNotReady = errors.New("not ready")
	// ErrNetworkFull reports that the nodes of a Create or WorkerAdd do
	// not fit on the realm's mesh or the cluster network, Docker bridge
	// networks of at most docker.MaxBridgeEndpoints containers
	// (preflightCheck, Create, WorkerAdd).
	ErrNetworkFull = errors.New("network full")
	// ErrImageUnsupported reports that a monitored cluster's image cannot
	// serve the HTTP endpoints the monitoring stack uses: a Slurm older
	// than slurm.MonitoringMinVersion, or a missing plugin (Create,
	// WorkerAdd).
	ErrImageUnsupported = errors.New("image unsupported")
	// ErrNoMonitoring reports that the realm has no monitoring stack
	// (GetMonitoring).
	ErrNoMonitoring = errors.New("no monitoring stack")
)

// RollbackTimeout bounds the rollback of a failed Create or WorkerAdd, and
// the removal of a mesh that sind create cluster could not finish. The
// writes that must not be cut short (detach.Context) have the same bound
// outside a rollback. The
// rollback runs on a context that ignores the caller's cancellation (an
// interrupted create still cleans up), so without a bound a hung Docker
// daemon would hold it forever; what is left then is reported.
const RollbackTimeout = 5 * time.Minute

// sentinelError is an error with its own message that wraps a sentinel,
// and the error that caused it when there is one.
type sentinelError struct {
	msg      string
	sentinel error
	cause    error
}

func (e *sentinelError) Error() string { return e.msg }

func (e *sentinelError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.sentinel}
	}
	return []error{e.sentinel, e.cause}
}

// errorWith returns an error with the formatted message that wraps
// sentinel, so that errors.Is matches it and the message stays as it was.
func errorWith(sentinel error, format string, args ...any) error {
	return &sentinelError{msg: fmt.Sprintf(format, args...), sentinel: sentinel}
}

// errorWithCause is errorWith that also wraps cause, so that errors.Is and
// errors.As match it too, while the message stays the formatted one.
func errorWithCause(sentinel, cause error, format string, args ...any) error {
	return &sentinelError{msg: fmt.Sprintf(format, args...), sentinel: sentinel, cause: cause}
}
