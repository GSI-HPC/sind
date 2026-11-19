// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !linux

package cmdexec

import "os/exec"

// orphanSignal does nothing: only Linux signals a child when its parent
// dies.
func orphanSignal(*exec.Cmd) {}
