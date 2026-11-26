// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package main

import (
	"context"
	"encoding/json"
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

// progressPrefix and deleteProgressPrefix start each line of the plain
// progress of a create and of a delete, after its time.
const (
	progressPrefix       = "create cluster › "
	deleteProgressPrefix = "delete cluster › "
)

// waitForLine waits until the plain progress in file has the line, or
// until ended closes, and reports whether it saw the line.
func waitForLine(file, line string, ended <-chan struct{}) bool {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if out, err := os.ReadFile(file); err == nil && strings.Contains(string(out), line) {
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
// once the tests have passed, and with the filters of more.
func realmObjects(ctx context.Context, t *testing.T, c *docker.Client, realm string, more ...string) []string {
	t.Helper()
	filters := append([]string{"label=" + cluster.LabelRealm + "=" + realm}, more...)
	var objects []string
	containers, err := c.ListContainers(ctx, filters...)
	require.NoError(t, err)
	for _, e := range containers {
		objects = append(objects, "container "+string(e.Name))
	}
	networks, err := c.ListNetworks(ctx, filters...)
	require.NoError(t, err)
	for _, e := range networks {
		objects = append(objects, "network "+string(e.Name))
	}
	volumes, err := c.ListVolumes(ctx, filters...)
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

// sindProcess is a sind that a test runs as a process of its own, to kill
// it: this test binary as sind (see TestMain).
type sindProcess struct {
	cmd   *exec.Cmd
	out   string        // the file that holds its output
	ended chan struct{} // closed once it has ended
	err   error         // how it ended, once ended is closed
}

// startSind starts sind with args in the realm, with plain progress. Its
// output goes to a file: with a pipe, Wait would not return before the
// docker processes that a killed sind leaves end.
func startSind(ctx context.Context, t *testing.T, realm string, args ...string) *sindProcess {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	out, err := os.Create(filepath.Join(t.TempDir(), "sind.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = out.Close() })
	p := &sindProcess{out: out.Name(), ended: make(chan struct{})}
	p.cmd = exec.CommandContext(ctx, self, append([]string{"--realm", realm, "--progress", "plain"}, args...)...)
	p.cmd.Env = append(os.Environ(), childEnv+"=main")
	p.cmd.Stdout, p.cmd.Stderr = out, out
	require.NoError(t, p.cmd.Start())
	go func() {
		p.err = p.cmd.Wait()
		close(p.ended)
	}()
	return p
}

// log returns what the process has written.
func (p *sindProcess) log() string {
	b, _ := os.ReadFile(p.out)
	return string(b)
}

// kill kills the process with SIGKILL and waits for its end.
func (p *sindProcess) kill() {
	_ = p.cmd.Process.Kill() // an error: the process has just ended
	<-p.ended
}

// killAtLine kills the process as soon as its plain progress has the line,
// and fails the test when the process ends without it.
func (p *sindProcess) killAtLine(t *testing.T, line string) {
	t.Helper()
	if !waitForLine(p.out, line, p.ended) {
		require.FailNow(t, "sind ended without the line "+line, "error: %v\n%s", p.err, p.log())
	}
	p.kill()
}

// deleteUntilQuiet runs sind delete cluster, which must succeed, until it
// warns no more, at most 3 times, and returns what the last run wrote to
// standard error. A delete that warns may leave something, and says so:
// the user then runs it again.
func deleteUntilQuiet(ctx context.Context, t *testing.T, realm, name string) string {
	t.Helper()
	for run := 1; ; run++ {
		_, stderr, err := executeWithRealmCtx(ctx, realm, "delete", "cluster", name)
		require.NoError(t, err, "delete %d after the kill: stderr=%q", run, stderr)
		if !strings.Contains(stderr, "Warning:") {
			return stderr
		}
		t.Logf("delete %d warns: %s", run, stderr)
		require.Less(t, run, 3, "delete %d still warns: stderr=%q", run, stderr)
	}
}

// requireNoRealmProcess fails the test when a process that names the realm
// still runs 10 seconds from now: the docker calls of a killed sind end
// with what they work on, at the latest.
func requireNoRealmProcess(t *testing.T, realm string) {
	t.Helper()
	assert.Eventually(t, func() bool { return len(realmProcesses(realm)) == 0 }, 10*time.Second, 100*time.Millisecond,
		"docker calls of the killed sind still run after the delete")
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
// A killed sind must also leave no process that runs on: docker events,
// which the readiness watcher starts, does not end with its reader, so the
// kernel ends it with sind (execopt.WithDiesWithParent, on Linux). The
// test looks for processes that name the realm, where there is a /proc.
func TestDeleteAfterKilledCreate(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	checkPrerequisites(t, c)
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

			create := startSind(ctx, t, realm, "create", "cluster", "--config", cfgPath, "--data", t.TempDir())
			if point.line != "" {
				create.killAtLine(t, progressPrefix+point.line)
			} else {
				// A create that ends before its kill left a complete
				// cluster, which the delete takes as well.
				select {
				case <-create.ended:
					require.NoError(t, create.err, "the create failed before its kill:\n%s", create.log())
				case <-time.After(point.after):
					create.kill()
				}
			}
			t.Logf("the create wrote:\n%s", create.log())
			if point.notYet != "" {
				assert.NotContains(t, create.log(), progressPrefix+point.notYet, "the kill came too late")
			}
			t.Logf("the create left: %s", strings.Join(realmObjects(ctx, t, c, realm), ", "))

			stderr := deleteUntilQuiet(ctx, t, realm, "default")
			assert.Empty(t, realmObjects(ctx, t, c, realm), "the delete reported success without a warning: stderr=%q", stderr)
			requireNoRealmProcess(t, realm)
		})
	}
}

// deleteKillLines are the lines of the plain progress of sind delete
// cluster at which TestDeleteAfterKilledDelete kills it: the start of each
// step of a delete in a realm that keeps its mesh.
var deleteKillLines = []string{
	"mesh deregistration: start",
	"nodes: start",
	"network: start",
	"volumes: start",
}

// meshNames returns the DNS records and the known_hosts lines of the realm
// that name a node of the cluster.
func meshNames(ctx context.Context, t *testing.T, realm, name string) []string {
	t.Helper()
	domain := "." + name + "."
	var names []string
	stdout, stderr, err := executeWithRealmCtx(ctx, realm, "get", "dns", "-o", "json")
	require.NoError(t, err, "get dns: stderr=%q", stderr)
	var records []struct {
		Hostname string `json:"hostname"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &records), stdout)
	for _, r := range records {
		if strings.Contains(r.Hostname, domain) {
			names = append(names, "DNS record "+r.Hostname)
		}
	}
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "get", "ssh-known-hosts", "-o", "json")
	require.NoError(t, err, "get ssh-known-hosts: stderr=%q", stderr)
	var hosts struct {
		KnownHosts string `json:"known_hosts"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &hosts), stdout)
	for line := range strings.SplitSeq(hosts.KnownHosts, "\n") {
		if strings.Contains(line, domain) {
			names = append(names, "known_hosts entry "+strings.Fields(line)[0])
		}
	}
	return names
}

// TestDeleteAfterKilledDelete kills sind delete cluster with SIGKILL at the
// start of each of its steps, and then runs it again. The second delete
// must succeed, and once it reports no warning, nothing of the cluster is
// left: no container, network or volume, and no DNS record or known_hosts
// entry on the mesh, which another cluster of the realm keeps.
//
// It is the rule of spec/tla/DeleteRetry.tla and of TestDelete_FaultSweep
// (pkg/cluster) on a real daemon. The last subtest kills the delete of the
// last cluster of the realm when it starts to remove the mesh.
func TestDeleteAfterKilledDelete(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	checkPrerequisites(t, c)
	realm := "it-killdel-" + testID
	cleanupRealm(t, c, realm)
	t.Cleanup(func() { endRealmProcesses(t, realm) })
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()

	create := func(t *testing.T, name string) {
		t.Helper()
		cfgPath := filepath.Join(t.TempDir(), "cluster.yaml")
		cfg := "kind: Cluster\nname: " + name + "\ndefaults:\n  image: " + testutil.Image() + "\n"
		require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))
		stdout, stderr, err := executeWithRealmCtx(ctx, realm, "create", "cluster", "--config", cfgPath, "--data", t.TempDir())
		require.NoError(t, err, "create cluster %s: stdout=%q stderr=%q", name, stdout, stderr)
	}
	// The cluster that keeps the mesh of the realm.
	create(t, "keep")

	for i, line := range deleteKillLines {
		t.Run(line, func(t *testing.T) {
			name := fmt.Sprintf("gone%d", i)
			create(t, name)
			require.NotEmpty(t, meshNames(ctx, t, realm, name), "the cluster has no name on the mesh")

			del := startSind(ctx, t, realm, "delete", "cluster", name)
			del.killAtLine(t, deleteProgressPrefix+line)
			t.Logf("the delete wrote:\n%s", del.log())
			ofCluster := "label=" + cluster.LabelCluster + "=" + name
			t.Logf("the delete left: %s", strings.Join(realmObjects(ctx, t, c, realm, ofCluster), ", "))

			stderr := deleteUntilQuiet(ctx, t, realm, name)
			assert.Empty(t, realmObjects(ctx, t, c, realm, ofCluster), "the delete reported success without a warning: stderr=%q", stderr)
			assert.Empty(t, meshNames(ctx, t, realm, name), "the delete reported success without a warning: stderr=%q", stderr)
		})
	}

	t.Run("mesh: start", func(t *testing.T) {
		del := startSind(ctx, t, realm, "delete", "cluster", "keep")
		del.killAtLine(t, deleteProgressPrefix+"mesh: start")
		t.Logf("the delete wrote:\n%s", del.log())
		t.Logf("the delete left: %s", strings.Join(realmObjects(ctx, t, c, realm), ", "))

		stderr := deleteUntilQuiet(ctx, t, realm, "keep")
		assert.Empty(t, realmObjects(ctx, t, c, realm), "the delete reported success without a warning: stderr=%q", stderr)
		requireNoRealmProcess(t, realm)
	})
}
