// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build !linux

package cmdexec

import "os/exec"

// orphanSignal does nothing: only Linux signals a child when its parent
// dies.
func orphanSignal(*exec.Cmd) {}

// startTied starts cmd: only Linux kills a child when its parent dies.
func startTied(cmd *exec.Cmd) (release func(), err error) {
	return func() {}, cmd.Start()
}
