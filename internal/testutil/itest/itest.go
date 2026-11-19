// SPDX-License-Identifier: LGPL-3.0-or-later

// Package itest holds what the integration tests of every package share:
// the check for a Docker daemon, whether a host that cannot run them skips
// or fails them, and the ID of the test process. It imports no package of
// sind but cmdexec, so that the tests of pkg/docker, which internal/testutil
// imports, can use it too.
package itest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// RequireEnv names the variable that makes Unavailable fail a test instead
// of skipping it. CI sets it, so that a host or a daemon that the tests
// cannot use fails the job instead of passing it with the tests skipped.
const RequireEnv = "SIND_TEST_REQUIRE_DOCKER"

// infoTimeout bounds the docker info of a Prober. It runs once per test
// process, at the first test that needs Docker.
const infoTimeout = 30 * time.Second

// RunID is a random suffix of this test process, for names that must not
// collide with those of other test processes on the same daemon.
var RunID = func() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}()

// Unavailable ends t because the host cannot run it, for the reason that
// format and args give: it skips t, or fails it when RequireEnv is set.
func Unavailable(t testing.TB, format string, args ...any) {
	t.Helper()
	if os.Getenv(RequireEnv) != "" {
		t.Fatalf(format+" ("+RequireEnv+" is set)", args...)
	}
	t.Skipf(format, args...)
}

// Prober checks once whether docker reaches a daemon, with docker info.
type Prober struct {
	Executor cmdexec.Executor

	once sync.Once
	err  error
}

// Require ends t with Unavailable unless docker reaches a daemon. Only
// its first call runs docker info; the later ones take its answer.
func (p *Prober) Require(t testing.TB) {
	t.Helper()
	p.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), infoTimeout)
		defer cancel()
		var stdout string
		stdout, _, p.err = p.Executor.Run(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
		// A docker CLI before v26 exits 0 with --format when it reaches
		// no daemon, and prints no server version.
		if p.err == nil && strings.TrimSpace(stdout) == "" {
			p.err = errors.New("docker info reports no server version")
		}
	})
	if p.err != nil {
		Unavailable(t, "docker reaches no daemon: %v", p.err)
	}
}

// Docker is the Prober of RequireDocker.
var Docker = &Prober{Executor: &cmdexec.OSExecutor{}}

// RequireDocker ends t with Unavailable unless docker reaches a daemon.
func RequireDocker(t testing.TB) {
	t.Helper()
	Docker.Require(t)
}
