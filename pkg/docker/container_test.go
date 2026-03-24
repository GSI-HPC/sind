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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunContainer(t *testing.T) {
	const containerID ContainerID = "b98dd34e3931dd738dd597ca2ae6fdc30a955be1c0662a321c634a82e5348ee9"

	var m MockExecutor
	m.AddResult(string(containerID)+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.RunContainer(context.Background(),
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
	var m MockExecutor
	m.AddResult("", "docker: Error response from daemon: Conflict.\n", fmt.Errorf("exit status 125"))
	c := NewClient(&m)

	id, err := c.RunContainer(context.Background(), "alpine")
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestStartContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.StartContainer(context.Background(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"start", string(testContainerName)}, m.Calls[0].Args)
}

func TestStartContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.StartContainer(context.Background(), testContainerName)
	assert.Error(t, err)
}

func TestStopContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.StopContainer(context.Background(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"stop", string(testContainerName)}, m.Calls[0].Args)
}

func TestStopContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.StopContainer(context.Background(), testContainerName)
	assert.Error(t, err)
}

func TestRemoveContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.RemoveContainer(context.Background(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"rm", string(testContainerName)}, m.Calls[0].Args)
}

func TestRemoveContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.RemoveContainer(context.Background(), testContainerName)
	assert.Error(t, err)
}

func TestKillContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult(string(testContainerName)+"\n", "", nil)
	c := NewClient(&m)

	err := c.KillContainer(context.Background(), testContainerName)
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"kill", string(testContainerName)}, m.Calls[0].Args)
}

func TestKillContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.KillContainer(context.Background(), testContainerName)
	assert.Error(t, err)
}

func TestSignalContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.SignalContainer(context.Background(), testContainerName, "HUP")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"kill", "-s", "HUP", string(testContainerName)}, m.Calls[0].Args)
}

func TestSignalContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.SignalContainer(context.Background(), testContainerName, "HUP")
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
	var m MockExecutor
	m.AddResult(inspectJSON, "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainerName)
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
	var m MockExecutor
	m.AddResult("", "Error: No such object: "+string(testContainerName)+"\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainerName)
	assert.Error(t, err)
	assert.Nil(t, info)
}

func TestInspectContainer_InvalidJSON(t *testing.T) {
	var m MockExecutor
	m.AddResult("not json", "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainerName)
	assert.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "parsing inspect output")
}

func TestInspectContainer_EmptyResult(t *testing.T) {
	var m MockExecutor
	m.AddResult("[]", "", nil)
	c := NewClient(&m)

	info, err := c.InspectContainer(context.Background(), testContainerName)
	assert.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "no results")
}

const psJSON = `{"ID":"94649329a21a97708c8f53c7348adafb926eaef1929b79ae760458a50d78e1ca","Names":"sind-dev-controller","State":"running","Image":"ghcr.io/gsi-hpc/sind-node:25.11","Labels":"sind.cluster=dev,sind.role=controller"}
{"ID":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","Names":"sind-dev-compute-0","State":"running","Image":"ghcr.io/gsi-hpc/sind-node:25.11","Labels":"sind.cluster=dev,sind.role=compute"}`

func TestListContainers(t *testing.T) {
	var m MockExecutor
	m.AddResult(psJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background(), "label=sind.cluster=dev")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, ContainerName("sind-dev-controller"), entries[0].Name)
	assert.Equal(t, "running", entries[0].State)
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:25.11", entries[0].Image)
	assert.Equal(t, map[string]string{"sind.cluster": "dev", "sind.role": "controller"}, entries[0].Labels)

	assert.Equal(t, ContainerName("sind-dev-compute-0"), entries[1].Name)
	assert.Equal(t, map[string]string{"sind.cluster": "dev", "sind.role": "compute"}, entries[1].Labels)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"ps", "-a", "--no-trunc", "--format", "json", "--filter", "label=sind.cluster=dev"}, m.Calls[0].Args)
}

func TestListContainers_NoLabels(t *testing.T) {
	const noLabelsJSON = `{"ID":"abc123","Names":"sind-dev-controller","State":"running","Image":"img"}`
	var m MockExecutor
	m.AddResult(noLabelsJSON, "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Nil(t, entries[0].Labels)
}

func TestListContainers_Empty(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background(), "label=sind.cluster=none")
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestListContainers_MultipleFilters(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	c.ListContainers(context.Background(), "label=sind.cluster=dev", "label=sind.role=controller")

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"ps", "-a", "--no-trunc", "--format", "json",
		"--filter", "label=sind.cluster=dev",
		"--filter", "label=sind.role=controller",
	}, m.Calls[0].Args)
}

func TestListContainers_InvalidJSON(t *testing.T) {
	var m MockExecutor
	m.AddResult("not json\n", "", nil)
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background())
	assert.Error(t, err)
	assert.Nil(t, entries)
	assert.Contains(t, err.Error(), "parsing ps output")
}

func TestListContainers_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	entries, err := c.ListContainers(context.Background())
	assert.Error(t, err)
	assert.Nil(t, entries)
}

func TestExec(t *testing.T) {
	var m MockExecutor
	m.AddResult("ssh-ed25519 AAAA...\n", "", nil)
	c := NewClient(&m)

	stdout, err := c.Exec(context.Background(), testContainerName, "cat", "/etc/ssh/ssh_host_ed25519_key.pub")
	require.NoError(t, err)
	assert.Equal(t, "ssh-ed25519 AAAA...\n", stdout)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainerName), "cat", "/etc/ssh/ssh_host_ed25519_key.pub"}, m.Calls[0].Args)
}

func TestExec_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "OCI runtime exec failed\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	stdout, err := c.Exec(context.Background(), testContainerName, "false")
	assert.Error(t, err)
	assert.Empty(t, stdout)
}

func TestExecWithStdin(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	stdin := strings.NewReader("ssh-ed25519 AAAA... root@sind\n")
	err := c.ExecWithStdin(context.Background(), testContainerName, stdin, "sh", "-c", "cat >> /root/.ssh/authorized_keys")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "-i", string(testContainerName), "sh", "-c", "cat >> /root/.ssh/authorized_keys"}, m.Calls[0].Args)
	assert.Equal(t, "ssh-ed25519 AAAA... root@sind\n", m.Calls[0].Stdin)
}

func TestExecWithStdin_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	stdin := strings.NewReader("data")
	err := c.ExecWithStdin(context.Background(), testContainerName, stdin, "cat")
	assert.Error(t, err)
}

func TestReadFile(t *testing.T) {
	var m MockExecutor
	m.AddResult("line1\nline2\n", "", nil)
	c := NewClient(&m)

	content, err := c.ReadFile(context.Background(), testContainerName, "/etc/hosts")
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", content)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", string(testContainerName), "cat", "/etc/hosts"}, m.Calls[0].Args)
}

func TestReadFile_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "cat: /missing: No such file\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	_, err := c.ReadFile(context.Background(), testContainerName, "/missing")
	assert.Error(t, err)
}

func TestWriteFile(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.WriteFile(context.Background(), testContainerName, "/tmp/out", "hello\n")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "-i", string(testContainerName), "sh", "-c", "cat > /tmp/out"}, m.Calls[0].Args)
	assert.Equal(t, "hello\n", m.Calls[0].Stdin)
}

func TestWriteFile_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.WriteFile(context.Background(), testContainerName, "/tmp/out", "data")
	assert.Error(t, err)
}

func TestAppendFile(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	err := c.AppendFile(context.Background(), testContainerName, "/tmp/log", "new line\n")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"exec", "-i", string(testContainerName), "sh", "-c", "cat >> /tmp/log"}, m.Calls[0].Args)
	assert.Equal(t, "new line\n", m.Calls[0].Stdin)
}

func TestAppendFile_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.AppendFile(context.Background(), testContainerName, "/tmp/log", "data")
	assert.Error(t, err)
}

func TestContainerExists_True(t *testing.T) {
	var m MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := NewClient(&m)

	exists, err := c.ContainerExists(context.Background(), testContainerName)
	require.NoError(t, err)
	assert.True(t, exists)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"container", "inspect", string(testContainerName)}, m.Calls[0].Args)
}

func TestContainerExists_False(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container: "+string(testContainerName)+"\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	c := NewClient(&m)

	exists, err := c.ContainerExists(context.Background(), testContainerName)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestContainerExists_OtherError(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := NewClient(&m)

	exists, err := c.ContainerExists(context.Background(), testContainerName)
	assert.Error(t, err)
	assert.False(t, exists)
}

func TestCreateContainer(t *testing.T) {
	const containerID ContainerID = "b98dd34e3931dd738dd597ca2ae6fdc30a955be1c0662a321c634a82e5348ee9"

	var m MockExecutor
	m.AddResult(string(containerID)+"\n", "", nil)
	c := NewClient(&m)

	id, err := c.CreateContainer(context.Background(),
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
	var m MockExecutor
	m.AddResult("", "docker: Error response from daemon: Conflict.\n", fmt.Errorf("exit status 125"))
	c := NewClient(&m)

	id, err := c.CreateContainer(context.Background(), "alpine")
	assert.Error(t, err)
	assert.Empty(t, id)
}

func TestCopyToContainer(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "", nil)
	c := NewClient(&m)

	files := map[string][]byte{
		"Corefile": []byte(".:53 { whoami }\n"),
		"hosts":    []byte(""),
	}
	err := c.CopyToContainer(context.Background(), testContainerName, "/", files)
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

func TestCopyToContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	err := c.CopyToContainer(context.Background(), testContainerName, "/", map[string][]byte{"f": []byte("x")})
	assert.Error(t, err)
}

func TestCopyFromContainer(t *testing.T) {
	// Build a tar archive containing one file as docker cp would return
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("172.18.0.2 controller.default.sind.local\n")
	tw.WriteHeader(&tar.Header{Name: "hosts", Size: int64(len(content)), Mode: 0644})
	tw.Write(content)
	tw.Close()

	var m MockExecutor
	m.AddResult(buf.String(), "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(context.Background(), testContainerName, "/hosts")
	require.NoError(t, err)
	assert.Equal(t, content, data)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"cp", string(testContainerName) + ":/hosts", "-"}, m.Calls[0].Args)
}

func TestCopyFromContainer_Error(t *testing.T) {
	var m MockExecutor
	m.AddResult("", "Error: No such container\n", fmt.Errorf("exit status 1"))
	c := NewClient(&m)

	data, err := c.CopyFromContainer(context.Background(), testContainerName, "/hosts")
	assert.Error(t, err)
	assert.Nil(t, data)
}

func TestCopyFromContainer_InvalidTar(t *testing.T) {
	var m MockExecutor
	m.AddResult("not a tar archive", "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(context.Background(), testContainerName, "/hosts")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "reading tar output")
}

func TestCopyFromContainer_TruncatedTar(t *testing.T) {
	// Build a tar with a header claiming 100 bytes but only 5 bytes of data
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "hosts", Size: 100, Mode: 0644})
	tw.Write([]byte("short"))
	// Don't close properly — leaves a truncated entry

	var m MockExecutor
	m.AddResult(buf.String(), "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(context.Background(), testContainerName, "/hosts")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "reading file content")
}

func TestCopyFromContainer_EmptyTar(t *testing.T) {
	// Empty tar archive (no entries)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.Close()

	var m MockExecutor
	m.AddResult(buf.String(), "", nil)
	c := NewClient(&m)

	data, err := c.CopyFromContainer(context.Background(), testContainerName, "/nonexistent")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "file not found in tar output")
}
