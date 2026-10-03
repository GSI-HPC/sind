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
	"path/filepath"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitCode1 runs a trivial shell command that exits with code 1 so tests can
// synthesise a real *os.ProcessState for use with *exec.ExitError.
func exitCode1(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ProcessState
}

// failure is a docker error result that is not "not found".
func failure() mock.Result {
	return mock.Result{Err: fmt.Errorf("connection refused")}
}

// warnings collects what a Manager reports with Warn.
func warnings(mgr *Manager) *[]string {
	var got []string
	mgr.OnWarning = func(msg string) { got = append(got, msg) }
	return &got
}

// --- Lifecycle ---

func TestMeshLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()
	mgr := NewManager(c, testutil.Realm("it-mesh"))

	if !rec.IsIntegration() {
		rec.SetOnCall(newFake(t).onCall)
	}
	t.Cleanup(func() { _ = mgr.CleanupMesh(context.Background()) })

	// EnsureMesh creates all resources.
	err := mgr.EnsureMesh(ctx)
	require.NoError(t, err)
	assert.True(t, mgr.Created())

	// Verify resources exist.
	exists, err := c.NetworkExists(ctx, mgr.NetworkName())
	require.NoError(t, err)
	assert.True(t, exists, "mesh network")

	exists, err = c.ContainerExists(ctx, mgr.DNSContainerName())
	require.NoError(t, err)
	assert.True(t, exists, "DNS container")

	exists, err = c.VolumeExists(ctx, mgr.SSHVolumeName())
	require.NoError(t, err)
	assert.True(t, exists, "SSH volume")

	exists, err = c.ContainerExists(ctx, mgr.SSHContainerName())
	require.NoError(t, err)
	assert.True(t, exists, "SSH container")

	// The relay has the keypair, so sind can read it.
	key, err := mgr.GetSSHPublicKey(ctx)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(key, "ssh-ed25519 "), "public key %q", key)

	// EnsureMesh is idempotent.
	err = mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// The info reports the images the containers run.
	info, err := mgr.GetInfo(ctx)
	require.NoError(t, err)
	assert.Equal(t, DNSImage, info.DNSImage)
	assert.Equal(t, SSHImage, info.SSHImage)
	assert.NotEmpty(t, info.DNSIP)

	// CleanupMesh removes everything.
	err = mgr.CleanupMesh(ctx)
	require.NoError(t, err)

	// Verify resources gone.
	exists, err = c.NetworkExists(ctx, mgr.NetworkName())
	require.NoError(t, err)
	assert.False(t, exists, "mesh network should be gone")

	exists, err = c.ContainerExists(ctx, mgr.DNSContainerName())
	require.NoError(t, err)
	assert.False(t, exists, "DNS container should be gone")

	exists, err = c.ContainerExists(ctx, mgr.SSHContainerName())
	require.NoError(t, err)
	assert.False(t, exists, "SSH container should be gone")

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestDNSRecordLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()
	mgr := NewManager(c, testutil.Realm("it-mesh"))

	if !rec.IsIntegration() {
		rec.SetOnCall(newFake(t).onCall)
	}
	t.Cleanup(func() { _ = mgr.CleanupMesh(context.Background()) })

	err := mgr.EnsureMesh(ctx)
	require.NoError(t, err)

	// Add two records.
	err = mgr.AddDNSRecord(ctx, "a.test.sind.sind", "172.18.0.2")
	require.NoError(t, err)

	err = mgr.AddDNSRecord(ctx, "b.test.sind.sind", "172.18.0.3")
	require.NoError(t, err)

	// Remove first record.
	err = mgr.RemoveDNSRecord(ctx, "a.test.sind.sind")
	require.NoError(t, err)

	records, err := mgr.GetDNSRecords(ctx)
	require.NoError(t, err)
	assert.Equal(t, []DNSRecord{{Hostname: "b.test.sind.sind", IP: "172.18.0.3"}}, records)

	// The reloads kept the DNS container running.
	info, err := c.InspectContainer(ctx, mgr.DNSContainerName())
	require.NoError(t, err)
	assert.Equal(t, docker.StateRunning, info.Status)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

// --- EnsureMesh ---

func TestEnsureMesh(t *testing.T) {
	f, c, m := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)
	got := warnings(mgr)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
	assert.True(t, mgr.Created())
	assert.Empty(t, *got)

	assert.True(t, f.networks["sind-mesh"])
	assert.True(t, f.volumes["sind-ssh-config"])

	dns := f.containers["sind-dns"]
	require.NotNil(t, dns)
	assert.Equal(t, docker.StateRunning, dns.state)
	assert.Equal(t, DNSImage, dns.image)
	assert.Equal(t, "10.0.0.2", dns.ips["sind-mesh"], "DNS is the first container on the mesh")
	assert.Contains(t, dns.files[corefilePath], "hosts {")

	// The relay resolves through the DNS container and got the keys
	// before it started; no helper container is involved.
	relay := f.containers["sind-ssh"]
	require.NotNil(t, relay)
	assert.Equal(t, docker.StateRunning, relay.state)
	assert.Equal(t, SSHImage, relay.image)
	assert.Equal(t, []string{"10.0.0.2"}, relay.dns)
	assert.Contains(t, relay.files["/root/.ssh/id_ed25519"], "OPENSSH PRIVATE KEY")
	assert.True(t, strings.HasPrefix(relay.files["/root/.ssh/id_ed25519.pub"], "ssh-ed25519 "))
	assert.Contains(t, relay.files, "/root/.ssh/known_hosts")
	assert.Empty(t, calls(m, "create --name sind-ssh-keygen"))

	// The relay starts after the DNS container.
	assert.Equal(t, []string{"start sind-dns", "start sind-ssh"}, calls(m, "start"))
	// Without --pull nothing is pulled.
	for _, create := range calls(m, "create") {
		assert.NotContains(t, create, "--pull")
	}
}

func TestEnsureMesh_Pull(t *testing.T) {
	_, c, m := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)
	mgr.Pull = true

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	creates := calls(m, "create")
	require.Len(t, creates, 2)
	for _, create := range creates {
		assert.Contains(t, create, "--pull always")
	}
}

func TestEnsureMesh_AllExist(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	mgr := NewManager(c, DefaultRealm)
	got := warnings(mgr)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
	assert.False(t, mgr.Created())
	assert.Empty(t, *got)

	// One inspect per resource, nothing else.
	assert.Len(t, m.Calls, 4)
	assert.Empty(t, calls(m, "start"))
	assert.Empty(t, calls(m, "create"))
}

// TestEnsureMesh_StartsStopped covers a realm after a host reboot or a Docker
// daemon restart: the mesh containers exist but are stopped.
func TestEnsureMesh_StartsStopped(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateExited)
	mgr := NewManager(c, DefaultRealm)
	got := warnings(mgr)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
	assert.False(t, mgr.Created())

	assert.Equal(t, []string{"start sind-dns", "start sind-ssh"}, calls(m, "start"), "DNS starts first")
	assert.Equal(t, docker.StateRunning, f.containers["sind-dns"].state)
	assert.Equal(t, docker.StateRunning, f.containers["sind-ssh"].state)
	assert.Equal(t, "10.0.0.2", f.containers["sind-dns"].ips["sind-mesh"], "DNS got its address back")
	assert.Empty(t, *got)
	assert.Empty(t, calls(m, "create"))
}

// TestEnsureMesh_DNSAddressChanged covers a DNS container that comes back
// with another address than the relay was created with.
func TestEnsureMesh_DNSAddressChanged(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateExited)
	// Another container took the DNS container's address meanwhile.
	f.containers["sind-dev-controller"] = &fakeContainer{state: docker.StateCreated, networks: []string{"sind-mesh"}}
	f.start("sind-dev-controller")
	mgr := NewManager(c, DefaultRealm)
	got := warnings(mgr)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)

	require.Len(t, *got, 1)
	assert.Contains(t, (*got)[0], "mesh DNS sind-dns now has the address 10.0.0.3")
	assert.Contains(t, (*got)[0], "containers created before still use 10.0.0.2 (sind-ssh)")
	assert.Contains(t, (*got)[0], "the SSH relay is re-created")
}

func TestEnsureMesh_ExistingVolumeKeepsKeys(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	delete(f.containers, "sind-ssh") // relay removed by hand
	mgr := NewManager(c, DefaultRealm)

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	// The relay is re-created on the volume, which keeps its keys.
	require.Len(t, calls(m, "create --name sind-ssh"), 1)
	assert.Empty(t, calls(m, "cp - sind-ssh"))
	assert.Equal(t, docker.StateRunning, f.containers["sind-ssh"].state)
}

func TestEnsureMesh_Errors(t *testing.T) {
	tests := []struct {
		name  string
		fail  string
		match string
	}{
		{"network check", "network inspect", "checking mesh network"},
		{"network create", "network create", "creating mesh network"},
		{"DNS check", "inspect sind-dns", "checking DNS container"},
		{"DNS create", "create --name sind-dns", "creating DNS container"},
		{"DNS Corefile", "cp - sind-dns", "writing DNS configuration"},
		{"DNS start", "start sind-dns", "starting DNS container"},
		{"volume check", "volume inspect", "checking SSH volume"},
		{"volume create", "volume create", "creating SSH volume"},
		{"relay check", "inspect sind-ssh", "checking SSH container"},
		{"relay create", "create --name sind-ssh", "creating SSH container"},
		{"relay keys", "cp - sind-ssh", "writing SSH keys"},
		{"relay start", "start sind-ssh", "starting SSH container"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c, _ := newFakeDocker(t)
			f.fail[tt.fail] = failure()
			mgr := NewManager(c, DefaultRealm)

			err := mgr.EnsureMesh(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

// TestEnsureMesh_DNSInspectAfterStartError covers the inspect that reads the
// address of a DNS container EnsureMesh just started.
func TestEnsureMesh_DNSInspectAfterStartError(t *testing.T) {
	var m mock.Executor
	inspected := false
	m.OnCall = func(args []string, _ string) mock.Result {
		joined := strings.Join(args, " ")
		switch {
		case joined == "network inspect sind-mesh", joined == "volume inspect sind-ssh-config":
			return mock.Result{Stdout: "[{}]\n"}
		case joined == "inspect sind-dns" && !inspected:
			inspected = true
			return mock.Result{Stdout: dnsInspectExitedJSON()}
		case joined == "start sind-dns":
			return mock.Result{}
		}
		return failure()
	}
	mgr := NewManager(docker.NewClient(&m), DefaultRealm)

	err := mgr.EnsureMesh(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container")
}

// --- EnsureMeshNetwork ---

func TestEnsureMeshNetwork_Creates(t *testing.T) {
	const networkID = "6f02052f0a95e0134b3f284b793c63803306b04225f9dc2b40cf48975a2e743b"

	var m mock.Executor
	// NetworkExists → not found (exit code 1)
	m.AddResult("", "Error: No such network: sind-mesh\n",
		testutil.ExitCode1(t))
	// CreateNetwork → success
	m.AddResult(networkID+"\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	assert.False(t, mgr.Created(), "Created() before EnsureMeshNetwork")
	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.True(t, mgr.Created(), "Created() after creating mesh network")

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"network", "create",
		"--label", "com.docker.compose.network=mesh",
		"--label", "com.docker.compose.project=sind-mesh",
		"--label", "sind.realm=" + DefaultRealm,
		string(NetworkName),
	}, m.Calls[1].Args)
}

func TestEnsureMeshNetwork_AlreadyExists(t *testing.T) {
	var m mock.Executor
	// NetworkExists → found
	m.AddResult("[{}]\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.False(t, mgr.Created(), "Created() when mesh already existed")

	// Only inspect, no create
	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[0].Args)
}

func TestEnsureMeshNetwork_InspectError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("connection refused"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMeshNetwork(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checking mesh network")
}

func TestEnsureMeshNetwork_CreateError(t *testing.T) {
	var m mock.Executor
	// NetworkExists → not found
	m.AddResult("", "Error: No such network: sind-mesh\n",
		testutil.ExitCode1(t))
	// CreateNetwork → error
	m.AddResult("", "Error: permission denied\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMeshNetwork(t.Context())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creating mesh network")
}

// --- StartMesh ---

func TestStartMesh_NoMesh(t *testing.T) {
	_, c, m := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)

	dnsIP, found, err := mgr.StartMesh(t.Context())
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, dnsIP)
	assert.Len(t, m.Calls, 2, "two inspects")
}

func TestStartMesh_Running(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	mgr := NewManager(c, DefaultRealm)

	dnsIP, found, err := mgr.StartMesh(t.Context())
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "10.0.0.2", dnsIP)
	assert.Empty(t, calls(m, "start"))
}

func TestStartMesh_Stopped(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateExited)
	mgr := NewManager(c, DefaultRealm)
	var sys mock.Executor
	sys.AddResult("", "", fmt.Errorf("inactive")) // host DNS: systemd-resolved not running
	mgr.Exec = &sys
	mgr.HostDNS = true

	dnsIP, found, err := mgr.StartMesh(t.Context())
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "10.0.0.2", dnsIP)
	assert.Equal(t, []string{"start sind-dns", "start sind-ssh"}, calls(m, "start"))
	require.Len(t, sys.Calls, 1, "host DNS reapplied")
	assert.Equal(t, "systemctl", sys.Calls[0].Name)
}

func TestStartMesh_OnlyDNS(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateExited)
	delete(f.containers, "sind-ssh")
	mgr := NewManager(c, DefaultRealm)

	_, found, err := mgr.StartMesh(t.Context())
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []string{"start sind-dns"}, calls(m, "start"))
}

func TestStartMesh_StaleRelay(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	f.containers["sind-ssh"].dns = []string{"10.0.0.9"}
	mgr := NewManager(c, DefaultRealm)
	got := warnings(mgr)

	_, _, err := mgr.StartMesh(t.Context())
	require.NoError(t, err)
	require.Len(t, *got, 1)
	assert.Contains(t, (*got)[0], "still use 10.0.0.9 (sind-ssh)")
}

func TestStartMesh_Errors(t *testing.T) {
	tests := []struct {
		name  string
		fail  string
		match string
	}{
		{"DNS inspect", "inspect sind-dns", "inspecting DNS container"},
		{"relay inspect", "inspect sind-ssh", "inspecting SSH container"},
		{"DNS start", "start sind-dns", "starting DNS container"},
		{"relay start", "start sind-ssh", "starting SSH container"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c, _ := newFakeDocker(t)
			f.withMesh(DefaultRealm, docker.StateExited)
			f.fail[tt.fail] = failure()
			mgr := NewManager(c, DefaultRealm)

			_, _, err := mgr.StartMesh(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

// --- WarnStaleDNS ---

func TestWarnStaleDNS(t *testing.T) {
	mgr := NewManager(nil, DefaultRealm)
	got := warnings(mgr)

	mgr.WarnStaleDNS(t.Context(), "10.0.0.5",
		&docker.ContainerInfo{Name: "sind-ssh", DNS: []string{"10.0.0.2"}},
		&docker.ContainerInfo{Name: "sind-dev-worker-0", DNS: []string{"10.0.0.2"}},
		&docker.ContainerInfo{Name: "sind-dev-controller", DNS: []string{"10.0.0.5"}},
		&docker.ContainerInfo{Name: "sind-other", DNS: nil},
	)

	require.Len(t, *got, 1)
	assert.Contains(t, (*got)[0], "mesh DNS sind-dns now has the address 10.0.0.5")
	assert.Contains(t, (*got)[0], "still use 10.0.0.2 (sind-ssh, sind-dev-worker-0):")
	assert.NotContains(t, (*got)[0], "sind-dev-controller")
	assert.NotContains(t, (*got)[0], "sind-other")
}

func TestWarnStaleDNS_NothingToReport(t *testing.T) {
	mgr := NewManager(nil, DefaultRealm)
	got := warnings(mgr)
	stale := &docker.ContainerInfo{Name: "sind-ssh", DNS: []string{"10.0.0.2"}}

	mgr.WarnStaleDNS(t.Context(), "", stale)
	mgr.WarnStaleDNS(t.Context(), "10.0.0.2", stale)

	assert.Empty(t, *got)
}

func TestWarn_WithoutOnWarning(t *testing.T) {
	mgr := NewManager(nil, DefaultRealm)
	mgr.Warn(t.Context(), "logged at warn level") // must not panic
}

// --- writeDNSEntries ---

func TestAddDNSRecord_Empty(t *testing.T) {
	var m mock.Executor
	// CopyFromContainer → Corefile with empty hosts block
	m.AddResult(corefileTar(t, nil), "", nil)
	// CopyToContainer → success
	m.AddResult("", "", nil)
	// InspectContainer → running
	m.AddResult(dnsInspectJSON(), "", nil)
	// SignalContainer → success
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	require.NoError(t, err)

	require.Len(t, m.Calls, 4)
	// Verify read
	assert.Equal(t, []string{"cp", string(DNSContainerName) + ":/Corefile", "-"}, m.Calls[0].Args)
	// Verify written Corefile contains the record
	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
	// Verify state inspection + in-place reload (CoreDNS ignores SIGHUP)
	assert.Equal(t, []string{"inspect", string(DNSContainerName)}, m.Calls[2].Args)
	assert.Equal(t, []string{"kill", "-s", "USR1", string(DNSContainerName)}, m.Calls[3].Args)
}

func TestAddDNSRecord_Appends(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "worker-0.dev.sind.sind", "172.18.0.3")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
}

func TestAddDNSRecord_ReplacesExisting(t *testing.T) {
	// Re-registering a host at a new IP leaves one record, not two.
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("sind-dns\n", "", nil)
	m.AddResult("sind-dns\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.9")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.9 controller.dev.sind.sind")
	assert.NotContains(t, corefile, "172.18.0.2")
}

func TestAddDNSRecord_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestAddDNSRecord_WriteError(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

func TestAddDNSRecord_ReloadError(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, nil), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecord(t.Context(), "controller.dev.sind.sind", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

// TestAddDNSRecords_Cancelled covers Ctrl+C during a DNS update: the write
// and the reload still run, so CoreDNS loads the Corefile it was given.
func TestAddDNSRecords_Cancelled(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	mgr := NewManager(c, DefaultRealm)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// The mock ignores the context, so this checks that the write and
	// the reload do not depend on it.
	err := mgr.writeDNSEntries(ctx, []string{"172.18.0.2 controller.dev.sind.sind"})
	require.NoError(t, err)
	assert.Equal(t, []string{"USR1"}, f.containers["sind-dns"].signals)
}

// --- AddDNSRecords (batch) ---

func TestAddDNSRecords(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	m.AddResult("", "", nil)                       // write
	m.AddResult(dnsInspectJSON(), "", nil)         // inspect → running
	m.AddResult("sind-dns\n", "", nil)             // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "worker-0.dev.sind.sind", IP: "172.18.0.3"},
		{Hostname: "worker-1.dev.sind.sind", IP: "172.18.0.4"},
	})
	require.NoError(t, err)

	// Four docker calls total (read, write, inspect, reload).
	require.Len(t, m.Calls, 4)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.4 worker-1.dev.sind.sind")
}

func TestAddDNSRecords_Dedup(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	m.AddResult("", "", nil)                       // write
	m.AddResult(dnsInspectJSON(), "", nil)         // inspect → running
	m.AddResult("sind-dns\n", "", nil)             // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	// Re-add controller with a new IP — should replace, not duplicate.
	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "controller.dev.sind.sind", IP: "172.18.0.5"},
	})
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.5 controller.dev.sind.sind")
	assert.Equal(t, 1, strings.Count(corefile, "controller.dev.sind.sind"),
		"controller should appear exactly once")
}

// TestAddDNSRecords_Unchanged covers records that are already in place, as
// when a power on finds the nodes at their old addresses: no write, no
// reload.
func TestAddDNSRecords_Unchanged(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "worker-0.dev.sind.sind", IP: "172.18.0.3"},
	})
	require.NoError(t, err)
	assert.Len(t, m.Calls, 1)
}

// TestAddDNSRecords_DuplicateEntry covers a Corefile with an entry twice:
// the rewrite drops the duplicate.
func TestAddDNSRecords_DuplicateEntry(t *testing.T) {
	existing := []string{
		"172.18.0.3 worker-0.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	m.AddResult("", "", nil)                       // write
	m.AddResult(dnsInspectJSON(), "", nil)         // inspect → running
	m.AddResult("sind-dns\n", "", nil)             // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "worker-0.dev.sind.sind", IP: "172.18.0.3"},
	})
	require.NoError(t, err)
	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Equal(t, 1, strings.Count(corefile, "worker-0.dev.sind.sind"))
}

func TestAddDNSRecords_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "controller.dev.sind.sind", IP: "172.18.0.2"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS")
}

// --- RemoveDNSRecords (batch) ---

func TestRemoveDNSRecords(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
		"172.18.0.4 worker-1.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil) // read
	m.AddResult("", "", nil)                       // write
	m.AddResult(dnsInspectJSON(), "", nil)         // inspect → running
	m.AddResult("sind-dns\n", "", nil)             // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), []string{
		"controller.dev.sind.sind",
		"worker-1.dev.sind.sind",
	})
	require.NoError(t, err)

	// Four docker calls total (read, write, inspect, reload).
	require.Len(t, m.Calls, 4)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
	assert.NotContains(t, corefile, "worker-1.dev.sind.sind")
}

func TestRemoveDNSRecords_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), []string{"controller.dev.sind.sind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS")
}

func TestAddDNSRecords_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, m.Calls, "no docker calls for empty slice")
}

func TestRemoveDNSRecords_Empty(t *testing.T) {
	var m mock.Executor
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, m.Calls, "no docker calls for empty slice")
}

// TestRemoveDNSRecords_DNSStopped covers issue #52 and its follow-up:
// deleting a cluster while the sind-dns container is stopped must not fail,
// and starts the DNS container (and the stopped relay), which loads the new
// Corefile, instead of leaving the realm without DNS.
func TestRemoveDNSRecords_DNSStopped(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateExited)
	f.containers["sind-dns"].files[corefilePath] = generateCorefile(DefaultRealm, []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	})
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), []string{"controller.dev.sind.sind"})
	require.NoError(t, err)

	dns := f.containers["sind-dns"]
	assert.Equal(t, docker.StateRunning, dns.state)
	assert.Empty(t, dns.signals, "a started CoreDNS reads the Corefile anyway")
	assert.NotContains(t, dns.files[corefilePath], "controller.dev.sind.sind")
	assert.Contains(t, dns.files[corefilePath], "172.18.0.3 worker-0.dev.sind.sind")
	assert.Equal(t, []string{"start sind-dns", "start sind-ssh"}, calls(m, "start"))
}

// TestAddDNSRecords_DNSStoppedStartError covers a stopped DNS container that
// does not start.
func TestAddDNSRecords_DNSStoppedStartError(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateExited)
	f.fail["start sind-dns"] = failure()
	mgr := NewManager(c, DefaultRealm)

	err := mgr.AddDNSRecords(t.Context(), []DNSRecord{
		{Hostname: "controller.dev.sind.sind", IP: "172.18.0.2"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

// TestRemoveDNSRecords_InspectError verifies that an inspect failure between
// writing the Corefile and reloading DNS surfaces as a wrapped error.
func TestRemoveDNSRecords_InspectError(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecords(t.Context(), []string{"controller.dev.sind.sind"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container")
}

// --- RemoveDNSRecord ---

func TestRemoveDNSRecord(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("sind-dns\n", "", nil) // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
}

func TestRemoveDNSRecord_LastEntry(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("sind-dns\n", "", nil) // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	// Should still have valid Corefile structure
	assert.Contains(t, corefile, "hosts {")
	assert.Contains(t, corefile, "fallthrough")
}

func TestRemoveDNSRecord_NotFound(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("sind-dns\n", "", nil) // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "worker-0.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.Contains(t, corefile, "172.18.0.2 controller.dev.sind.sind")
}

func TestRemoveDNSRecord_ReloadError(t *testing.T) {
	existing := []string{"172.18.0.2 controller.dev.sind.sind"}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reloading DNS")
}

func TestRemoveDNSRecord_DuplicateHostnames(t *testing.T) {
	existing := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.5 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}

	var m mock.Executor
	m.AddResult(corefileTar(t, existing), "", nil)
	m.AddResult("", "", nil)
	m.AddResult(dnsInspectJSON(), "", nil)
	m.AddResult("sind-dns\n", "", nil) // kill -s USR1
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	require.NoError(t, err)

	corefile := extractTarFile(t, m.Calls[1].Stdin, "Corefile")
	assert.NotContains(t, corefile, "controller.dev.sind.sind")
	assert.Contains(t, corefile, "172.18.0.3 worker-0.dev.sind.sind")
}

func TestRemoveDNSRecord_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "controller.dev.sind.sind")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading DNS Corefile")
}

func TestRemoveDNSRecord_WriteError(t *testing.T) {
	var m mock.Executor
	m.AddResult(corefileTar(t, []string{"172.18.0.2 x"}), "", nil)
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.RemoveDNSRecord(t.Context(), "x")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writing DNS Corefile")
}

// --- generateCorefile / parseEntries ---

func TestGenerateCorefile_Empty(t *testing.T) {
	cf := generateCorefile(DefaultRealm, nil)
	assert.Contains(t, cf, "sind.sind:53")
	assert.Contains(t, cf, "hosts {")
	assert.Contains(t, cf, "fallthrough")
	assert.Contains(t, cf, "forward . /etc/resolv.conf")
}

func TestGenerateCorefile_WithEntries(t *testing.T) {
	entries := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}
	cf := generateCorefile(DefaultRealm, entries)
	assert.Contains(t, cf, "        172.18.0.2 controller.dev.sind.sind\n")
	assert.Contains(t, cf, "        172.18.0.3 worker-0.dev.sind.sind\n")
}

func TestGenerateCorefile_CustomRealm(t *testing.T) {
	cf := generateCorefile("ci42", nil)
	assert.Contains(t, cf, "ci42.sind:53")
}

func TestParseEntries_Empty(t *testing.T) {
	cf := generateCorefile(DefaultRealm, nil)
	entries := parseEntries(cf)
	assert.Empty(t, entries)
}

func TestParseEntries_Roundtrip(t *testing.T) {
	original := []string{
		"172.18.0.2 controller.dev.sind.sind",
		"172.18.0.3 worker-0.dev.sind.sind",
	}
	cf := generateCorefile(DefaultRealm, original)
	parsed := parseEntries(cf)
	assert.Equal(t, original, parsed)
}

func TestParseEntries_NoHostsBlock(t *testing.T) {
	entries := parseEntries(".:53 {\n    forward . /etc/resolv.conf\n}\n")
	assert.Empty(t, entries)
}

// --- Custom Realm ---

func TestCustomRealm_ResourceNames(t *testing.T) {
	c := docker.NewClient(&mock.Executor{})
	mgr := NewManager(c, "testrealm")

	assert.Equal(t, docker.NetworkName("testrealm-mesh"), mgr.NetworkName())
	assert.Equal(t, docker.ContainerName("testrealm-dns"), mgr.DNSContainerName())
	assert.Equal(t, docker.ContainerName("testrealm-ssh"), mgr.SSHContainerName())
	assert.Equal(t, docker.VolumeName("testrealm-ssh-config"), mgr.SSHVolumeName())
	assert.Equal(t, docker.ContainerName("testrealm-ssh-keygen"), mgr.SSHKeygenName())
	assert.Equal(t, "testrealm-mesh", mgr.ComposeProject())
}

func TestCustomRealm_DefaultProducesStandardNames(t *testing.T) {
	c := docker.NewClient(&mock.Executor{})
	mgr := NewManager(c, DefaultRealm)

	assert.Equal(t, NetworkName, mgr.NetworkName())
	assert.Equal(t, DNSContainerName, mgr.DNSContainerName())
	assert.Equal(t, SSHContainerName, mgr.SSHContainerName())
	assert.Equal(t, SSHVolumeName, mgr.SSHVolumeName())
}

// --- CleanupMesh ---

func TestCleanupMesh(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)

	assert.Empty(t, f.containers)
	assert.Empty(t, f.networks)
	assert.Empty(t, f.volumes)

	// The containers go first (in any order), then the network, then the
	// volume.
	require.Len(t, m.Calls, 5)
	var removed []string
	for _, call := range m.Calls[:3] {
		removed = append(removed, strings.Join(call.Args, " "))
	}
	assert.ElementsMatch(t, []string{
		"rm -f -v sind-ssh-keygen", "rm -f -v sind-ssh", "rm -f -v sind-dns",
	}, removed)
	assert.Equal(t, []string{"network", "rm", string(NetworkName)}, m.Calls[3].Args)
	assert.Equal(t, []string{"volume", "rm", string(SSHVolumeName)}, m.Calls[4].Args)
}

func TestCleanupMesh_NoneExist(t *testing.T) {
	_, c, m := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)
	assert.Len(t, m.Calls, 5)
}

func TestCleanupMesh_Errors(t *testing.T) {
	tests := []struct {
		fail  string
		match string
	}{
		{"rm -f -v sind-ssh-keygen", "removing SSH keygen container"},
		{"rm -f -v sind-ssh", "removing SSH container"},
		{"rm -f -v sind-dns", "removing DNS container"},
		{"network rm", "removing mesh network"},
		{"volume rm", "removing SSH volume"},
	}
	for _, tt := range tests {
		t.Run(tt.fail, func(t *testing.T) {
			f, c, _ := newFakeDocker(t)
			f.withMesh(DefaultRealm, docker.StateRunning)
			f.fail[tt.fail] = failure()
			mgr := NewManager(c, DefaultRealm)

			err := mgr.CleanupMesh(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

// --- Custom Realm ---

func TestCustomRealm_EnsureMeshNetwork(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchNetwork("sind-mesh"), testutil.ExitCode1(t)) // NetworkExists → no
	m.AddResult("net-id\n", "", nil)                                            // CreateNetwork
	c := docker.NewClient(&m)
	mgr := NewManager(c, "myrealm")

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, []string{"network", "inspect", "myrealm-mesh"}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"network", "create",
		"--label", "com.docker.compose.network=mesh",
		"--label", "com.docker.compose.project=myrealm-mesh",
		"--label", "sind.realm=myrealm",
		"myrealm-mesh",
	}, m.Calls[1].Args)
}

func TestCustomRealm_EnsureMesh(t *testing.T) {
	f, c, m := newFakeDocker(t)
	mgr := NewManager(c, "myrealm")

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	assert.True(t, f.networks["myrealm-mesh"])
	assert.True(t, f.volumes["myrealm-ssh-config"])
	require.Contains(t, f.containers, "myrealm-dns")
	require.Contains(t, f.containers, "myrealm-ssh")
	assert.Equal(t, []string{"myrealm-mesh"}, f.containers["myrealm-dns"].networks)
	assert.Equal(t, []string{"myrealm-mesh"}, f.containers["myrealm-ssh"].networks)
	assert.Contains(t, f.containers["myrealm-dns"].files[corefilePath], "myrealm.sind:53")

	assert.Equal(t, []string{
		"volume", "create",
		"--label", "com.docker.compose.project=myrealm-mesh",
		"--label", "com.docker.compose.volume=ssh-config",
		"--label", "sind.realm=myrealm",
		"myrealm-ssh-config",
	}, m.Calls[indexOf(t, m, "volume create")].Args)
	relay := m.Calls[indexOf(t, m, "create --name myrealm-ssh")].Args
	vol, _ := testutil.ArgValue(relay, "-v")
	assert.Equal(t, "myrealm-ssh-config:/root/.ssh", vol)
	assert.Equal(t, []string{SSHImage, "sleep", "infinity"}, relay[len(relay)-3:])
}

func TestCustomRealm_CleanupMesh(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh("myrealm", docker.StateRunning)
	mgr := NewManager(c, "myrealm")

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)

	assert.Empty(t, f.containers)
	assert.Len(t, calls(m, "rm -f -v myrealm-ssh-keygen"), 1)
	assert.Equal(t, []string{"network rm myrealm-mesh"}, calls(m, "network rm"))
	assert.Equal(t, []string{"volume rm myrealm-ssh-config"}, calls(m, "volume rm"))
}

// indexOf returns the index of the first recorded call that starts with
// prefix.
func indexOf(t *testing.T, m *mock.Executor, prefix string) int {
	t.Helper()
	for i, c := range m.Calls {
		if strings.HasPrefix(strings.Join(c.Args, " "), prefix) {
			return i
		}
	}
	require.Failf(t, "call not found", "%q", prefix)
	return -1
}

// dnsInspectJSON returns a mock docker inspect result for the DNS container on the mesh network.
func dnsInspectJSON() string {
	return `[{"Id":"dns123","Name":"/sind-dns","State":{"Status":"running"},"Config":{"Image":"` + DNSImage + `","Labels":{}},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"10.0.0.2"}}}}]`
}

// dnsInspectExitedJSON returns a mock docker inspect result for a stopped DNS container.
func dnsInspectExitedJSON() string {
	return `[{"Id":"dns123","Name":"/sind-dns","State":{"Status":"exited"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{}}}]`
}

// --- GetInfo ---

func TestGetInfo(t *testing.T) {
	var m mock.Executor
	m.AddResult(dnsInspectJSON(), "", nil) // InspectContainer(DNS)

	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	info, err := mgr.GetInfo(t.Context())
	require.NoError(t, err)
	require.Len(t, m.Calls, 1, "one inspect of the DNS container")
	assert.Equal(t, []string{"inspect", "sind-dns"}, m.Calls[0].Args)
	assert.Equal(t, "sind-mesh", info.Network)
	assert.Equal(t, "sind-dns", info.DNSContainer)
	assert.Equal(t, "10.0.0.2", info.DNSIP)
	assert.Equal(t, "sind.sind", info.DNSZone)
	assert.Equal(t, string(DNSImage), info.DNSImage)
	assert.Equal(t, "sind-ssh", info.SSHContainer)
	assert.Equal(t, "sind-ssh-config", info.SSHVolume)
	assert.Equal(t, SSHImage, info.SSHImage)
}

func TestGetInfo_CustomRealm(t *testing.T) {
	inspectJSON := `[{"Id":"dns1","Name":"/ci-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"ci-mesh":{"IPAddress":"10.1.0.5"}}}}]`
	var m mock.Executor
	m.AddResult(inspectJSON, "", nil)

	c := docker.NewClient(&m)
	mgr := NewManager(c, "ci")

	info, err := mgr.GetInfo(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "ci-mesh", info.Network)
	assert.Equal(t, "ci-dns", info.DNSContainer)
	assert.Equal(t, "10.1.0.5", info.DNSIP)
	assert.Equal(t, "ci.sind", info.DNSZone)
}

func TestGetInfo_NoMesh(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error: No such object: sind-dns\n", &exec.ExitError{ProcessState: exitCode1(t)})

	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetInfo(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mesh found for realm")
	assert.Contains(t, err.Error(), DefaultRealm)
}

func TestGetInfo_InspectError(t *testing.T) {
	// An inspect failing for another reason than a missing container (e.g.
	// daemon unreachable) is not taken for a missing mesh.
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("docker daemon unreachable"))

	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetInfo(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspecting DNS container: docker daemon unreachable")
	assert.NotContains(t, err.Error(), "no mesh found")
}

// --- GetDNSRecords ---

func TestGetDNSRecords(t *testing.T) {
	var m mock.Executor
	entries := []string{"172.18.0.2 controller.dev.sind.sind", "172.18.0.3 worker-0.dev.sind.sind"}
	m.AddResult("[{}]\n", "", nil) // DNS container exists
	m.AddResult(corefileTar(t, entries), "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	records, err := mgr.GetDNSRecords(t.Context())
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "172.18.0.2", records[0].IP)
	assert.Equal(t, "controller.dev.sind.sind", records[0].Hostname)
	assert.Equal(t, "172.18.0.3", records[1].IP)
	assert.Equal(t, "worker-0.dev.sind.sind", records[1].Hostname)
}

func TestGetDNSRecords_Empty(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // DNS container exists
	m.AddResult(corefileTar(t, nil), "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	records, err := mgr.GetDNSRecords(t.Context())
	require.NoError(t, err)
	assert.NotNil(t, records, "empty result must be [] not nil so json emits []")
	assert.Empty(t, records)
}

func TestGetDNSRecords_ReadError(t *testing.T) {
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // DNS container exists
	m.AddResult("", "Error\n", fmt.Errorf("container not found"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetDNSRecords(t.Context())
	assert.Error(t, err)
}

// TestGetDNSRecords_NoMesh covers the typo/empty-realm case: DNS container is
// absent so GetDNSRecords must surface a clean "no mesh found" error instead
// of leaking the raw docker exec failure.
func TestGetDNSRecords_NoMesh(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchContainer("sind-dns"), &exec.ExitError{ProcessState: exitCode1(t)}) // DNS container missing
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetDNSRecords(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no mesh found for realm "sind"`)
	assert.NotContains(t, err.Error(), "exit status")
}

// TestGetDNSRecords_CheckError covers a DNS container check that fails for
// another reason than a missing container (e.g. daemon unreachable): it
// names the container instead of claiming there is no mesh.
func TestGetDNSRecords_CheckError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("docker daemon unreachable"))
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetDNSRecords(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking sind-dns: docker daemon unreachable")
}

// --- HostDNS branches ---

func TestEnsureMesh_HostDNS_Skipped(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)

	var sys mock.Executor
	sys.AddResult("", "", fmt.Errorf("inactive")) // systemctl → not active

	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
}

func TestEnsureMesh_HostDNS_ConfigureFails(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)

	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl ok
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	sys.AddResult("", "", fmt.Errorf("denied")) // resolvectl dns fails

	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	// Error is logged, not returned (best-effort).
	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
	assert.Len(t, sys.Calls, 5)
}

func TestEnsureMesh_HostDNS_Success(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)

	var sys mock.Executor
	sys.AddResult("", "", nil) // systemctl ok
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil) // resolvectl dns
	sys.AddResult("", "", nil) // resolvectl domain

	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)
	require.Len(t, sys.Calls, 6)
	assert.Equal(t, []string{"dns", "br-abcdef012345", "10.0.0.2"}, sys.Calls[4].Args)
}

func TestCleanupMesh_HostDNS(t *testing.T) {
	_, c, _ := newFakeDocker(t)
	var sys mock.Executor
	sys.AddResult("", "", fmt.Errorf("inactive")) // systemctl → not active (revertHostDNS skips)

	mgr := NewManager(c, DefaultRealm)
	mgr.HostDNS = true
	mgr.Exec = &sys

	err := mgr.CleanupMesh(t.Context())
	require.NoError(t, err)
}

// --- test helpers ---

// corefileTar builds a tar archive containing a Corefile with the given entries,
// matching the format returned by docker cp.
func corefileTar(t *testing.T, entries []string) string {
	t.Helper()
	content := []byte(generateCorefile(DefaultRealm, entries))
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "Corefile", Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write(content)
	_ = tw.Close()
	return buf.String()
}

// extractTarFile reads a named file from a tar archive captured by MockExecutor.
func extractTarFile(t *testing.T, tarData, name string) string {
	t.Helper()
	tr := tar.NewReader(strings.NewReader(tarData))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			require.Failf(t, "file not found in tar", "%q", name)
		}
		require.NoError(t, err)
		if hdr.Name == name {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			return string(data)
		}
	}
}
