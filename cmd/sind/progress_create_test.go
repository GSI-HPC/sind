// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/progresstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

// createDaemon answers the docker commands of sind create cluster for a
// realm whose mesh runs already, the way dockerd would for a cluster that
// gets ready at once: the cluster's resources do not exist before, the
// images are there, every node runs and its services are active. The realm
// lock's daemon part goes to a lockDaemon.
func createDaemon(t *testing.T) func(args []string, stdin string) mock.Result {
	t.Helper()
	exit1 := testutil.ExitCode1(t)
	lock := newLockDaemon(t)
	inspect := func(name string, networks map[string]string) string {
		type network struct {
			IPAddress string `json:"IPAddress"`
		}
		nets := map[string]network{}
		for n, ip := range networks {
			nets[n] = network{IPAddress: ip}
		}
		out, err := json.Marshal([]map[string]any{{
			"Id":              "id-" + name,
			"Name":            "/" + name,
			"State":           map[string]string{"Status": "running"},
			"Config":          map[string]any{"Labels": map[string]string{}},
			"NetworkSettings": map[string]any{"Networks": nets},
		}})
		require.NoError(t, err)
		return string(out) + "\n"
	}
	corefile := "sind.sind:53 {\n    hosts {\n        fallthrough\n    }\n}\n"
	return func(args []string, stdin string) mock.Result {
		joined := strings.Join(args, " ")
		last := args[len(args)-1]
		switch {
		case args[0] == "info":
			return mock.Result{Stdout: `{"ServerVersion":"28.0.0","CgroupVersion":"2"}` + "\n"}
		case args[0] == "network" && (last == "sind-lock" || args[1] == "rm" && lock.ids[args[2]] != ""):
			return lock.onCall(args, stdin)

		// The mesh runs.
		case joined == "network inspect sind-mesh --format {{json .Labels}}":
			return mock.Result{Stdout: `{"sind.realm":"sind"}` + "\n"}
		case joined == "volume inspect sind-ssh-config":
			return mock.Result{Stdout: "[{}]\n"}
		case args[0] == "inspect" && args[1] == "sind-dns":
			return mock.Result{Stdout: inspect("sind-dns", map[string]string{"sind-mesh": "10.0.255.254"})}
		case args[0] == "inspect" && args[1] == "sind-ssh":
			return mock.Result{Stdout: inspect("sind-ssh", map[string]string{"sind-mesh": "10.0.0.2"})}
		case args[0] == "exec" && args[1] == "sind-ssh" && args[2] == "sh":
			return mock.Result{Stdout: "PRIVATE KEY\n\x00"}
		case args[0] == "exec" && args[1] == "sind-ssh" && args[2] == "cat":
			return mock.Result{Stdout: "ssh-ed25519 AAAA-realm-key\n"}
		case args[0] == "cp" && len(args) == 3 && args[1] == "sind-dns:/Corefile" && args[2] == "-":
			return mock.Result{Stdout: testutil.TarArchive("Corefile", corefile)}

		// The cluster's resources are not there, its images are.
		case args[0] == "image" && args[1] == "inspect":
			return mock.Result{Stdout: "sha256:local\n"}
		case args[0] == "network" && args[1] == "inspect":
			return mock.Result{Stderr: testutil.NoSuchNetwork(args[2]), Err: exit1}
		case args[0] == "volume" && args[1] == "inspect":
			return mock.Result{Stderr: testutil.NoSuchVolume(args[2]), Err: exit1}
		case args[0] == "ps":
			return mock.Result{}
		case args[0] == "run" && slices.Contains(args, "/proc/self/mounts"):
			return mock.Result{Stdout: "cgroup /sys/fs/cgroup cgroup2 rw,nsdelegate 0 0\n"}
		case args[0] == "run" && slices.Contains(args, "--rm"):
			return mock.Result{Stdout: "slurm 25.11.0\n"}
		case args[0] == "network" && args[1] == "create":
			return mock.Result{Stdout: "net-id\n"}
		case args[0] == "create", args[0] == "run":
			return mock.Result{Stdout: "cid\n"}
		case slices.Contains([]string{"volume", "network"}, args[0]), slices.Contains([]string{"cp", "rm", "start", "kill"}, args[0]):
			return mock.Result{}

		// The nodes run, and their services are active.
		case args[0] == "inspect":
			return mock.Result{Stdout: inspect(args[1], map[string]string{"sind-dev-net": "10.0.1.1"})}
		case args[0] == "exec" && args[1] == "-i":
			return mock.Result{}
		case args[0] == "exec" && strings.Contains(joined, "is-system-running"):
			return mock.Result{Stdout: "running\n"}
		case args[0] == "exec" && strings.Contains(joined, "/dev/tcp"):
			return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.0\n"}
		case args[0] == "exec" && strings.Contains(joined, "ssh-keyscan"):
			return mock.Result{Stdout: "localhost ssh-ed25519 AAAA-hostkey-" + args[1] + "\n"}
		case args[0] == "exec" && args[2] == "scontrol":
			return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
		case args[0] == "exec" && args[2] == "systemctl" && args[3] == "is-active":
			return mock.Result{Stdout: "active\n"}
		case args[0] == "exec":
			return mock.Result{}
		}
		t.Errorf("unexpected docker call %q", args)
		return mock.Result{Err: fmt.Errorf("unexpected docker call %q", args)}
	}
}

// create cluster reports its work as the spans of the command, which keep
// every promise progresstest.Check holds them to: the targets of the mesh,
// of the nodes and of Slurm are all queued before the first of them runs,
// and every span ends before its parent. The mesh is in place: its parts
// are skipped.
func TestCreateCluster_ReportsItsProgress(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SIND_REALM", "")
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	require.NoError(t, os.WriteFile(path, []byte("kind: Cluster\nnodes: [controller, worker]\n"), 0o644))
	client := docker.NewClient(&mock.Executor{OnCall: createDaemon(t)})
	ctx := withMeshMgr(withClient(context.Background(), client), mesh.NewManager(client, mesh.DefaultRealm))
	ctx, w := progresstest.Watch(ctx, t, progresstest.Classify(progressClass(context.Background())))
	var stdout, stderr bytes.Buffer

	err := executeOn(ctx, &stdout, &stderr, "create", "cluster", "--config", path, "--data", "volume")

	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
	w.Finish()
	assert.Equal(t, `command create cluster: ok
  step mesh registration: ok
  step mesh total=4 [hidden,fold,show-lines]: ok
    target dns,relay [hidden,show-lines]: skipped: running
    target network,volume [hidden,show-lines]: skipped: exists
  step nodes total=2 [fold]: ok
    target controller role=controller: ok
      wait ready: ok
    target worker-0 role=worker: ok
      wait ready: ok
  step preflight [hidden]: ok
  step slurm total=2 [fold]: ok
    target controller role=controller: ok
      wait ready: ok
    target worker-0 role=worker: ok
      wait ready: ok
`, timeouts.ReplaceAllString(withoutCalls(w.Events()), ""))
	// Each node's wait has the whole --wait limit; the waits for Slurm
	// have what is left of it once the nodes are ready, in whole seconds.
	var bounds []time.Duration
	for _, e := range w.Events() {
		if e.Kind == progress.KindWait && e.Type == progress.TypeStart {
			bounds = append(bounds, e.Timeout)
		}
	}
	require.Len(t, bounds, 4)
	assert.Equal(t, []time.Duration{5 * time.Minute, 5 * time.Minute}, bounds[:2])
	for _, b := range bounds[2:] {
		assert.True(t, b > 4*time.Minute && b <= 5*time.Minute && b%time.Second == 0, "%s", b)
	}
}

// timeouts matches the bound of a wait in a progresstest tree.
var timeouts = regexp.MustCompile(` timeout=[0-9hms.]+`)

// When create cluster made the mesh and could not finish it, it removes the
// mesh again as the step "rollback", which fails when the mesh stays, also
// once the command was interrupted, as the rollback runs on; the command's
// error and what it writes are those it has without the step.
func TestCreateCluster_MeshRollbackIsAStep(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SIND_REALM", "")
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	require.NoError(t, os.WriteFile(path, []byte("kind: Cluster\nnodes: [controller, worker]\n"), 0o644))
	interrupted, interrupt := context.WithCancel(context.Background())
	interrupt()
	for _, tc := range []struct {
		name, rollback string
		interrupt      context.Context
	}{
		{"removed", "step rollback: ok", context.Background()},
		{"left", "step rollback: failed (target): removing DNS container: docker daemon unavailable", context.Background()},
		{"left after an interrupt", "step rollback: failed (target): removing DNS container: docker daemon unavailable", interrupted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(watched bool) (string, []progress.Event, error) {
				exit1 := testutil.ExitCode1(t)
				daemon := createDaemon(t)
				m := &mock.Executor{OnCall: func(args []string, stdin string) mock.Result {
					switch joined := strings.Join(args, " "); {
					// There is no mesh, and the new network cannot be
					// inspected.
					case joined == "network inspect sind-mesh --format {{json .Labels}}", joined == "network inspect sind-mesh":
						return mock.Result{Stderr: testutil.NoSuchNetwork("sind-mesh"), Err: exit1}
					case tc.name != "removed" && joined == "rm -f -v sind-dns":
						return mock.Result{Err: errors.New("docker daemon unavailable")}
					}
					return daemon(args, stdin)
				}}
				client := docker.NewClient(m)
				ctx := withMeshMgr(withClient(context.Background(), client), mesh.NewManager(client, mesh.DefaultRealm))
				var w *progresstest.Watcher
				if watched {
					ctx, w = progresstest.Watch(ctx, t, progresstest.Classify(progressClass(tc.interrupt)))
				}
				var stdout, stderr bytes.Buffer
				err := executeOn(ctx, &stdout, &stderr, "create", "cluster", "--config", path, "--data", "volume")
				assert.Empty(t, stdout.String())
				if w == nil {
					return stderr.String(), nil, err
				}
				w.Finish()
				return stderr.String(), w.Events(), err
			}

			plainStderr, _, plainErr := run(false)
			stderr, events, err := run(true)

			require.EqualError(t, err, "setting up mesh: inspecting mesh network: exit status 1: Error response from daemon: network sind-mesh not found")
			assert.Equal(t, plainErr.Error(), err.Error())
			assert.Equal(t, withoutTimestamps(plainStderr), withoutTimestamps(stderr))
			tree := withoutCalls(events)
			assert.True(t, strings.HasSuffix(tree, "\n  "+tc.rollback+"\n"), tree)
		})
	}
}
