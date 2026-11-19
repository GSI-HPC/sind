// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

// State represents the state of a cluster or node.
type State string

// Possible cluster/node states.
const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StatePaused  State = "paused"
	StateMixed   State = "mixed"   // cluster: nodes in different states
	StateEmpty   State = "empty"   // cluster: no nodes exist
	StateUnknown State = "unknown" // node: unrecognised container state
)
