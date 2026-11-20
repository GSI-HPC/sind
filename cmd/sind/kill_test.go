// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// killPoints are the points at which TestDeleteAfterKilledCreate kills a
// create: when a line of its plain progress appears, or a time after the
// start of the create for the steps that have no line (the mesh, the
// checks, the network and the volumes). A line gives the same point on a
// fast and on a slow machine; a time does not.
var killPoints = []struct {
	name   string
	line   string        // the progress line that the test waits for, after "create cluster › "
	notYet string        // a line that tells that the kill came too late, if one must not be there
	after  time.Duration // the time after the start of the create, without a line
}{
	{name: "1s after the start", after: time.Second},
	{name: "3s after the start", after: 3 * time.Second},
	{name: "nodes start", line: "nodes: start", notYet: "nodes: done"},
	{name: "nodes boot", line: "nodes › controller › ready: waiting", notYet: "nodes: done"},
	{name: "mesh registration starts", line: "mesh registration: start", notYet: "mesh registration: done"},
	{name: "slurm starts on the controller", line: "slurm › controller › ready: waiting"},
	{name: "slurm starts in the cluster", line: "slurm › ready: waiting"},
}

// progressPrefix starts each line of the plain progress of a create, after
// its time.
const progressPrefix = "create cluster › "

// waitForLine waits until the plain progress in file has the line, or
// until ended closes, and reports whether it saw the line.
func waitForLine(file, line string, ended <-chan struct{}) bool {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if out, err := os.ReadFile(file); err == nil && strings.Contains(string(out), progressPrefix+line) {
			return true
		}
		select {
		case <-ended:
			return false
		case <-tick.C:
		}
	}
}

// realmObjects lists the containers, networks and volumes with the label
// of the realm, as the Integration Test job of CI does for every realm
// once the tests have passed.
func realmObjects(ctx context.Context, t *testing.T, c *docker.Client, realm string) []string {
	t.Helper()
	filter := "label=" + cluster.LabelRealm + "=" + realm
	var objects []string
	containers, err := c.ListContainers(ctx, filter)
	require.NoError(t, err)
	for _, e := range containers {
		objects = append(objects, "container "+string(e.Name))
	}
	networks, err := c.ListNetworks(ctx, filter)
	require.NoError(t, err)
	for _, e := range networks {
		objects = append(objects, "network "+string(e.Name))
	}
	volumes, err := c.ListVolumes(ctx, filter)
	require.NoError(t, err)
	for _, e := range volumes {
		objects = append(objects, "volume "+string(e.Name))
	}
	return objects
}

// realmProcess is a process whose command line names a realm.
type realmProcess struct {
	pid  int
	args string
}

// realmProcesses lists the processes whose command line names the realm:
// the docker calls of a killed create that still run. It reads /proc, so it
// finds nothing on a system without one.
func realmProcesses(realm string) []realmProcess {
	files, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	var processes []realmProcess
	for _, file := range files {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(file)))
		if err != nil || pid == os.Getpid() {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil || !strings.Contains(string(raw), realm) {
			continue
		}
		args := strings.ReplaceAll(strings.TrimRight(string(raw), "\x00"), "\x00", " ")
		processes = append(processes, realmProcess{pid: pid, args: args})
	}
	return processes
}

// endRealmProcesses kills the processes whose command line names the
// realm, and logs them.
func endRealmProcesses(t *testing.T, realm string) {
	t.Helper()
	for _, p := range realmProcesses(realm) {
		t.Logf("still runs, and is killed: %d: %s", p.pid, p.args)
		_ = syscall.Kill(p.pid, syscall.SIGKILL)
	}
}

// TestDeleteAfterKilledCreate kills sind create cluster with SIGKILL, so
// that no rollback runs, at several points of the create (killPoints), and
// then runs sind delete cluster. The delete must succeed, and once it
// reports no warning, Docker holds nothing of the realm: "sind delete
// cluster removes what is left".
//
// It is the rule of spec/tla/CreateRecover.tla and of TestCreate_FaultSweep
// (pkg/cluster) on a real daemon, where the docker calls of a killed create
// are processes of their own, and containers that it started still run.
// Each realm has one cluster, so its mesh goes with it.
//
// The rule is about what Docker holds. A killed sind can also leave a
// process: the docker events call of its readiness watcher learns that its
// reader is gone only when it writes the next event, which never comes for
// containers that are removed before they ran. The test does not check
// for it. It kills what it finds, so that no process of a test realm stays.
func TestDeleteAfterKilledCreate(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	checkPrerequisites(t, c)
	self, err := os.Executable()
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "cluster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("kind: Cluster\ndefaults:\n  image: "+testutil.Image()+"\n"), 0o644))

	// The points run one after the other, so that the test adds one
	// cluster at a time to what the daemon has to do for the other tests.
	for i, point := range killPoints {
		t.Run(point.name, func(t *testing.T) {
			realm := fmt.Sprintf("it-kill%d-%s", i, testID)
			cleanupRealm(t, c, realm)
			t.Cleanup(func() { endRealmProcesses(t, realm) })
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
			defer cancel()

			// The create is this test binary as sind (see TestMain). Its
			// output goes to a file: with a pipe, Wait would not return
			// before the docker processes that the create leaves end.
			out, err := os.Create(filepath.Join(t.TempDir(), "create.log"))
			require.NoError(t, err)
			defer func() { _ = out.Close() }()
			create := exec.CommandContext(ctx, self, "--realm", realm, "--progress", "plain",
				"create", "cluster", "--config", cfgPath, "--data", t.TempDir())
			create.Env = append(os.Environ(), childEnv+"=main")
			create.Stdout, create.Stderr = out, out
			require.NoError(t, create.Start())
			var createErr error
			ended := make(chan struct{})
			go func() {
				createErr = create.Wait()
				close(ended)
			}()
			log := func() string {
				b, _ := os.ReadFile(out.Name())
				return string(b)
			}

			if point.line != "" {
				// The kill follows the line at once.
				if !waitForLine(out.Name(), point.line, ended) {
					require.FailNow(t, "the create ended without the line "+point.line, "error: %v\n%s", createErr, log())
				}
				_ = create.Process.Kill() // an error: the create has just ended
				<-ended
			} else {
				// A create that ends before its kill left a complete
				// cluster, which the delete takes as well.
				select {
				case <-ended:
					require.NoError(t, createErr, "the create failed before its kill:\n%s", log())
				case <-time.After(point.after):
					_ = create.Process.Kill()
					<-ended
				}
			}
			t.Logf("the create wrote:\n%s", log())
			if point.notYet != "" {
				assert.NotContains(t, log(), progressPrefix+point.notYet, "the kill came too late")
			}
			t.Logf("the create left: %s", strings.Join(realmObjects(ctx, t, c, realm), ", "))

			// A delete that warns may leave something, and says so: the
			// user then runs it again.
			var stderr string
			for run := 1; ; run++ {
				_, stderr, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", "default")
				require.NoError(t, err, "delete %d after the kill: stderr=%q", run, stderr)
				if !strings.Contains(stderr, "Warning:") {
					break
				}
				t.Logf("delete %d warns: %s", run, stderr)
				require.Less(t, run, 3, "delete %d still warns: stderr=%q", run, stderr)
			}
			assert.Empty(t, realmObjects(ctx, t, c, realm), "the delete reported success without a warning: stderr=%q", stderr)
		})
	}
}
