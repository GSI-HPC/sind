// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// orphanSignal has the kernel send cmd SIGINT when the thread that started
// it ends, which it does when this process dies (execopt.WithOrphanSafe).
func orphanSignal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGINT
}

// startTied starts cmd so that the kernel kills it when this process
// dies, however it dies (execopt.WithDiesWithParent). The kernel sends the
// signal when the thread that started cmd ends, so a goroutine keeps that
// thread until the caller calls release, which it does once cmd has ended.
func startTied(cmd *exec.Cmd) (release func(), err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	started := make(chan error)
	ended := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := cmd.Start()
		started <- err
		if err == nil {
			<-ended
		}
	}()
	if err := <-started; err != nil {
		return func() {}, err
	}
	return sync.OnceFunc(func() { close(ended) }), nil
}
