// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitStatus returns an *exec.ExitError with the given exit code.
func exitStatus(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}

// setupCall returns the steps, by name, of the node setup that a docker
// call runs (see nodeSetupScript), and the container it runs in.
func setupCall(args []string) (container string, steps map[string]string, ok bool) {
	if len(args) < 5 || args[0] != "exec" || args[2] != "sh" || args[3] != "-c" || !strings.HasPrefix(args[4], "set -e\n") {
		return "", nil, false
	}
	return args[1], setupScriptSteps(args[4]), true
}

// isUsersSetup reports whether a docker call is the setup of container,
// and adds the cluster users there.
func isUsersSetup(args []string, container string) bool {
	name, steps, ok := setupCall(args)
	return ok && name == container && steps["users"] != ""
}

// setupScriptSteps returns the steps of a node setup script by name, each
// with its commands.
func setupScriptSteps(script string) map[string]string {
	steps := map[string]string{}
	var name string
	var lines []string
	flush := func() {
		if name != "" {
			steps[name] = strings.Join(lines, "\n")
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(script, "\n"), "\n") {
		if n, ok := strings.CutPrefix(line, "echo '"+setupStepMarker); ok {
			flush()
			name, lines = strings.TrimSuffix(n, "' >&2"), nil
			continue
		}
		switch line {
		case "set -e", "{", "} >&2":
			continue
		}
		lines = append(lines, line)
	}
	flush()
	return steps
}

// failedSetup is the mock result of a node setup that ran the named steps
// and failed in the last one with exit status code, after the step wrote
// out to stderr.
func failedSetup(t *testing.T, code int, out string, names ...string) mock.Result {
	t.Helper()
	var b strings.Builder
	for _, n := range names {
		b.WriteString(setupStepMarker + n + "\n")
	}
	b.WriteString(out)
	return mock.Result{Stderr: b.String(), Err: exitStatus(t, code)}
}

func TestNodeSetupScript(t *testing.T) {
	script := nodeSetupScript([]setupStep{
		{name: "users", script: "groupadd --gid 3000 hpc\n"},
		{name: "ssh", script: "ssh-keyscan localhost", output: true},
	})

	assert.Equal(t, `set -e
echo 'sind-setup-step: users' >&2
{
groupadd --gid 3000 hpc
} >&2
echo 'sind-setup-step: ssh' >&2
ssh-keyscan localhost
`, script)
	assert.Equal(t, map[string]string{"users": "groupadd --gid 3000 hpc", "ssh": "ssh-keyscan localhost"}, setupScriptSteps(script))
}

// shells are the POSIX shells on this machine to run setup scripts with:
// sh, bash in POSIX mode, as the sind-node image's /bin/sh is, and dash.
func shells(t *testing.T) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	for name, cmd := range map[string][]string{"sh": {"sh"}, "bash": {"bash", "--posix"}, "dash": {"dash"}} {
		if _, err := exec.LookPath(cmd[0]); err == nil {
			found[name] = cmd
		}
	}
	require.NotEmpty(t, found)
	return found
}

// runScript runs a setup script with a shell, with args as its positional
// parameters.
func runScript(t *testing.T, shell []string, script string, args ...string) (string, string, error) {
	t.Helper()
	var e cmdexec.OSExecutor
	return e.Run(t.Context(), shell[0], append(append(shell[1:], "-c", script, "sh"), args...)...)
}

func TestNodeSetupScript_Shell(t *testing.T) {
	// Only the output step writes to stdout; the others' stdout goes to
	// stderr, after their markers.
	steps := []setupStep{
		{name: "a", script: "echo a-out\necho a-err >&2"},
		{name: "b", script: `echo "out $1"`, output: true},
	}
	for name, shell := range shells(t) {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, err := runScript(t, shell, nodeSetupScript(steps), "arg1")

			require.NoError(t, err)
			assert.Equal(t, "out arg1\n", stdout)
			assert.Equal(t, "sind-setup-step: a\na-out\na-err\nsind-setup-step: b\n", stderr)
		})
	}
}

func TestNodeSetupScript_ShellStopsAtFailure(t *testing.T) {
	// The script stops at the first command that fails, also inside the
	// redirected group and at the end of an || list, with that command's
	// exit status; the marker before it names the step.
	for name, script := range map[string]string{
		"command":    "echo c-err >&2\nsh -c 'exit 3'\necho after >&2",
		"or list":    "false || sh -c 'echo c-err >&2; exit 3'\necho after >&2",
		"output too": "echo c-err >&2\nsh -c 'exit 3'\necho after",
	} {
		steps := []setupStep{
			{name: "a", script: "true"},
			{name: "c", script: script, output: name == "output too"},
			{name: "d", script: "echo d-ran >&2"},
		}
		for shellName, shell := range shells(t) {
			t.Run(name+"/"+shellName, func(t *testing.T) {
				stdout, stderr, err := runScript(t, shell, nodeSetupScript(steps))

				var exitErr *cmdexec.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, 3, exitErr.ExitCode())
				assert.Empty(t, stdout)
				step, out, ok := failedStep(steps, stderr)
				require.True(t, ok)
				assert.Equal(t, "c", step.name)
				assert.Equal(t, "c-err\n", out)
			})
		}
	}
}

func TestFailedStep(t *testing.T) {
	steps := []setupStep{{name: "users"}, {name: "ssh"}}

	_, _, ok := failedStep(steps, "Error response from daemon: container x is not running\n")
	assert.False(t, ok, "no marker: docker failed itself")

	step, out, ok := failedStep(steps, "sind-setup-step: users\nwarning\nsind-setup-step: ssh\nmkdir: denied\n")
	require.True(t, ok)
	assert.Equal(t, "ssh", step.name)
	assert.Equal(t, "mkdir: denied\n", out, "only the failed step's output")

	// A marker line of no step is output.
	step, out, ok = failedStep(steps, "sind-setup-step: users\nsind-setup-step: other\n")
	require.True(t, ok)
	assert.Equal(t, "users", step.name)
	assert.Equal(t, "sind-setup-step: other\n", out)
}

// stepNames returns the names of the steps, in order.
func stepNames(steps []setupStep) []string {
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.name
	}
	return names
}

func TestNodeSetupSteps(t *testing.T) {
	nss := RunConfig{NSSSlurm: true, Image: "img:1", Identity: config.IdentityNSSSlurm}
	for _, tt := range []struct {
		name string
		nc   RunConfig
		want []string
	}{
		{"local without users", RunConfig{}, []string{"ssh"}},
		{"users", RunConfig{AddUsers: true, Users: testUsers}, []string{"users", "ssh"}},
		{"users elsewhere", RunConfig{Users: testUsers}, []string{"ssh"}},
		{"no users to add", RunConfig{AddUsers: true}, []string{"ssh"}},
		{"nss_slurm", nss, []string{"nss_slurm", "nsswitch", "ssh"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stepNames(nodeSetupSteps(tt.nc)))
		})
	}

	scripts := map[string]string{}
	for _, s := range append(nodeSetupSteps(nss), nodeSetupSteps(RunConfig{AddUsers: true, Users: testUsers})...) {
		scripts[s.name] = s.script
	}
	assert.Equal(t, map[string]string{
		"nss_slurm": nssSlurmCheck,
		"nsswitch":  nssSlurmSwitch,
		"users":     testUsersScript,
		"ssh":       ssh.InjectKeyScript,
	}, scripts)
}

func TestSetupNode(t *testing.T) {
	var m mock.Executor
	m.AddResult("# localhost:22 SSH-2.0-OpenSSH_9.9\nlocalhost ssh-ed25519 AAAA-hostkey\n", "", nil)
	nc := RunConfig{AddUsers: true, Users: testUsers}

	key, err := setupNode(t.Context(), docker.NewClient(&m), "sind-dev-controller", nc, "ssh-ed25519 AAAA-test-key\n")

	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAA-hostkey", key)
	// One docker exec, with the key as an argument of the shell.
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "sind-dev-controller", "sh", "-c", nodeSetupScript(nodeSetupSteps(nc)), "sh", "ssh-ed25519 AAAA-test-key"}, m.Calls[0].Args)
}

func TestSetupNode_StepFails(t *testing.T) {
	nss := RunConfig{NSSSlurm: true, Image: "old:1", Identity: config.IdentityClientIDs}
	users := RunConfig{AddUsers: true, Users: testUsers}
	for _, tt := range []struct {
		name   string
		nc     RunConfig
		result func(t *testing.T) mock.Result
		want   string
	}{
		{"nss_slurm missing", nss, func(t *testing.T) mock.Result { return failedSetup(t, 1, "", "nss_slurm") },
			"image old:1 has no libnss_slurm.so.2, which identity clientIds needs on managed workers; use a current sind-node image (--pull refreshes a cached one), or build yours with contribs/nss_slurm: exit status 1"},
		{"nsswitch", nss, func(t *testing.T) mock.Result {
			return failedSetup(t, 2, "sed: can't read /etc/nsswitch.conf: No such file or directory\n", "nss_slurm", "nsswitch")
		}, "switching passwd and group lookups to nss_slurm: exit status 2: sed: can't read /etc/nsswitch.conf: No such file or directory"},
		{"users", users, func(t *testing.T) mock.Result {
			return failedSetup(t, 9, "groupadd: group 'alice' already exists\n", "users")
		}, "adding users: exit status 9: groupadd: group 'alice' already exists"},
		{"ssh", users, func(t *testing.T) mock.Result {
			return failedSetup(t, 1, "mkdir: cannot create directory '/root/.ssh': Read-only file system\n", "users", "ssh")
		}, "setting up SSH: injecting SSH key and scanning host key: exit status 1: mkdir: cannot create directory '/root/.ssh': Read-only file system"},
		{"docker exec", users, func(t *testing.T) mock.Result {
			return mock.Result{Stderr: "Error response from daemon: container sind-dev-worker-0 is not running\n", Err: exitStatus(t, 1)}
		}, "running the node setup: exit status 1: Error response from daemon: container sind-dev-worker-0 is not running"},
		{"docker", users, func(*testing.T) mock.Result { return mock.Result{Err: context.DeadlineExceeded} },
			"running the node setup: context deadline exceeded"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var m mock.Executor
			r := tt.result(t)
			m.AddResult(r.Stdout, r.Stderr, r.Err)

			_, err := setupNode(t.Context(), docker.NewClient(&m), "sind-dev-worker-0", tt.nc, "ssh-ed25519 AAAA-test-key\n")

			require.EqualError(t, err, tt.want)
			assert.Len(t, m.Calls, 1)
		})
	}
}

func TestSetupNode_NoHostKey(t *testing.T) {
	var m mock.Executor
	m.AddResult("# localhost:22 SSH-2.0-OpenSSH_9.9\n", "", nil)

	_, err := setupNode(t.Context(), docker.NewClient(&m), "sind-dev-controller", RunConfig{}, "ssh-ed25519 AAAA-test-key\n")

	require.EqualError(t, err, "setting up SSH: no ed25519 host key found")
}

func TestSetupNodes_CallsPerNode(t *testing.T) {
	// From docker create to the host key, every node takes the same docker
	// calls, whatever its role and identity mode: create, start, the base
	// probes once each (inspect, then three execs), the inspect of its
	// addresses, and one setup exec.
	var m mock.Executor
	m.OnCall = func(args []string, _ string) mock.Result {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "create":
			return mock.Result{Stdout: "cid\n"}
		case args[0] == "inspect":
			return mock.Result{Stdout: inspectJSON(t, args[1], "running", map[docker.NetworkName]string{"sind-dev-net": "10.0.1.1"})}
		case strings.Contains(joined, "is-system-running"):
			return mock.Result{Stdout: "running\n"}
		case strings.Contains(joined, "/dev/tcp"):
			return mock.Result{Stdout: "SSH-2.0-OpenSSH_9.9\n"}
		case strings.Contains(joined, "is-active"):
			return mock.Result{Stdout: "active\n"}
		case strings.Contains(joined, "ssh-keyscan"):
			return mock.Result{Stdout: "localhost ssh-ed25519 AAAA-" + args[1] + "\n"}
		}
		return mock.Result{}
	}
	client := docker.NewClient(&m)
	base := RunConfig{Realm: mesh.DefaultRealm, ClusterName: "dev", Users: testUsers}
	controller, local, nss := base, base, base
	controller.ShortName, controller.Role, controller.AddUsers = "controller", config.RoleController, true
	local.ShortName, local.Role, local.AddUsers = "worker-0", config.RoleWorker, true
	nss.ShortName, nss.Role, nss.NSSSlurm, nss.Identity = "worker-1", config.RoleWorker, true, config.IdentityNSSSlurm

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	results, err := setupNodes(ctx, client, mesh.NewManager(client, mesh.DefaultRealm), mesh.DefaultRealm, "dev", "ssh-ed25519 AAAA-test-key\n",
		[]RunConfig{controller, local, nss}, &readiness{interval: time.Millisecond}, nil)
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAA-sind-dev-worker-1", results[2].hostKey)

	calls := map[string]map[string]int{}
	setups := map[string]int{}
	for _, c := range m.Calls {
		container := c.Args[1]
		if c.Args[0] == "create" {
			container = c.Args[2] // --name NAME
		}
		if calls[container] == nil {
			calls[container] = map[string]int{}
		}
		calls[container][c.Args[0]]++
		if name, steps, ok := setupCall(c.Args); ok {
			setups[name] = len(steps)
		}
	}
	want := map[string]int{"create": 1, "start": 1, "inspect": 2, "exec": 4}
	for _, name := range []string{"sind-dev-controller", "sind-dev-worker-0", "sind-dev-worker-1"} {
		assert.Equal(t, want, calls[name], name)
	}
	// The setup exec runs every step of the node.
	assert.Equal(t, map[string]int{"sind-dev-controller": 2, "sind-dev-worker-0": 2, "sind-dev-worker-1": 3}, setups)
	assert.Len(t, m.Calls, 3*8)
}
