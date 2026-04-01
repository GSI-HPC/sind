// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerStateLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	name := itContainerName("state")
	n := string(name)

	if !rec.IsIntegration() {
		rec.AddResult("abc123\n", "", nil)                                        // create
		rec.AddResult("[{}]\n", "", nil)                                          // exists → true
		rec.AddResult(n+"\n", "", nil)                                            // start
		rec.AddResult(inspectRunning(n), "", nil)                                 // inspect → running
		rec.AddResult(n+"\n", "", nil)                                            // stop
		rec.AddResult(inspectExited(n), "", nil)                                  // inspect → exited
		rec.AddResult(n+"\n", "", nil)                                            // start again
		rec.AddResult(n+"\n", "", nil)                                            // pause
		rec.AddResult(inspectPaused(n), "", nil)                                  // inspect → paused
		rec.AddResult(n+"\n", "", nil)                                            // unpause
		rec.AddResult(inspectRunning(n), "", nil)                                 // inspect → running
		rec.AddResult(n+"\n", "", nil)                                            // kill
		rec.AddResult(n+"\n", "", nil)                                            // remove
		rec.AddResult("", "Error\n", &exec.ExitError{ProcessState: exitCode1(t)}) // exists → false
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_ = c.KillContainer(cleanupCtx, name)
		_ = c.RemoveContainer(cleanupCtx, name)
	})

	// Create.
	_, err := c.CreateContainer(ctx, "--name", string(name), "busybox:latest", "sleep", "60")
	require.NoError(t, err)

	exists, err := c.ContainerExists(ctx, name)
	require.NoError(t, err)
	assert.True(t, exists)

	// Start → running.
	err = c.StartContainer(ctx, name)
	require.NoError(t, err)

	info, err := c.InspectContainer(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	// Stop → exited.
	err = c.StopContainer(ctx, name)
	require.NoError(t, err)

	info, err = c.InspectContainer(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "exited", info.Status)

	// Start → Pause → paused.
	err = c.StartContainer(ctx, name)
	require.NoError(t, err)

	err = c.PauseContainer(ctx, name)
	require.NoError(t, err)

	info, err = c.InspectContainer(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "paused", info.Status)

	// Unpause → running.
	err = c.UnpauseContainer(ctx, name)
	require.NoError(t, err)

	info, err = c.InspectContainer(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	// Kill + Remove → gone.
	err = c.KillContainer(ctx, name)
	require.NoError(t, err)

	err = c.RemoveContainer(ctx, name)
	require.NoError(t, err)

	exists, err = c.ContainerExists(ctx, name)
	require.NoError(t, err)
	assert.False(t, exists)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestContainerExecAndFiles(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	name := itContainerName("files")
	n := string(name)

	if !rec.IsIntegration() {
		rec.AddResult("abc123\n", "", nil)               // run
		rec.AddResult("hello\n", "", nil)                // exec echo
		rec.AddResult("", "", nil)                       // write
		rec.AddResult("", "", nil)                       // append
		rec.AddResult("line1\nline2\n", "", nil)         // read
		rec.AddResult("", "", nil)                       // copy to
		rec.AddResult(copyFromTar("content-a"), "", nil) // copy from
		rec.AddResult(n+"\n", "", nil)                   // kill (cleanup)
		rec.AddResult(n+"\n", "", nil)                   // rm (cleanup)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_ = c.KillContainer(cleanupCtx, name)
		_ = c.RemoveContainer(cleanupCtx, name)
	})

	_, err := c.RunContainer(ctx, "--name", string(name), "busybox:latest", "sleep", "60")
	require.NoError(t, err)

	// Exec.
	stdout, err := c.Exec(ctx, name, "echo", "hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", stdout)

	// Write + Append + Read.
	err = c.WriteFile(ctx, name, "/tmp/test.txt", "line1\n")
	require.NoError(t, err)

	err = c.AppendFile(ctx, name, "/tmp/test.txt", "line2\n")
	require.NoError(t, err)

	content, err := c.ReadFile(ctx, name, "/tmp/test.txt")
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", content)

	// CopyTo + CopyFrom.
	err = c.CopyToContainer(ctx, name, "/tmp", map[string][]byte{
		"a.txt": []byte("content-a"),
	})
	require.NoError(t, err)

	data, err := c.CopyFromContainer(ctx, name, "/tmp/a.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("content-a"), data)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestContainerLabelsAndList(t *testing.T) {
	t.Parallel()
	c, rec := newTestClient(t)
	ctx := t.Context()
	name := itContainerName("labels")
	n := string(name)

	if !rec.IsIntegration() {
		rec.AddResult("abc123\n", "", nil)                                                                                                                                                             // run
		rec.AddResult(`[{"Id":"abc123","Name":"/`+n+`","State":{"Status":"running"},"Config":{"Labels":{"sind.cluster":"it-test","sind.role":"worker"}},"NetworkSettings":{"Networks":{}}}]`, "", nil) // inspect
		rec.AddResult(`{"ID":"abc123","Names":"`+n+`","State":"running","Image":"busybox:latest","Labels":"sind.cluster=it-test,sind.role=worker"}`+"\n", "", nil)                                     // list
		rec.AddResult("", "", nil)                                                                                                                                                                     // list empty
		rec.AddResult(n+"\n", "", nil)                                                                                                                                                                 // kill (cleanup)
		rec.AddResult(n+"\n", "", nil)                                                                                                                                                                 // rm (cleanup)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_ = c.KillContainer(cleanupCtx, name)
		_ = c.RemoveContainer(cleanupCtx, name)
	})

	_, err := c.RunContainer(ctx,
		"--name", string(name),
		"--label", "sind.cluster=it-test",
		"--label", "sind.role=worker",
		"busybox:latest", "sleep", "60",
	)
	require.NoError(t, err)

	// Inspect returns labels.
	info, err := c.InspectContainer(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "it-test", info.Labels["sind.cluster"])
	assert.Equal(t, "worker", info.Labels["sind.role"])

	// List by label.
	entries, err := c.ListContainers(ctx, "label=sind.cluster=it-test")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, name, entries[0].Name)

	// List no matches.
	entries, err = c.ListContainers(ctx, "label=sind.cluster=nonexistent")
	require.NoError(t, err)
	assert.Empty(t, entries)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestRunContainer(t *testing.T) {
	const containerID ContainerID = "b98dd34e3931dd738dd597ca2ae6fdc30a955be1c0662a321c634a82e5348ee9"

	var m cmdexec.MockExecutor
	m.AddResult(string(containerID)+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.RunContainer(t.Context(),
		"--name", string(testContainerName),
		"--label", "sind.cluster=dev",
		"alpine",
	)
	require.NoError(t, err)
	assert.Equal(t, containerID, id)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, "docker", m.Calls[0].Name)
	assert.Equal(t, []string{
		"run", "-d",
		"--name", string(testContainerName),
		"--label", "sind.cluster=dev",
		"alpine",
	}, m.Calls[0].Args)
}

func TestRunContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "docker: Error response from daemon: Conflict.\n", fmt.Errorf("exit status 125"))
	c := NewClient(&m)

	id, err := c.RunContainer(t.Context(), "alpine")
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestStartContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.StartContainer(t.Context(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"start", string(testContainerName)}, m.Calls[0].Args)
}

func TestStartContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.StartContainer(t.Context(), testContainerName)
	assert.Error(t, err)
}

func TestStopContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.StopContainer(t.Context(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"stop", string(testContainerName)}, m.Calls[0].Args)
}

func TestStopContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.StopContainer(t.Context(), testContainerName)
	assert.Error(t, err)
}

func TestRemoveContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveContainer(t.Context(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"rm", string(testContainerName)}, m.Calls[0].Args)
}

func TestRemoveContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveContainer(t.Context(), testContainerName)
	assert.Error(t, err)
}

func TestPauseContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.PauseContainer(t.Context(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"pause", string(testContainerName)}, m.Calls[0].Args)
}

func TestPauseContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: container is not running\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.PauseContainer(t.Context(), testContainerName)
	assert.Error(t, err)
}

func TestUnpauseContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.UnpauseContainer(t.Context(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"unpause", string(testContainerName)}, m.Calls[0].Args)
}

func TestUnpauseContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: container is not paused\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.UnpauseContainer(t.Context(), testContainerName)
	assert.Error(t, err)
}

func TestKillContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.KillContainer(t.Context(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"kill", string(testContainerName)}, m.Calls[0].Args)
}

func TestKillContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.KillContainer(t.Context(), testContainerName)
	assert.Error(t, err)
}

func TestSignalContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.SignalContainer(t.Context(), testContainerName, "HUP")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"kill", "-s", "HUP", string(testContainerName)}, m.Calls[0].Args)
}

func TestSignalContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.SignalContainer(t.Context(), testContainerName, "HUP")
	assert.Error(t, err)
}

const inspectJSON = `[{
  "Id": "94649329a21a97708c8f53c7348adafb926eaef1929b79ae760458a50d78e1ca",
  "Name": "/sind-dev-controller",
  "State": {"Status": "running", "Running": true, "Paused": false},
  "Config": {"Labels": {"sind.cluster": "dev", "sind.role": "controller"}},
  "NetworkSettings": {
    "Networks": {
      "sind-dev-net": {"IPAddress": "172.18.0.2"},
      "sind-mesh":    {"IPAddress": "172.19.0.3"}
    }
  }
}]`

func TestInspectContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(inspectJSON, "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(t.Context(), testContainerName)
	require.NoError(t, err)

	assert.Equal(t, ContainerID("94649329a21a97708c8f53c7348adafb926eaef1929b79ae760458a50d78e1ca"), info.ID)
	assert.Equal(t, testContainerName, info.Name)
	assert.Equal(t, "running", info.Status)
	assert.Equal(t, map[string]string{
		"sind.cluster": "dev",
		"sind.role":    "controller",
	}, info.Labels)
	assert.Equal(t, map[NetworkName]string{
		"sind-dev-net": "172.18.0.2",
		"sind-mesh":    "172.19.0.3",
	}, info.IPs)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"inspect", string(testContainerName)}, m.Calls[0].Args)
}

func TestInspectContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such object: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	info, err := c.InspectContainer(t.Context(), testContainerName)
	assert.Error(t, err)
	assert.Nil(t, info)
}

func TestInspectContainer_InvalidJSON(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("not json", "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(t.Context(), testContainerName)
	assert.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "parsing inspect output")
}

func TestInspectContainer_EmptyResult(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("[]", "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(t.Context(), testContainerName)
	assert.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "no results")
}

const psJSON = `{"ID":"94649329a21a97708c8f53c7348adafb926eaef1929b79ae760458a50d78e1ca","Names":"sind-dev-controller","State":"running","Image":"ghcr.io/gsi-hpc/sind-node:25.11","Labels":"sind.cluster=dev,sind.role=controller"}
{"ID":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","Names":"sind-dev-worker-0","State":"running","Image":"ghcr.io/gsi-hpc/sind-node:25.11","Labels":"sind.cluster=dev,sind.role=worker"}`

func TestListContainers(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult(psJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(t.Context(), "label=sind.cluster=dev")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, ContainerName("sind-dev-controller"), entries[0].Name)
	assert.Equal(t, "running", entries[0].State)
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:25.11", entries[0].Image)
	assert.Equal(t, map[string]string{"sind.cluster": "dev", "sind.role": "controller"}, entries[0].Labels)

	assert.Equal(t, ContainerName("sind-dev-worker-0"), entries[1].Name)
	assert.Equal(t, map[string]string{"sind.cluster": "dev", "sind.role": "worker"}, entries[1].Labels)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"ps", "-a", "--no-trunc", "--format", "json", "--filter", "label=sind.cluster=dev"}, m.Calls[0].Args)
}

func TestListContainers_NoLabels(t *testing.T) {
	const noLabelsJSON = `{"ID":"abc123","Names":"sind-dev-controller","State":"running","Image":"img"}`
	var m cmdexec.MockExecutor
	m.AddResult(noLabelsJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Nil(t, entries[0].Labels)
}

func TestListContainers_Empty(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(t.Context(), "label=sind.cluster=none")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestListContainers_MultipleFilters(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	_, _ = c.ListContainers(t.Context(), "label=sind.cluster=dev", "label=sind.role=controller")

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"ps", "-a", "--no-trunc", "--format", "json",
		"--filter", "label=sind.cluster=dev",
		"--filter", "label=sind.role=controller",
	}, m.Calls[0].Args)
}

func TestListContainers_InvalidJSON(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("not json\n", "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
	assert.Contains(t, err.Error(), "parsing ps output")
}

func TestListContainers_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	entries, err := c.ListContainers(t.Context())
	assert.Error(t, err)
	assert.Nil(t, entries)
}

func TestExec(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("ssh-ed25519 AAAA...\n", "", nil)
	c := NewClient(&m)

	stdout, err := c.Exec(t.Context(), testContainerName, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAA...\n", stdout)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainerName), "cat", "/etc/ssh/ssh_host_ed25519_key.pub"}, m.Calls[0].Args)
}

func TestExec_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "OCI runtime exec failed\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	stdout, err := c.Exec(t.Context(), testContainerName, "false")
	assert.Error(t, err)
	assert.Empty(t, stdout)
}

func TestExecWithStdin(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	stdin := strings.NewReader("ssh-ed25519 AAAA... root@sind\n")
	err := c.ExecWithStdin(t.Context(), testContainerName, stdin, "sh", "-c", "cat >> /root/.ssh/authorized_keys")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "-i", string(testContainerName), "sh", "-c", "cat >> /root/.ssh/authorized_keys"}, m.Calls[0].Args)
	assert.Equal(t, "ssh-ed25519 AAAA... root@sind\n", m.Calls[0].Stdin)
}

func TestExecWithStdin_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	stdin := strings.NewReader("data")
	err := c.ExecWithStdin(t.Context(), testContainerName, stdin, "cat")
	assert.Error(t, err)
}

func TestReadFile(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("line1\nline2\n", "", nil)
	c := NewClient(&m)

	content, err := c.ReadFile(t.Context(), testContainerName, "/etc/hosts")
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", content)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainerName), "cat", "/etc/hosts"}, m.Calls[0].Args)
}

func TestReadFile_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "cat: /missing: No such file\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	_, err := c.ReadFile(t.Context(), testContainerName, "/missing")
	assert.Error(t, err)
}

func TestWriteFile(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.WriteFile(t.Context(), testContainerName, "/tmp/out", "hello\n")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "-i", string(testContainerName), "sh", "-c", "cat > /tmp/out"}, m.Calls[0].Args)
	assert.Equal(t, "hello\n", m.Calls[0].Stdin)
}

func TestWriteFile_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.WriteFile(t.Context(), testContainerName, "/tmp/out", "data")
	assert.Error(t, err)
}

func TestAppendFile(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.AppendFile(t.Context(), testContainerName, "/tmp/log", "new line\n")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "-i", string(testContainerName), "sh", "-c", "cat >> /tmp/log"}, m.Calls[0].Args)
	assert.Equal(t, "new line\n", m.Calls[0].Stdin)
}

func TestAppendFile_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.AppendFile(t.Context(), testContainerName, "/tmp/log", "data")
	assert.Error(t, err)
}

func TestContainerExists_True(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := NewClient(&m)

	exists, err := c.ContainerExists(t.Context(), testContainerName)
	require.NoError(t, err)
	assert.True(t, exists)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"container", "inspect", string(testContainerName)}, m.Calls[0].Args)
}

func TestContainerExists_False(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	exists, err := c.ContainerExists(t.Context(), testContainerName)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestContainerExists_OtherError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	exists, err := c.ContainerExists(t.Context(), testContainerName)
	assert.Error(t, err)
	assert.False(t, exists)
}

func TestCreateContainer(t *testing.T) {
	const containerID ContainerID = "b98dd34e3931dd738dd597ca2ae6fdc30a955be1c0662a321c634a82e5348ee9"

	var m cmdexec.MockExecutor
	m.AddResult(string(containerID)+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateContainer(t.Context(),
		"--name", string(testContainerName),
		"--network", "sind-mesh",
		"coredns/coredns:latest",
	)
	require.NoError(t, err)
	assert.Equal(t, containerID, id)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"create",
		"--name", string(testContainerName),
		"--network", "sind-mesh",
		"coredns/coredns:latest",
	}, m.Calls[0].Args)
}

func TestCreateContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "docker: Error response from daemon: Conflict.\n", fmt.Errorf("exit status 125"))
	c := NewClient(&m)

	id, err := c.CreateContainer(t.Context(), "alpine")
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestCopyToContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	files := map[string][]byte{
		"Corefile": []byte(".:53 { whoami }\n"),
		"hosts":    []byte(""),
	}
	err := c.CopyToContainer(t.Context(), testContainerName, "/", files)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"cp", "-", string(testContainerName) + ":/"}, m.Calls[0].Args)

	// Verify tar content
	tr := tar.NewReader(strings.NewReader(m.Calls[0].Stdin))

	hdr, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "Corefile", hdr.Name)
	content, _ := io.ReadAll(tr)
	assert.Equal(t, ".:53 { whoami }\n", string(content))

	hdr, err = tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "hosts", hdr.Name)
	content, _ = io.ReadAll(tr)
	assert.Equal(t, "", string(content))
}

func TestCopyFilesToContainer(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	files := map[string]File{
		"munge.key": {Content: []byte("secret"), Mode: 0600},
		"config":    {Content: []byte("data"), Mode: 0644},
	}
	err := c.CopyFilesToContainer(t.Context(), testContainerName, "/etc", files)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"cp", "-", string(testContainerName) + ":/etc"}, m.Calls[0].Args)

	// Verify tar content and permissions (sorted order: config, munge.key)
	tr := tar.NewReader(strings.NewReader(m.Calls[0].Stdin))

	hdr, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "config", hdr.Name)
	assert.Equal(t, int64(0644), hdr.Mode)

	hdr, err = tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "munge.key", hdr.Name)
	assert.Equal(t, int64(0600), hdr.Mode)
}

func TestBuildFilesTar_HeaderError(t *testing.T) {
	// Fail immediately — tar header write fails.
	w := &failWriter{failAfter: 0}
	files := map[string]File{
		"test": {Content: []byte("data"), Mode: 0644},
	}
	err := buildFilesTar(w, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing tar header")
}

func TestBuildFilesTar_ContentError(t *testing.T) {
	// Allow header to succeed, fail on content write.
	w := &failWriter{failAfter: 512}
	files := map[string]File{
		"test": {Content: []byte("data"), Mode: 0644},
	}
	err := buildFilesTar(w, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writing tar content")
}

func TestBuildFilesTar_CloseError(t *testing.T) {
	// Allow header + content, fail on close (two 512-byte blocks written).
	w := &failWriter{failAfter: 1024}
	files := map[string]File{
		"test": {Content: []byte("data"), Mode: 0644},
	}
	err := buildFilesTar(w, files)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closing tar writer")
}

// failWriter is an io.Writer that fails after a configurable number of bytes.
type failWriter struct {
	written   int
	failAfter int
}

func (w *failWriter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.failAfter {
		return 0, fmt.Errorf("write failed")
	}
	w.written += len(p)
	return len(p), nil
}

func TestCopyToContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.CopyToContainer(t.Context(), testContainerName, "/", map[string][]byte{"f": []byte("x")})
	assert.Error(t, err)
}

func TestCopyFromContainer(t *testing.T) {
	// Build a tar archive containing one file as docker cp would return
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("172.18.0.2 controller.default.sind.sind\n")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "hosts", Size: int64(len(content)), Mode: 0644}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	var m cmdexec.MockExecutor
	m.AddResult(buf.String(), "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(t.Context(), testContainerName, "/hosts")
	require.NoError(t, err)
	assert.Equal(t, content, data)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"cp", string(testContainerName) + ":/hosts", "-"}, m.Calls[0].Args)
}

func TestCopyFromContainer_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	data, err := c.CopyFromContainer(t.Context(), testContainerName, "/hosts")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestCopyFromContainer_InvalidTar(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("not a tar archive", "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(t.Context(), testContainerName, "/hosts")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "reading tar output")
}

func TestCopyFromContainer_TruncatedTar(t *testing.T) {
	// Build a tar with a header claiming 100 bytes but only 5 bytes of data
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "hosts", Size: 100, Mode: 0644}))
	_, err := tw.Write([]byte("short"))
	require.NoError(t, err)
	// Don't close properly — leaves a truncated entry

	var m cmdexec.MockExecutor
	m.AddResult(buf.String(), "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(t.Context(), testContainerName, "/hosts")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "reading file content")
}

func TestCopyFromContainer_EmptyTar(t *testing.T) {
	// Empty tar archive (no entries)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.Close())

	var m cmdexec.MockExecutor
	m.AddResult(buf.String(), "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(t.Context(), testContainerName, "/nonexistent")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "file not found in tar output")
}
