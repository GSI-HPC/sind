// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"os/exec"
	"syscall"
)

// orphanSignal has the kernel send cmd SIGINT when the thread that started
// it ends, which it does when this process dies (WithOrphanSafe).
func orphanSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGINT
}
