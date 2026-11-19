// SPDX-License-Identifier: LGPL-3.0-or-later

package itest

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
)

// endT records how a test ended. Fatalf and Skipf end the goroutine, as
// those of testing.T do.
type endT struct {
	testing.TB
	ended string
}

func (e *endT) Helper() {}

func (e *endT) Fatalf(format string, args ...any) {
	e.ended = "failed: " + fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func (e *endT) Skipf(format string, args ...any) {
	e.ended = "skipped: " + fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// run runs f with an endT as a test of its own goroutine, and returns how
// it ended, or "" when it went on.
func run(f func(t testing.TB)) string {
	e := &endT{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(e)
	}()
	<-done
	return e.ended
}

func TestUnavailable(t *testing.T) {
	t.Setenv(RequireEnv, "")
	assert.Equal(t, "skipped: no daemon 1", run(func(t testing.TB) { Unavailable(t, "no daemon %d", 1) }))

	t.Setenv(RequireEnv, "1")
	assert.Equal(t, "failed: no daemon 1 (SIND_TEST_REQUIRE_DOCKER is set)", run(func(t testing.TB) { Unavailable(t, "no daemon %d", 1) }))
}

func TestProber_RunsDockerInfoOnce(t *testing.T) {
	t.Setenv(RequireEnv, "1")
	m := &mock.Executor{}
	m.AddResult("29.0.0\n", "", nil)
	p := &Prober{Executor: m}

	assert.Empty(t, run(p.Require))
	assert.Empty(t, run(p.Require))

	assert.Equal(t, []mock.Call{{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}}}, m.Calls)
}

func TestProber_NoDaemon(t *testing.T) {
	t.Setenv(RequireEnv, "1")
	m := &mock.Executor{}
	m.AddResult("", "", errors.New("Cannot connect to the Docker daemon"))
	p := &Prober{Executor: m}

	want := "failed: docker reaches no daemon: Cannot connect to the Docker daemon (SIND_TEST_REQUIRE_DOCKER is set)"
	assert.Equal(t, want, run(p.Require))
	assert.Equal(t, want, run(p.Require), "the answer of the first probe")
	assert.Len(t, m.Calls, 1)
}

func TestProber_NoServerVersion(t *testing.T) {
	t.Setenv(RequireEnv, "1")
	m := &mock.Executor{}
	m.AddResult("\n", "", nil)
	p := &Prober{Executor: m}

	assert.Equal(t, "failed: docker reaches no daemon: docker info reports no server version (SIND_TEST_REQUIRE_DOCKER is set)", run(p.Require))
}

func TestRequireDocker(t *testing.T) {
	t.Setenv(RequireEnv, "")
	m := &mock.Executor{}
	m.AddResult("", "", errors.New("no docker"))
	saved := Docker
	t.Cleanup(func() { Docker = saved })
	Docker = &Prober{Executor: m}

	assert.Equal(t, "skipped: docker reaches no daemon: no docker", run(RequireDocker))
}

func TestRunID(t *testing.T) {
	assert.Regexp(t, `^[0-9a-f]{8}$`, RunID)
}
