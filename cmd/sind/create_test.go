// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noStdin returns an input without data: /dev/null is a character device,
// like a terminal.
func noStdin(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(os.DevNull)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestLoadConfig_FromReader(t *testing.T) {
	// A reader set with cmd.SetIn is read as piped input.
	cfg, _, err := loadConfig(strings.NewReader("kind: Cluster\nname: from-reader\n"), io.Discard, "")
	require.NoError(t, err)
	assert.Equal(t, "from-reader", cfg.Name)
}

func TestLoadConfig_FromCommandInput(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetIn(strings.NewReader("kind: Cluster\nname: from-cmd\n"))
	cfg, _, err := loadConfig(cmd.InOrStdin(), io.Discard, "")
	require.NoError(t, err)
	assert.Equal(t, "from-cmd", cfg.Name)
}

func TestStdinHasData_ClosedFile(t *testing.T) {
	f, err := os.Open(os.DevNull)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.False(t, stdinHasData(f))
}

func TestLoadConfig_Default(t *testing.T) {
	cfg, source, err := loadConfig(noStdin(t), io.Discard, "")
	require.NoError(t, err)
	assert.Empty(t, source)
	assert.Equal(t, "Cluster", cfg.Kind)
	assert.Equal(t, "default", cfg.Name)
}

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("kind: Cluster\nname: test\n")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, source, err := loadConfig(noStdin(t), io.Discard, path)
	require.NoError(t, err)
	assert.Equal(t, "test", cfg.Name)
	assert.Equal(t, path, source)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, _, err := loadConfig(noStdin(t), io.Discard, "/nonexistent/config.yaml")
	require.EqualError(t, err, "config /nonexistent/config.yaml: no such file or directory")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("not: valid: yaml: ["), 0o644))

	_, _, err := loadConfig(noStdin(t), io.Discard, path)
	require.EqualError(t, err, "config "+path+": line 1: mapping values are not allowed in this context")
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	// No content, blank lines, a comment or a null document read the same.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	for _, data := range []string{"", "\n\n", "# nothing\n", "---\n", "null\n"} {
		require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
		_, _, err := loadConfig(noStdin(t), io.Discard, path)
		require.EqualError(t, err, "config "+path+": empty configuration", "%q", data)
	}
}

func TestLoadConfig_FromStdin(t *testing.T) {
	// A pipe containing YAML config, as `sind create cluster < file` gets.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString("kind: Cluster\nname: from-stdin\n")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	cfg, source, err := loadConfig(r, io.Discard, "")
	require.NoError(t, err)
	assert.Equal(t, "from-stdin", cfg.Name)
	assert.Equal(t, "<stdin>", source)
}

// pipeWith returns the read end of a pipe holding data, as a shell gives
// sind for `cmd | sind create cluster`.
func pipeWith(t *testing.T, data string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	_, err = w.WriteString(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return r
}

func TestLoadConfig_ImplicitStdinWarns(t *testing.T) {
	var stderr bytes.Buffer
	cfg, _, err := loadConfig(pipeWith(t, "kind: Cluster\nname: piped\n"), &stderr, "")
	require.NoError(t, err)
	assert.Equal(t, "piped", cfg.Name)
	assert.Equal(t, stdinDeprecation+"\n", stderr.String())
	assert.True(t, strings.HasPrefix(stderr.String(), "Warning: "))
}

func TestLoadConfig_ImplicitEmptyStdinIsDefault(t *testing.T) {
	// An empty pipe, as a CI runner or `true | sind create cluster` gives,
	// creates the default cluster instead of failing.
	for _, input := range []string{"", " \n\t\n"} {
		var stderr bytes.Buffer
		cfg, _, err := loadConfig(pipeWith(t, input), &stderr, "")
		require.NoError(t, err)
		assert.Equal(t, "default", cfg.Name)
		assert.Equal(t, stdinDeprecation+"\n", stderr.String())
	}
}

func TestLoadConfig_TerminalStdinDoesNotWarn(t *testing.T) {
	var stderr bytes.Buffer
	cfg, _, err := loadConfig(noStdin(t), &stderr, "")
	require.NoError(t, err)
	assert.Equal(t, "default", cfg.Name)
	assert.Empty(t, stderr.String())
}

func TestLoadConfig_ExplicitStdin(t *testing.T) {
	var stderr bytes.Buffer
	cfg, source, err := loadConfig(pipeWith(t, "kind: Cluster\nname: explicit\n"), &stderr, "-")
	require.NoError(t, err)
	assert.Equal(t, "<stdin>", source)
	assert.Equal(t, "explicit", cfg.Name)
	assert.Empty(t, stderr.String())

	// --config - reads stdin whatever it is.
	cfg, _, err = loadConfig(strings.NewReader("kind: Cluster\nname: reader\n"), &stderr, "-")
	require.NoError(t, err)
	assert.Equal(t, "reader", cfg.Name)
}

func TestLoadConfig_ExplicitEmptyStdin(t *testing.T) {
	for _, input := range []string{"", "\n  \n", "# nothing\n"} {
		_, _, err := loadConfig(pipeWith(t, input), io.Discard, "-")
		require.EqualError(t, err, "config <stdin>: empty configuration")
	}
}

// failingReader fails every read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestLoadConfig_StdinReadError(t *testing.T) {
	for _, path := range []string{"", "-"} {
		_, _, err := loadConfig(failingReader{}, io.Discard, path)
		require.EqualError(t, err, "config <stdin>: read failed")
	}
}

func TestLoadConfig_StdinInvalidYAML(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString("not: valid: yaml: [")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	_, _, err = loadConfig(r, io.Discard, "")
	require.EqualError(t, err, "config <stdin>: line 1: mapping values are not allowed in this context")
}

func TestCreateCluster_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	createCmd, _, err := cmd.Find([]string{"create", "cluster"})
	require.NoError(t, err)
	assert.Equal(t, "cluster [NAME]", createCmd.Use)

	// Check flags exist with correct defaults
	assert.Nil(t, createCmd.Flags().Lookup("name"), "--name flag should not exist")
	assert.NotNil(t, createCmd.Flags().Lookup("config"))
	assert.NotNil(t, createCmd.Flags().Lookup("data"))
	assert.Equal(t, ".", createCmd.Flags().Lookup("data").DefValue)
}

func TestCreate_WaitFlag(t *testing.T) {
	for _, sub := range []string{"cluster", "worker"} {
		t.Run(sub, func(t *testing.T) {
			c, _, err := NewRootCommand().Find([]string{"create", sub})
			require.NoError(t, err)
			flag := c.Flags().Lookup("wait")
			require.NotNil(t, flag)
			assert.Equal(t, "0s", flag.DefValue, "the help shows no default of its own")
			assert.Contains(t, flag.Usage, "(default: SIND_WAIT, else 5m0s)")

			// A negative limit is a usage error, reported before sind acts.
			_, _, err = executeCommand("create", sub, "--wait", "-1s")
			require.EqualError(t, err, "--wait must not be negative, got -1s")
			assert.True(t, cobratree.IsUsage(err))
		})
	}
}

// TestWaitAndPullFlags covers SIND_WAIT and SIND_PULL, the --wait and the
// --pull of create cluster and create worker when the command line has
// none: a value they cannot use is an error that names the variable, and
// no usage error, and sind checks them only then.
func TestWaitAndPullFlags(t *testing.T) {
	for _, sub := range []string{"cluster", "worker"} {
		for _, tc := range []struct {
			wait, pull string
			args       []string
			wantWait   time.Duration
			wantPull   bool
			wantErr    string
		}{
			{"", "", nil, 5 * time.Minute, false, ""},
			{"10m", "true", nil, 10 * time.Minute, true, ""},
			{"0", "0", nil, 0, false, ""},
			{"10m", "true", []string{"--wait", "1m", "--pull=false"}, time.Minute, false, ""},
			{"5", "yes", []string{"--wait", "1m", "--pull"}, time.Minute, true, ""},
			{"5", "", nil, 0, false, `invalid SIND_WAIT value "5": want a duration with a unit, such as 90s, 5m or 1h30m`},
			{"-1m", "", nil, 0, false, "SIND_WAIT must not be negative, got -1m"},
			{"", "yes", nil, 0, false, `invalid SIND_PULL value "yes": want true or false`},
		} {
			t.Setenv("SIND_WAIT", tc.wait)
			t.Setenv("SIND_PULL", tc.pull)
			c, _, err := NewRootCommand().Find([]string{"create", sub})
			require.NoError(t, err)
			require.NoError(t, c.ParseFlags(tc.args))
			name := fmt.Sprintf("create %s %v, SIND_WAIT=%s SIND_PULL=%s", sub, tc.args, tc.wait, tc.pull)

			wait, waitErr := waitFlag(c)
			pull, pullErr := pullFlag(c)

			if err := errors.Join(waitErr, pullErr); tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr, name)
				assert.False(t, cobratree.IsUsage(err), name)
				continue
			}
			require.NoError(t, errors.Join(waitErr, pullErr), name)
			assert.Equal(t, tc.wantWait, wait, name)
			assert.Equal(t, tc.wantPull, pull, name)
		}
	}
}

// TestCreate_InvalidWaitAndPullFromEnv checks that the create commands
// refuse an invalid SIND_WAIT or SIND_PULL before they call docker.
func TestCreate_InvalidWaitAndPullFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		args        []string
	}{
		{"SIND_WAIT", "5", []string{"create", "cluster"}},
		{"SIND_WAIT", "5", []string{"create", "worker"}},
		{"SIND_PULL", "yes", []string{"create", "cluster"}},
		{"SIND_PULL", "yes", []string{"create", "worker", "--image", "sind-node:25.11"}},
	} {
		t.Setenv(tc.name, tc.value)
		var m mock.Executor
		_, _, err := executeWithMock(&m, tc.args...)
		require.ErrorContains(t, err, "invalid "+tc.name+" value", "%v", tc.args)
		assert.Empty(t, m.Calls, "%v", tc.args)
		t.Setenv(tc.name, "")
	}
}

func TestApplyDataFlag_HostPath(t *testing.T) {
	cfg, _, err := loadConfig(noStdin(t), io.Discard, "")
	require.NoError(t, err)

	require.NoError(t, applyDataFlag(cfg, "/tmp/my-project"))

	assert.Equal(t, config.StorageHostPath, cfg.Storage.DataStorage.Type)
	assert.Equal(t, "/tmp/my-project", cfg.Storage.DataStorage.HostPath)
}

func TestApplyDataFlag_RelativePath(t *testing.T) {
	cfg, _, err := loadConfig(noStdin(t), io.Discard, "")
	require.NoError(t, err)

	require.NoError(t, applyDataFlag(cfg, "."))

	assert.Equal(t, config.StorageHostPath, cfg.Storage.DataStorage.Type)
	assert.True(t, filepath.IsAbs(cfg.Storage.DataStorage.HostPath), "path should be absolute")
}

func TestApplyDataFlag_Empty(t *testing.T) {
	cfg, _, err := loadConfig(noStdin(t), io.Discard, "")
	require.NoError(t, err)

	err = applyDataFlag(cfg, "")

	require.Error(t, err)
	assert.True(t, cobratree.IsUsage(err))
	assert.Empty(t, cfg.Storage.DataStorage.HostPath)
}

func TestApplyDataFlag_Volume(t *testing.T) {
	cfg, _, err := loadConfig(noStdin(t), io.Discard, "")
	require.NoError(t, err)

	require.NoError(t, applyDataFlag(cfg, "volume"))

	assert.Empty(t, cfg.Storage.DataStorage.Type)
	assert.Empty(t, cfg.Storage.DataStorage.HostPath)
}

func TestApplyDataStorage(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		name     string
		ds       config.DataStorage
		dataFlag string
		want     config.DataStorage
	}{
		{
			name:     "no storage uses --data",
			dataFlag: "/srv/project",
			want:     config.DataStorage{Type: config.StorageHostPath, HostPath: "/srv/project"},
		},
		{
			name:     "relative hostPath becomes absolute",
			ds:       config.DataStorage{Type: config.StorageHostPath, HostPath: "./data"},
			dataFlag: "/ignored",
			want:     config.DataStorage{Type: config.StorageHostPath, HostPath: filepath.Join(wd, "data")},
		},
		{
			name:     "hostPath without type",
			ds:       config.DataStorage{HostPath: "data"},
			dataFlag: "/ignored",
			want:     config.DataStorage{HostPath: filepath.Join(wd, "data")},
		},
		{
			name:     "volume type ignores --data",
			ds:       config.DataStorage{Type: config.StorageVolume, MountPath: "/scratch"},
			dataFlag: "/ignored",
			want:     config.DataStorage{Type: config.StorageVolume, MountPath: "/scratch"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Cluster{Storage: config.Storage{DataStorage: tt.ds}}
			require.NoError(t, applyDataStorage(cfg, tt.dataFlag))
			assert.Equal(t, tt.want, cfg.Storage.DataStorage)
		})
	}
}

func TestCreateCluster_WarnsAboutRootDataPath(t *testing.T) {
	// An invalid config stops create after the warning, before any docker call.
	cfgPath := filepath.Join(t.TempDir(), "cluster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("kind: Cluster\nnodes: [worker]\n"), 0o644))
	var m mock.Executor

	_, stderr, err := executeWithMock(&m, "create", "cluster", "--config", cfgPath, "--data", "/")

	require.Error(t, err)
	assert.Contains(t, stderr, "Warning: every node mounts the root directory / read-write as its data")
	assert.Empty(t, m.Calls)

	_, stderr, _ = executeWithMock(&m, "create", "cluster", "--config", cfgPath, "--data", "volume")
	assert.NotContains(t, stderr, "Warning")
}

func TestCreateCluster_WarnsAboutUnenforcedLimits(t *testing.T) {
	// The warning comes right after validation, before sind touches
	// docker.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("SIND_REALM", "")
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`kind: Cluster
nodes: [controller, db, worker]
accounts:
  - name: physics
    limits:
      MaxJobs: 1
`), 0o644))
	m := &mock.Executor{OnCall: func([]string, string) mock.Result {
		return mock.Result{Err: errors.New("no docker")}
	}}

	_, stderr, err := executeWithMock(m, "create", "cluster", "--config", path)

	require.Error(t, err)
	assert.Contains(t, stderr, "Warning: Slurm does not enforce the accounts' limits (physics: MaxJobs): slurm.main does not set AccountingStorageEnforce; add AccountingStorageEnforce=associations,limits to it\n")
}

func TestCreateCluster_MonitoringFlag(t *testing.T) {
	// --monitoring adds to the config's monitoring before validation:
	// an unmanaged config fails like monitoring: true in the file, as a
	// config error (exit 1), before the realm is resolved and before any
	// docker call. --monitoring=false leaves the config's setting alone.
	t.Setenv("SIND_REALM", "BAD")
	dir := t.TempDir()
	unmanaged := filepath.Join(dir, "unmanaged.yaml")
	require.NoError(t, os.WriteFile(unmanaged, []byte("kind: Cluster\nnodes:\n  - role: controller\n    managed: false\n  - role: worker\n"), 0o644))
	monitored := filepath.Join(dir, "monitored.yaml")
	require.NoError(t, os.WriteFile(monitored, []byte("kind: Cluster\nmonitoring: true\nnodes:\n  - role: controller\n    managed: false\n  - role: worker\n"), 0o644))

	for _, args := range [][]string{
		{"--monitoring", "--config", unmanaged},
		{"--monitoring=false", "--config", monitored},
	} {
		var m mock.Executor
		_, _, err := executeWithMock(&m, append([]string{"create", "cluster"}, args...)...)
		require.EqualError(t, err, "config "+args[len(args)-1]+": monitoring requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster", "%v", args)
		assert.False(t, cobratree.IsUsage(err))
		assert.Empty(t, m.Calls)
	}

	var m mock.Executor
	_, _, err := executeWithMock(&m, "create", "cluster", "--monitoring=false", "--config", unmanaged)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "monitoring")
}

func TestLoadConfig_PreservesName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("kind: Cluster\nname: from-file\n")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	cfg, _, err := loadConfig(noStdin(t), io.Discard, path)
	require.NoError(t, err)
	assert.Equal(t, "from-file", cfg.Name)
}

// --- Integration ---

// cleanupRealm deletes, when the test ends, the clusters of realm and its
// mesh: whatever the test left behind, as far as it got.
func cleanupRealm(t *testing.T, c *docker.Client, realm string) {
	t.Cleanup(func() {
		bg := context.Background()
		meshMgr := mesh.NewManager(c, realm)
		names, _ := cluster.DiscoverClusterNames(bg, c, realm)
		for _, name := range names {
			_ = cluster.Delete(bg, c, meshMgr, name)
		}
		_ = meshMgr.CleanupMesh(bg)
	})
}

// TestClusterLifecycle exercises the full user workflow via CLI commands:
// create cluster → get/status → power ops → add worker → delete worker → delete cluster.
func TestClusterLifecycle(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	checkPrerequisites(t, c)
	image := testutil.Image()
	dataDir := t.TempDir()

	realm := "it-e2e-" + testID
	cluster := "e2e-" + testID
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	cleanupRealm(t, c, realm)

	// Write a config file.
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "cluster.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("kind: Cluster\ndefaults:\n  image: "+image+"\n"), 0o644))

	// --- create cluster ---
	_, stderr, err := executeWithRealmCtx(ctx, realm, "create", "cluster", cluster, "--config", cfgPath, "--data", dataDir)
	require.NoError(t, err, "create cluster failed: stderr=%q", stderr)

	// --- get clusters ---
	var stdout string
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "running")

	// --- get nodes ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "controller."+cluster)
	assert.Contains(t, stdout, "worker-0."+cluster)

	// --- get networks ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "networks")
	require.NoError(t, err)
	assert.Contains(t, stdout, realm+"-"+cluster+"-net")
	assert.Contains(t, stdout, realm+"-mesh")

	// --- get volumes ---
	// Data volume is not created when using host-path bind mount (default).
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "volumes")
	require.NoError(t, err)
	assert.Contains(t, stdout, realm+"-"+cluster+"-config")
	assert.Contains(t, stdout, realm+"-"+cluster+"-munge")

	// --- get auth-key ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "auth-key", cluster)
	require.NoError(t, err)
	assert.NotEmpty(t, stdout)
	assert.NotContains(t, stdout, "Error")

	// --- get cluster ---
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "cluster", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "CLUSTER")
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "NODES")
	assert.Contains(t, stdout, "controller")
	assert.Contains(t, stdout, "worker-0")
	assert.Contains(t, stdout, "NETWORKS")
	assert.Contains(t, stdout, "MESH SERVICES")
	assert.Contains(t, stdout, "MOUNTS")

	// The mesh DNS and the SSH relay run.
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "cluster", cluster, "-o", "json")
	require.NoError(t, err)
	assert.Contains(t, stdout, `"dns_ok": true`)
	assert.Contains(t, stdout, `"ssh_ok": true`)

	// --- exec ---
	stdout, stderr, err = executeWithRealmCtx(ctx, realm, "exec", cluster, "--", "hostname")
	require.NoError(t, err, "exec failed: stdout=%q stderr=%q", stdout, stderr)
	assert.Contains(t, stdout, "controller")

	// exec with default cluster
	_, _, err = executeWithRealmCtx(ctx, realm, "exec", "--", "echo", "hello")
	if err != nil {
		// Only fails if no "default" cluster exists — expected in isolation.
		assert.Contains(t, err.Error(), "default")
	}

	// exec missing separator
	_, _, err = executeWithRealmCtx(ctx, realm, "exec", "hostname")
	assert.Error(t, err)

	// exec missing command
	_, _, err = executeWithRealmCtx(ctx, realm, "exec", "--")
	assert.Error(t, err)

	// --- power shutdown ---
	node := "worker-0." + cluster
	_, _, err = executeWithRealmCtx(ctx, realm, "power", "shutdown", node)
	require.NoError(t, err)
	info, err := c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	require.NoError(t, err)
	assert.Equal(t, docker.StateExited, info.Status)

	// --- power on ---
	_, _, err = executeWithRealmCtx(ctx, realm, "power", "on", node)
	require.NoError(t, err)
	info, err = c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, info.Status)

	// --- power freeze / unfreeze ---
	_, _, err = executeWithRealmCtx(ctx, realm, "power", "freeze", node)
	require.NoError(t, err)
	info, _ = c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	assert.Equal(t, docker.StatePaused, info.Status)

	_, _, err = executeWithRealmCtx(ctx, realm, "power", "unfreeze", node)
	require.NoError(t, err)
	info, _ = c.InspectContainer(ctx, docker.ContainerName(realm+"-"+cluster+"-worker-0"))
	assert.Equal(t, docker.StateRunning, info.Status)

	// --- create worker ---
	_, stderr, err = executeWithRealmCtx(ctx, realm, "create", "worker", cluster, "--count", "1")
	require.NoError(t, err, "create worker failed: stderr=%q", stderr)

	// Verify new node appears.
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "worker-1."+cluster)

	// --- delete worker ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "worker", "worker-1."+cluster)
	require.NoError(t, err)

	// Verify node is gone.
	stdout, _, err = executeWithRealmCtx(ctx, realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.NotContains(t, stdout, "worker-1")

	// --- delete cluster ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", cluster)
	require.NoError(t, err)

	// Verify everything is gone.
	exists, err := c.ContainerExists(ctx, docker.ContainerName(realm+"-"+cluster+"-controller"))
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = c.NetworkExists(ctx, docker.NetworkName(realm+"-"+cluster+"-net"))
	require.NoError(t, err)
	assert.False(t, exists)

	// --- delete nonexistent cluster (idempotent) ---
	_, _, err = executeWithRealmCtx(ctx, realm, "delete", "cluster", "nonexistent-"+testID)
	require.NoError(t, err, "deleting nonexistent cluster should not error")
}
