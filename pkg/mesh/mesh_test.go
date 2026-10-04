// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/netip"
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
	assert.False(t, mgr.Created(), "second EnsureMesh created nothing")

	// The info reports the images the containers run.
	info, err := mgr.GetInfo(ctx)
	require.NoError(t, err)
	assert.Equal(t, DNSImage, info.DNSImage)
	assert.Equal(t, SSHImage(), info.SSHImage)
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

// TestMeshPinnedDNSAddress checks that a new mesh pins the DNS container's
// address outside the range Docker hands out addresses from, so that after
// a host reboot the DNS container gets it back even when another container
// on the mesh, here the relay, starts first.
func TestMeshPinnedDNSAddress(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx := t.Context()
	mgr := NewManager(c, testutil.Realm("it-pin"))
	got := warnings(mgr)

	if !rec.IsIntegration() {
		rec.SetOnCall(newFake(t).onCall)
	}
	t.Cleanup(func() { _ = mgr.CleanupMesh(context.Background()) })

	require.NoError(t, mgr.EnsureMesh(ctx))

	mesh, err := c.InspectNetwork(ctx, mgr.NetworkName())
	require.NoError(t, err)
	pinned := mesh.Labels[LabelDNSIP]
	require.NotEmpty(t, pinned, "the mesh network pins no DNS address: %+v", mesh)
	subnet, err := netip.ParsePrefix(mesh.Subnet)
	require.NoError(t, err)
	ipRange, err := netip.ParsePrefix(mesh.IPRange)
	require.NoError(t, err)
	addr, err := netip.ParseAddr(pinned)
	require.NoError(t, err)
	assert.True(t, subnet.Contains(addr), "%s in %s", addr, subnet)
	assert.False(t, ipRange.Contains(addr), "%s outside %s", addr, ipRange)

	dns, err := c.InspectContainer(ctx, mgr.DNSContainerName())
	require.NoError(t, err)
	assert.Equal(t, pinned, dns.IPs[mgr.NetworkName()])
	relay, err := c.InspectContainer(ctx, mgr.SSHContainerName())
	require.NoError(t, err)
	assert.Equal(t, []string{pinned}, relay.DNS)
	relayAddr, err := netip.ParseAddr(relay.IPs[mgr.NetworkName()])
	require.NoError(t, err)
	assert.True(t, ipRange.Contains(relayAddr), "relay %s in %s", relayAddr, ipRange)

	// After a host reboot, the relay starts before the DNS container.
	require.NoError(t, c.StopContainer(ctx, mgr.SSHContainerName()))
	require.NoError(t, c.StopContainer(ctx, mgr.DNSContainerName()))
	require.NoError(t, c.StartContainer(ctx, mgr.SSHContainerName()))

	dnsIP, found, err := mgr.StartMesh(ctx)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, pinned, dnsIP, "DNS got its pinned address back")
	assert.Empty(t, *got, "the relay still resolves through the DNS container")

	info, err := mgr.GetInfo(ctx)
	require.NoError(t, err)
	assert.Equal(t, pinned, info.DNSIP)

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

	assert.True(t, f.volumes["sind-ssh-config"])

	// The mesh network pins the DNS address outside its ip-range.
	mesh := f.networks["sind-mesh"]
	require.NotNil(t, mesh)
	assert.Equal(t, "10.0.0.0/16", mesh.subnet.String())
	assert.Equal(t, "10.0.0.0/17", mesh.ipRange.String())
	assert.Equal(t, "10.0.255.254", mesh.labels[LabelDNSIP])

	dns := f.containers["sind-dns"]
	require.NotNil(t, dns)
	assert.Equal(t, docker.StateRunning, dns.state)
	assert.Equal(t, DNSImage, dns.image)
	assert.Equal(t, "10.0.255.254", dns.ips["sind-mesh"], "DNS has the pinned address")
	assert.Contains(t, dns.files[corefilePath], "hosts {")

	// The relay resolves through the DNS container and got the keys
	// before it started; no helper container is involved.
	relay := f.containers["sind-ssh"]
	require.NotNil(t, relay)
	assert.Equal(t, docker.StateRunning, relay.state)
	assert.Equal(t, SSHImage(), relay.image)
	assert.Equal(t, "10.0.0.2", relay.ips["sind-mesh"], "relay has an address of the ip-range")
	assert.Equal(t, []string{"10.0.255.254"}, relay.dns)
	assert.Contains(t, relay.files["/root/.ssh/id_ed25519"], "OPENSSH PRIVATE KEY")
	assert.True(t, strings.HasPrefix(relay.files["/root/.ssh/id_ed25519.pub"], "ssh-ed25519 "))
	assert.Contains(t, relay.files, "/root/.ssh/known_hosts")
	assert.Empty(t, calls(m, "create --name sind-ssh-keygen"))

	// Docker chose the subnet of a network without one, which sind
	// created again with that subnet pinned.
	assert.Equal(t, []string{
		"network inspect sind-mesh --format {{json .Labels}}",
		"network create --label com.docker.compose.network=mesh --label com.docker.compose.project=sind-mesh --label sind.realm=sind sind-mesh",
		"network inspect sind-mesh",
		"network rm sind-mesh",
		"network create --subnet 10.0.0.0/16 --gateway 10.0.0.1 --ip-range 10.0.0.0/17 " +
			"--label com.docker.compose.network=mesh --label com.docker.compose.project=sind-mesh --label sind.dns.ip=10.0.255.254 --label sind.realm=sind sind-mesh",
	}, calls(m, "network"))
	dnsCreate := calls(m, "create --name sind-dns")
	require.Len(t, dnsCreate, 1)
	assert.Contains(t, dnsCreate[0], "--network sind-mesh --ip 10.0.255.254 ")

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
	require.NoError(t, f.start("sind-dev-controller"))
	mgr := NewManager(c, DefaultRealm)
	got := warnings(mgr)

	err := mgr.EnsureMesh(t.Context())
	require.NoError(t, err)

	require.Len(t, *got, 1)
	assert.Contains(t, (*got)[0], "mesh DNS sind-dns now has the address 10.0.0.3")
	assert.Contains(t, (*got)[0], "containers created before still use 10.0.0.2 (sind-ssh)")
	assert.Contains(t, (*got)[0], "the SSH relay is re-created")
}

// TestEnsureMesh_PinnedDNSKeepsAddress covers a mesh with a pinned DNS
// address after a host reboot, when a node and the relay start before the
// DNS container: it still gets its address back.
func TestEnsureMesh_PinnedDNSKeepsAddress(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withPinnedMesh(DefaultRealm, docker.StateExited)
	f.containers["sind-dev-controller"] = &fakeContainer{state: docker.StateCreated, networks: []string{"sind-mesh"}}
	require.NoError(t, f.start("sind-dev-controller"))
	require.NoError(t, f.start("sind-ssh"))
	mgr := NewManager(c, DefaultRealm)
	got := warnings(mgr)

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	assert.Equal(t, "10.0.255.254", f.containers["sind-dns"].ips["sind-mesh"])
	assert.Equal(t, "10.0.0.2", f.containers["sind-dev-controller"].ips["sind-mesh"])
	assert.Empty(t, *got, "the relay resolves through the DNS address it was created with")
}

// TestEnsureMesh_PinnedDNSRecreated covers a DNS container removed by hand
// from a mesh with a pinned DNS address: the new one gets that address.
func TestEnsureMesh_PinnedDNSRecreated(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withPinnedMesh(DefaultRealm, docker.StateRunning)
	f.stop("sind-dns", docker.StateExited)
	delete(f.containers, "sind-dns")
	mgr := NewManager(c, DefaultRealm)

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	assert.False(t, mgr.Created())
	require.Len(t, calls(m, "create --name sind-dns"), 1)
	assert.Contains(t, calls(m, "create --name sind-dns")[0], "--ip 10.0.255.254")
	assert.Equal(t, "10.0.255.254", f.containers["sind-dns"].ips["sind-mesh"])
	assert.Empty(t, calls(m, "network create"))
}

// TestEnsureMesh_UnpinnedDNSRecreated covers a DNS container removed by hand
// from a mesh of an earlier sind version: the new one gets an address from
// Docker.
func TestEnsureMesh_UnpinnedDNSRecreated(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	f.stop("sind-dns", docker.StateExited)
	delete(f.containers, "sind-dns")
	mgr := NewManager(c, DefaultRealm)

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	require.Len(t, calls(m, "create --name sind-dns"), 1)
	assert.NotContains(t, calls(m, "create --name sind-dns")[0], "--ip")
	assert.Equal(t, "10.0.0.2", f.containers["sind-dns"].ips["sind-mesh"])
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
		case joined == "network inspect sind-mesh --format {{json .Labels}}":
			return mock.Result{Stdout: "{}\n"}
		case joined == "volume inspect sind-ssh-config":
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
	// NetworkLabels → not found (exit code 1)
	m.AddResult("", "Error: No such network: sind-mesh\n",
		testutil.ExitCode1(t))
	// CreateNetwork → success, InspectNetwork → the subnet Docker chose
	m.AddResult(networkID+"\n", "", nil)
	m.AddResult(`[{"Name":"sind-mesh","IPAM":{"Config":[{"Subnet":"192.168.16.0/20","Gateway":"192.168.16.1"}]}}]`, "", nil)
	// RemoveNetwork, CreateNetworkWithSubnet → success
	m.AddResult("sind-mesh\n", "", nil)
	m.AddResult(networkID+"\n", "", nil)
	c := docker.NewClient(&m)
	mgr := NewManager(c, DefaultRealm)

	assert.False(t, mgr.Created(), "Created() before EnsureMeshNetwork")
	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.True(t, mgr.Created(), "Created() after creating mesh network")

	require.Len(t, m.Calls, 5)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName), "--format", "{{json .Labels}}"}, m.Calls[0].Args)
	assert.Equal(t, []string{
		"network", "create",
		"--label", "com.docker.compose.network=mesh",
		"--label", "com.docker.compose.project=sind-mesh",
		"--label", "sind.realm=" + DefaultRealm,
		string(NetworkName),
	}, m.Calls[1].Args)
	assert.Equal(t, []string{"network", "inspect", string(NetworkName)}, m.Calls[2].Args)
	assert.Equal(t, []string{"network", "rm", string(NetworkName)}, m.Calls[3].Args)
	assert.Equal(t, []string{
		"network", "create",
		"--subnet", "192.168.16.0/20", "--gateway", "192.168.16.1", "--ip-range", "192.168.16.0/21",
		"--label", "com.docker.compose.network=mesh",
		"--label", "com.docker.compose.project=sind-mesh",
		"--label", "sind.dns.ip=192.168.31.254",
		"--label", "sind.realm=" + DefaultRealm,
		string(NetworkName),
	}, m.Calls[4].Args)
}

// TestEnsureMeshNetwork_SmallSubnet covers a daemon whose address pools give
// networks too small to pin the DNS address: the network stays as Docker
// created it.
func TestEnsureMeshNetwork_SmallSubnet(t *testing.T) {
	var m mock.Executor
	m.AddResult("", testutil.NoSuchNetwork("sind-mesh"), testutil.ExitCode1(t))
	m.AddResult("net-id\n", "", nil)
	m.AddResult(`[{"Name":"sind-mesh","IPAM":{"Config":[{"Subnet":"10.200.3.0/24","Gateway":"10.200.3.1"}]}}]`, "", nil)
	mgr := NewManager(docker.NewClient(&m), DefaultRealm)

	pinned, err := mgr.ensureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.Empty(t, pinned)
	assert.True(t, mgr.Created())
	assert.Len(t, m.Calls, 3, "no re-create")
}

// TestEnsureMeshNetwork_SubnetTaken covers another network that takes the
// mesh's subnet between its removal and its re-creation: sind starts over
// with the subnet Docker picks next.
func TestEnsureMeshNetwork_SubnetTaken(t *testing.T) {
	for _, msg := range []string{
		"invalid pool request: Pool overlaps with other one on this address space",
		"cannot create network 0123 (br-0123): conflicts with network 4567 (br-4567): networks have overlapping IPv4",
		"failed to allocate gateway (10.0.0.1): Address already in use",
	} {
		t.Run(msg, func(t *testing.T) {
			f, c, m := newFakeDocker(t)
			taken := false
			m.OnCall = func(args []string, stdin string) mock.Result {
				joined := strings.Join(args, " ")
				if !taken && strings.HasPrefix(joined, "network create --subnet 10.0.0.0/16 ") {
					taken = true
					f.addNetwork("other")
					return mock.Result{Stderr: "Error response from daemon: " + msg + "\n", Err: testutil.ExitCode1(t)}
				}
				return f.onCall(args, stdin)
			}
			mgr := NewManager(c, DefaultRealm)

			pinned, err := mgr.ensureMeshNetwork(t.Context())
			require.NoError(t, err)
			assert.True(t, mgr.Created())
			assert.Equal(t, "10.1.255.254", pinned)
			mesh := f.networks["sind-mesh"]
			require.NotNil(t, mesh)
			assert.Equal(t, "10.1.0.0/16", mesh.subnet.String())
			assert.Equal(t, "10.1.255.254", mesh.labels[LabelDNSIP])
			assert.Len(t, calls(m, "network rm sind-mesh"), 2)
		})
	}
}

// TestEnsureMeshNetwork_SubnetNeverFree covers a subnet that is taken on
// every attempt: sind keeps a network without a pinned DNS address.
func TestEnsureMeshNetwork_SubnetNeverFree(t *testing.T) {
	f, c, m := newFakeDocker(t)
	f.fail["network create --subnet"] = mock.Result{
		Stderr: "Error response from daemon: invalid pool request: Pool overlaps with other one on this address space\n",
		Err:    testutil.ExitCode1(t),
	}
	mgr := NewManager(c, DefaultRealm)

	pinned, err := mgr.ensureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.Empty(t, pinned)
	assert.True(t, mgr.Created())
	require.Contains(t, f.networks, "sind-mesh")
	assert.NotContains(t, f.networks["sind-mesh"].labels, LabelDNSIP)
	assert.Len(t, calls(m, "network create --subnet"), meshSubnetAttempts)
	assert.Len(t, calls(m, "network rm"), meshSubnetAttempts)
}

// TestEnsureMeshNetwork_PinErrors covers the failures after the network
// without a subnet exists: sind reports them, and Created tells whether a
// network is left to remove.
func TestEnsureMeshNetwork_PinErrors(t *testing.T) {
	tests := []struct {
		name    string
		fail    string
		match   string
		created bool
	}{
		{"inspect", "network inspect sind-mesh", "inspecting mesh network", true},
		{"remove", "network rm", "removing mesh network to pin its subnet", true},
		{"re-create", "network create --subnet", "creating mesh network", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c, m := newFakeDocker(t)
			m.OnCall = func(args []string, stdin string) mock.Result {
				joined := strings.Join(args, " ")
				if joined == tt.fail || strings.HasPrefix(joined, tt.fail+" ") && !strings.Contains(joined, "--format") {
					return failure()
				}
				return f.onCall(args, stdin)
			}
			mgr := NewManager(c, DefaultRealm)

			_, err := mgr.ensureMeshNetwork(t.Context())
			require.ErrorContains(t, err, tt.match)
			assert.Equal(t, tt.created, mgr.Created())
		})
	}
}

// TestEnsureMeshNetwork_RecreatedConcurrently covers another client of the
// same daemon creating the network while sind pins its subnet.
func TestEnsureMeshNetwork_RecreatedConcurrently(t *testing.T) {
	f, c, m := newFakeDocker(t)
	m.OnCall = func(args []string, stdin string) mock.Result {
		if strings.HasPrefix(strings.Join(args, " "), "network create --subnet") {
			f.addNetwork("sind-mesh")
		}
		return f.onCall(args, stdin)
	}
	mgr := NewManager(c, DefaultRealm)

	pinned, err := mgr.ensureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.Empty(t, pinned)
	assert.False(t, mgr.Created(), "the other client created it")
}

func TestPinnedLayout(t *testing.T) {
	tests := []struct {
		subnet, gateway string
		ipRange, dns    string // empty: no layout
	}{
		{"172.18.0.0/16", "172.18.0.1", "172.18.0.0/17", "172.18.255.254"},
		{"192.168.16.0/20", "192.168.16.1", "192.168.16.0/21", "192.168.31.254"},
		{"10.200.8.0/21", "10.200.8.1", "10.200.8.0/22", "10.200.15.254"},
		{"10.0.0.0/8", "10.0.0.1", "10.0.0.0/9", "10.255.255.254"},
		{"172.18.5.0/16", "172.18.0.1", "172.18.0.0/17", "172.18.255.254"}, // not masked
		{"10.200.8.0/22", "10.200.8.1", "", ""},                            // range of 510
		{"10.200.3.0/24", "10.200.3.1", "", ""},
		{"10.0.0.0/31", "10.0.0.0", "", ""},
		{"10.0.0.1/32", "10.0.0.1", "", ""},
		{"fd00::/64", "fd00::1", "", ""},
		{"", "", "", ""},
		{"172.18.0.0/16", "", "", ""},
		{"172.18.0.0/16", "172.19.0.1", "", ""},     // gateway outside
		{"172.18.0.0/16", "172.18.255.254", "", ""}, // gateway at the DNS address
	}
	for _, tt := range tests {
		t.Run(tt.subnet+" "+tt.gateway, func(t *testing.T) {
			ipam, dns, ok := pinnedLayout(tt.subnet, tt.gateway)
			if tt.dns == "" {
				assert.False(t, ok)
				return
			}
			require.True(t, ok)
			assert.Equal(t, tt.ipRange, ipam.IPRange)
			assert.Equal(t, tt.gateway, ipam.Gateway)
			assert.Equal(t, tt.dns, dns)
			ipRange := netip.MustParsePrefix(ipam.IPRange)
			subnet := netip.MustParsePrefix(ipam.Subnet)
			addr := netip.MustParseAddr(dns)
			assert.True(t, subnet.Contains(addr))
			assert.False(t, ipRange.Contains(addr), "the DNS address is outside the range")
			// The range holds the network address, the gateway and every
			// container but the DNS that a bridge network takes.
			assert.GreaterOrEqual(t, 1<<(32-ipRange.Bits()), docker.MaxBridgeEndpoints+1)
		})
	}
}

// TestEnsureMeshNetwork_CreatedPerCall covers a Manager that serves several
// creates: Created answers for the last call only.
func TestEnsureMeshNetwork_CreatedPerCall(t *testing.T) {
	_, c, _ := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)

	require.NoError(t, mgr.EnsureMeshNetwork(t.Context()))
	assert.True(t, mgr.Created())

	require.NoError(t, mgr.EnsureMeshNetwork(t.Context()))
	assert.False(t, mgr.Created(), "the mesh existed before the second call")
}

func TestEnsureMeshNetwork_AlreadyExists(t *testing.T) {
	for _, tt := range []struct {
		labels, pinned string
	}{
		{`{"sind.dns.ip":"172.18.255.254","sind.realm":"sind"}`, "172.18.255.254"},
		{`{"sind.realm":"sind"}`, ""}, // created by an earlier sind version
	} {
		t.Run(tt.labels, func(t *testing.T) {
			var m mock.Executor
			// NetworkLabels → found
			m.AddResult(tt.labels+"\n", "", nil)
			c := docker.NewClient(&m)
			mgr := NewManager(c, DefaultRealm)

			pinned, err := mgr.ensureMeshNetwork(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.pinned, pinned)
			assert.False(t, mgr.Created(), "Created() when mesh already existed")

			// Only inspect, no create
			require.Len(t, m.Calls, 1)
			assert.Equal(t, []string{"network", "inspect", string(NetworkName), "--format", "{{json .Labels}}"}, m.Calls[0].Args)
		})
	}
}

// TestEnsureMeshNetwork_CreatedConcurrently covers another client of the
// same daemon creating the network between the check and the create.
func TestEnsureMeshNetwork_CreatedConcurrently(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.addNetwork("sind-mesh")
	f.fail["network inspect"] = notFound(t, testutil.NoSuchNetwork("sind-mesh"))
	mgr := NewManager(c, DefaultRealm)

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)
	assert.False(t, mgr.Created(), "the other client created it")
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
	assert.False(t, mgr.Created(), "no network was created")
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
	f, c, m := newFakeDocker(t)
	mgr := NewManager(c, "myrealm")

	err := mgr.EnsureMeshNetwork(t.Context())
	require.NoError(t, err)

	assert.Equal(t, []string{
		"network inspect myrealm-mesh --format {{json .Labels}}",
		"network create --label com.docker.compose.network=mesh --label com.docker.compose.project=myrealm-mesh --label sind.realm=myrealm myrealm-mesh",
		"network inspect myrealm-mesh",
		"network rm myrealm-mesh",
		"network create --subnet 10.0.0.0/16 --gateway 10.0.0.1 --ip-range 10.0.0.0/17 " +
			"--label com.docker.compose.network=mesh --label com.docker.compose.project=myrealm-mesh --label sind.dns.ip=10.0.255.254 --label sind.realm=myrealm myrealm-mesh",
	}, calls(m, "network"))
	assert.Equal(t, map[string]string{
		"com.docker.compose.network": "mesh",
		"com.docker.compose.project": "myrealm-mesh",
		LabelDNSIP:                   "10.0.255.254",
		LabelRealm:                   "myrealm",
	}, f.networks["myrealm-mesh"].labels)
}

func TestCustomRealm_EnsureMesh(t *testing.T) {
	f, c, m := newFakeDocker(t)
	mgr := NewManager(c, "myrealm")

	require.NoError(t, mgr.EnsureMesh(t.Context()))

	assert.Contains(t, f.networks, "myrealm-mesh")
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
	assert.Equal(t, []string{SSHImage(), "sleep", "infinity"}, relay[len(relay)-3:])
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
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateRunning)
	// A relay made by an older sind runs another image.
	f.containers["sind-ssh"].image = "ghcr.io/gsi-hpc/sind-node:latest-old"
	mgr := NewManager(c, DefaultRealm)

	info, err := mgr.GetInfo(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "sind-mesh", info.Network)
	assert.Equal(t, "sind-dns", info.DNSContainer)
	assert.Equal(t, "10.0.0.2", info.DNSIP)
	assert.Equal(t, "sind.sind", info.DNSZone)
	assert.Equal(t, DNSImage, info.DNSImage)
	assert.Equal(t, "sind-ssh", info.SSHContainer)
	assert.Equal(t, "sind-ssh-config", info.SSHVolume)
	assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:latest-old", info.SSHImage, "the image the relay runs")
}

func TestGetInfo_CustomRealm(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh("ci", docker.StateRunning)
	mgr := NewManager(c, "ci")

	info, err := mgr.GetInfo(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "ci-mesh", info.Network)
	assert.Equal(t, "ci-dns", info.DNSContainer)
	assert.Equal(t, "10.0.0.2", info.DNSIP)
	assert.Equal(t, "ci.sind", info.DNSZone)
}

func TestGetInfo_Stopped(t *testing.T) {
	f, c, _ := newFakeDocker(t)
	f.withMesh(DefaultRealm, docker.StateExited)
	delete(f.containers, "sind-ssh")
	mgr := NewManager(c, DefaultRealm)

	info, err := mgr.GetInfo(t.Context())
	require.NoError(t, err)
	assert.Empty(t, info.DNSIP, "a stopped DNS container has no address")
	assert.Empty(t, info.SSHImage, "no relay")
}

func TestGetInfo_NoMesh(t *testing.T) {
	_, c, _ := newFakeDocker(t)
	mgr := NewManager(c, DefaultRealm)

	_, err := mgr.GetInfo(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mesh found for realm")
	assert.Contains(t, err.Error(), DefaultRealm)
}

func TestGetInfo_InspectErrors(t *testing.T) {
	for _, tt := range []struct{ fail, match string }{
		{"inspect sind-dns", "inspecting DNS container"},
		{"inspect sind-ssh", "inspecting SSH container"},
	} {
		t.Run(tt.fail, func(t *testing.T) {
			f, c, _ := newFakeDocker(t)
			f.withMesh(DefaultRealm, docker.StateRunning)
			f.fail[tt.fail] = failure()
			mgr := NewManager(c, DefaultRealm)

			_, err := mgr.GetInfo(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
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

// TestGetDNSRecords_CheckError covers a DNS container check that fails for
// another reason than a missing container (daemon unreachable): the error
// names the container.
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
