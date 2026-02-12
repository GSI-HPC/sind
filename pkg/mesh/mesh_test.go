// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- EnsureMeshNetwork ---

func TestEnsureMeshNetwork_Creates(t *testing.T) {
	const networkID = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"

	var m docker.MockExecutor
	// NetworkExists → not found (exit code 1)
	m.AddResult("", "Error: No such network: sind-mesh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateNetwork → success
	m.AddResult(networkID+"\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", string(cluster.MeshNetworkName)}, m.Calls[0].Args)
	assert.Equal(t, []string{"network", "create", string(cluster.MeshNetworkName)}, m.Calls[1].Args)
}

func TestEnsureMeshNetwork_AlreadyExists(t *testing.T) {
	var m docker.MockExecutor
	// NetworkExists → found
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	require.NoError(t, err)

	// Only inspect, no create
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", string(cluster.MeshNetworkName)}, m.Calls[0].Args)
}

func TestEnsureMeshNetwork_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestEnsureMeshNetwork_CreateError(t *testing.T) {
	var m docker.MockExecutor
	// NetworkExists → not found
	m.AddResult("", "Error: No such network: sind-mesh\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateNetwork → error
	m.AddResult("", "Error: permission denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureMeshNetwork(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating mesh network")
}

// --- EnsureDNS ---

func TestEnsureDNS_Creates(t *testing.T) {
	const containerID = "abc123"

	var m docker.MockExecutor
	// ContainerExists → not found
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	// CreateContainer → success
	m.AddResult(containerID+"\n", "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// StartContainer → success
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	assert.Equal(t, []string{"container", "inspect", string(cluster.DNSContainerName)}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"create",
		"--name", string(cluster.DNSContainerName),
		"--network", string(cluster.MeshNetworkName),
		DNSImage,
	}, m.Calls[1].Args)
	assert.Equal(t, []string{"cp", "-", string(cluster.DNSContainerName) + ":/"}, m.Calls[2].Args)
	assert.Equal(t, []string{"start", string(cluster.DNSContainerName)}, m.Calls[3].Args)

	// Verify initial Corefile has empty hosts block
	corefile := extractTarFile(t, m.Calls[2].Stdin, "Corefile")
	assert.Contains(t, corefile, "hosts {")
	assert.Contains(t, corefile, "fallthrough")
	assert.NotContains(t, corefile, "sind.local\n")
}

func TestEnsureDNS_AlreadyExists(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1)
}

func TestEnsureDNS_InspectError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking DNS container")
}

func TestEnsureDNS_CreateError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("", "Error: pull access denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating DNS container")
}

func TestEnsureDNS_CopyError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS configuration")
}

func TestEnsureDNS_StartError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error: No such container: sind-dns\n",
		&exec.ExitError{ProcessState: exitCode1(t)})
	m.AddResult("abc123\n", "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error: cannot start\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.EnsureDNS(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "starting DNS container")
}

// --- AddDNSRecord ---

func TestAddDNSRecord_Empty(t *testing.T) {
	var m docker.MockExecutor
	// CopyFromContainer → Corefile with empty hosts block
	m.AddResult(corefileTar(t, nil), "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// SignalContainer → success
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(context.Background(), "controller.dev.sind.local", "172.18.0.2")
	require.NoError(t, err)

	require.Len(t, m.Calls, 3)
	// Verify read
	assert.Equal(t, []string{"cp", string(cluster.DNSContainerName) + ":/Corefile", "-"}, m.Calls[0].Args)
	// Verify written Corefile contains the record
	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.local")
	// Verify SIGHUP
	assert.Equal(t, []string{"kill", "-s", "HUP", string(cluster.DNSContainerName)}, m.Calls[2].Args)
}

func TestAddDNSRecord_Appends(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.local"}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(context.Background(), "compute-0.dev.sind.local", "172.18.0.3")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.local")
	assert.Contains(t, corefile, "172.18.0.3 compute-0.dev.sind.local")
}

func TestAddDNSRecord_ReadError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(context.Background(), "controller.dev.sind.local", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestAddDNSRecord_WriteError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(context.Background(), "controller.dev.sind.local", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

func TestAddDNSRecord_SignalError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.AddDNSRecord(context.Background(), "controller.dev.sind.local", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

// --- RemoveDNSRecord ---

func TestRemoveDNSRecord(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.local",
		"172.18.0.3 compute-0.dev.sind.local",
	}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(context.Background(), "controller.dev.sind.local")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.local")
	assert.Contains(t, corefile, "172.18.0.3 compute-0.dev.sind.local")
}

func TestRemoveDNSRecord_LastEntry(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.local"}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(context.Background(), "controller.dev.sind.local")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.local")
	// Should still have valid Corefile structure
	assert.Contains(t, corefile, "hosts {")
	assert.Contains(t, corefile, "fallthrough")
}

func TestRemoveDNSRecord_NotFound(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.local"}

	var m docker.MockExecutor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(context.Background(), "compute-0.dev.sind.local")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.local")
}

func TestRemoveDNSRecord_ReadError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(context.Background(), "controller.dev.sind.local")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestRemoveDNSRecord_WriteError(t *testing.T) {
	var m docker.MockExecutor
	m.AddResult(corefileTar(t, []string{"172.18.0.2 x"}), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c)

	err := mgr.RemoveDNSRecord(context.Background(), "x")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

// --- generateCorefile / parseEntries ---

func TestGenerateCorefile_Empty(t *testing.T) {
	cf := generateCorefile(nil)
	assert.Contains(t, cf, "sind.local:53")
	assert.Contains(t, cf, "hosts {")
	assert.Contains(t, cf, "fallthrough")
	assert.Contains(t, cf, "forward . /etc/resolv.conf")
}

func TestGenerateCorefile_WithEntries(t *testing.T) {
	entries := []string{
		"172.18.0.2 controller.dev.sind.local",
		"172.18.0.3 compute-0.dev.sind.local",
	}
	cf := generateCorefile(entries)
	assert.Contains(t, cf, "        172.18.0.2 controller.dev.sind.local\n")
	assert.Contains(t, cf, "        172.18.0.3 compute-0.dev.sind.local\n")
}

func TestParseEntries_Empty(t *testing.T) {
	cf := generateCorefile(nil)
	entries := parseEntries(cf)
	assert.Empty(t, entries)
}

func TestParseEntries_Roundtrip(t *testing.T) {
	original := []string{
		"172.18.0.2 controller.dev.sind.local",
		"172.18.0.3 compute-0.dev.sind.local",
	}
	cf := generateCorefile(original)
	parsed := parseEntries(cf)
	assert.Equal(t, original, parsed)
}

// --- test helpers ---

// corefileTar builds a tar archive containing a Corefile with the given entries,
// matching the format returned by docker cp.
func corefileTar(t *testing.T, entries []string) string {
	t.Helper()
	content := []byte(generateCorefile(entries))
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "Corefile", Size: int64(len(content)), Mode: 0644})
	tw.Write(content)
	tw.Close()
	return buf.String()
}

// extractTarFile reads a named file from a tar archive captured by MockExecutor.
func extractTarFile(t *testing.T, tarData, name string) string {
	t.Helper()
	tr := tar.NewReader(strings.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("file %q not found in tar", name)
		}
		require.NoError(t, err)
		if hdr.Name == name {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			return string(data)
		}
	}
}

// exitCode1 runs a command that exits with code 1 and returns its ProcessState.
func exitCode1(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ProcessState
}
