// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveTestManager returns a Manager with both Docker and system executors
// set to mocks. The Docker mock is returned for queuing Docker responses.
func resolveTestManager(sysExec *cmdexec.MockExecutor) (*Manager, *cmdexec.MockExecutor) {
	var dockerMock cmdexec.MockExecutor
	mgr := &Manager{
		Docker: docker.NewClient(&dockerMock),
		Exec:   sysExec,
		Realm:  DefaultRealm,
	}
	return mgr, &dockerMock
}

// --- resolvedActive ---

func TestResolvedActive_True(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	mgr := &Manager{Exec: &m}

	assert.True(t, mgr.resolvedActive(t.Context()))
	require.Len(t, m.Calls, 1)
	assert.Equal(t, "systemctl", m.Calls[0].Name)
	assert.Equal(t, []string{"is-active", "--quiet", "systemd-resolved"}, m.Calls[0].Args)
}

func TestResolvedActive_False(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("inactive"))
	mgr := &Manager{Exec: &m}

	assert.False(t, mgr.resolvedActive(t.Context()))
}

// --- polkitAuthorized ---

func TestPolkitAuthorized_True(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	mgr := &Manager{Exec: &m}

	assert.True(t, mgr.polkitAuthorized(t.Context(), "org.freedesktop.resolve1.set-dns-servers"))
	require.Len(t, m.Calls, 1)
	assert.Equal(t, "pkcheck", m.Calls[0].Name)
	assert.Contains(t, m.Calls[0].Args, "org.freedesktop.resolve1.set-dns-servers")
}

func TestPolkitAuthorized_False(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("not authorized"))
	mgr := &Manager{Exec: &m}

	assert.False(t, mgr.polkitAuthorized(t.Context(), "org.freedesktop.resolve1.set-dns-servers"))
}

// --- dnsPolkitAuthorized ---

func TestDnsPolkitAuthorized_AllPass(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // set-dns-servers
	m.AddResult("", "", nil) // set-domains
	m.AddResult("", "", nil) // revert
	mgr := &Manager{Exec: &m}

	assert.True(t, mgr.dnsPolkitAuthorized(t.Context()))
	require.Len(t, m.Calls, 3)
}

func TestDnsPolkitAuthorized_SecondFails(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)                       // set-dns-servers: ok
	m.AddResult("", "", fmt.Errorf("not allowed")) // set-domains: fail
	mgr := &Manager{Exec: &m}

	assert.False(t, mgr.dnsPolkitAuthorized(t.Context()))
	require.Len(t, m.Calls, 2) // short-circuits after second
}

// --- findBridgeInterface ---

func TestFindBridgeInterface(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })

	networkID := "abcdef012345extra"
	bridgeName := "br-abcdef012345"
	require.NoError(t, os.Mkdir(filepath.Join(dir, bridgeName), 0o755))

	iface, err := findBridgeInterface(networkID)
	require.NoError(t, err)
	assert.Equal(t, bridgeName, iface)
}

func TestFindBridgeInterface_NotFound(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })

	_, err := findBridgeInterface("abcdef012345extra")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFindBridgeInterface_IDTooShort(t *testing.T) {
	_, err := findBridgeInterface("short")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

// --- configureDNS ---

func TestConfigureDNS(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // resolvectl dns
	m.AddResult("", "", nil) // resolvectl domain
	mgr := &Manager{Exec: &m, Realm: DefaultRealm}

	err := mgr.configureDNS(t.Context(), "br-abcdef012345", "172.18.0.2")
	require.NoError(t, err)

	require.Len(t, m.Calls, 2)
	assert.Equal(t, "resolvectl", m.Calls[0].Name)
	assert.Equal(t, []string{"dns", "br-abcdef012345", "172.18.0.2"}, m.Calls[0].Args)
	assert.Equal(t, "resolvectl", m.Calls[1].Name)
	assert.Equal(t, []string{"domain", "br-abcdef012345", "~sind.sind"}, m.Calls[1].Args)
}

func TestConfigureDNS_DnsError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("permission denied"))
	mgr := &Manager{Exec: &m, Realm: DefaultRealm}

	err := mgr.configureDNS(t.Context(), "br-abc", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "setting DNS server")
}

func TestConfigureDNS_DomainError(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)                       // dns ok
	m.AddResult("", "", fmt.Errorf("link failed")) // domain fails
	mgr := &Manager{Exec: &m, Realm: DefaultRealm}

	err := mgr.configureDNS(t.Context(), "br-abc", "172.18.0.2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "setting DNS domains")
}

func TestConfigureDNS_CustomRealm(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil) // resolvectl dns
	m.AddResult("", "", nil) // resolvectl domain
	mgr := &Manager{Exec: &m, Realm: "myrealm"}

	err := mgr.configureDNS(t.Context(), "br-abc", "172.18.0.2")
	require.NoError(t, err)
	assert.Equal(t, []string{"domain", "br-abc", "~myrealm.sind"}, m.Calls[1].Args)
}

// --- revertDNS ---

func TestRevertDNS(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", nil)
	mgr := &Manager{Exec: &m}

	err := mgr.revertDNS(t.Context(), "br-abcdef012345")
	require.NoError(t, err)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, "resolvectl", m.Calls[0].Name)
	assert.Equal(t, []string{"revert", "br-abcdef012345"}, m.Calls[0].Args)
}

func TestRevertDNS_Error(t *testing.T) {
	var m cmdexec.MockExecutor
	m.AddResult("", "", fmt.Errorf("no such link"))
	mgr := &Manager{Exec: &m}

	err := mgr.revertDNS(t.Context(), "br-gone")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reverting DNS")
}

// --- configureHostDNS ---

const inspectNetworkJSON = `[{"Name":"sind-mesh","Driver":"bridge","ID":"abcdef012345abcdef012345abcdef012345abcdef012345abcdef012345abcdef01","IPAM":{"Config":[{"Subnet":"172.18.0.0/16","Gateway":"172.18.0.1"}]}}]`

const inspectDNSContainerJSON = `[{"Name":"sind-dns","State":{"Status":"running","Running":true},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"172.18.0.2"}}}}]`

func TestConfigureHostDNS_Success(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil) // systemctl is-active
	sys.AddResult("", "", nil) // pkcheck set-dns-servers
	sys.AddResult("", "", nil) // pkcheck set-domains
	sys.AddResult("", "", nil) // pkcheck revert
	sys.AddResult("", "", nil) // resolvectl dns
	sys.AddResult("", "", nil) // resolvectl domain

	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult(inspectNetworkJSON, "", nil)      // InspectNetwork
	dockerMock.AddResult(inspectDNSContainerJSON, "", nil) // InspectContainer

	ok, err := mgr.configureHostDNS(t.Context())
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestConfigureHostDNS_ResolvedNotActive(t *testing.T) {
	var sys cmdexec.MockExecutor
	sys.AddResult("", "", fmt.Errorf("inactive")) // systemctl fails
	mgr, _ := resolveTestManager(&sys)

	ok, err := mgr.configureHostDNS(t.Context())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestConfigureHostDNS_PolkitDenied(t *testing.T) {
	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil)                          // systemctl ok
	sys.AddResult("", "", fmt.Errorf("not authorized")) // pkcheck fails
	mgr, _ := resolveTestManager(&sys)

	ok, err := mgr.configureHostDNS(t.Context())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestConfigureHostDNS_InspectNetworkFails(t *testing.T) {
	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil) // systemctl
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult("", "Error\n", fmt.Errorf("not found")) // InspectNetwork fails

	ok, err := mgr.configureHostDNS(t.Context())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestConfigureHostDNS_BridgeNotFound(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	// No bridge directory created.

	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil) // systemctl
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult(inspectNetworkJSON, "", nil) // InspectNetwork

	ok, err := mgr.configureHostDNS(t.Context())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestConfigureHostDNS_InspectContainerFails(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil) // systemctl
	sys.AddResult("", "", nil) // pkcheck x3
	sys.AddResult("", "", nil)
	sys.AddResult("", "", nil)
	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult(inspectNetworkJSON, "", nil)                 // InspectNetwork
	dockerMock.AddResult("", "Error\n", fmt.Errorf("container gone")) // InspectContainer

	ok, err := mgr.configureHostDNS(t.Context())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestConfigureHostDNS_ConfigureDNSFails(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil)                  // systemctl
	sys.AddResult("", "", nil)                  // pkcheck x3
	sys.AddResult("", "", nil)                  //
	sys.AddResult("", "", nil)                  //
	sys.AddResult("", "", fmt.Errorf("denied")) // resolvectl dns fails

	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult(inspectNetworkJSON, "", nil)
	dockerMock.AddResult(inspectDNSContainerJSON, "", nil)

	ok, err := mgr.configureHostDNS(t.Context())
	assert.Error(t, err)
	assert.False(t, ok)
}

// --- revertHostDNS ---

func TestRevertHostDNS_Success(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil) // systemctl is-active
	sys.AddResult("", "", nil) // resolvectl revert

	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult(inspectNetworkJSON, "", nil) // InspectNetwork

	mgr.revertHostDNS(t.Context())
	require.Len(t, sys.Calls, 2)
	assert.Equal(t, "resolvectl", sys.Calls[1].Name)
	assert.Equal(t, []string{"revert", "br-abcdef012345"}, sys.Calls[1].Args)
}

func TestRevertHostDNS_ResolvedNotActive(t *testing.T) {
	var sys cmdexec.MockExecutor
	sys.AddResult("", "", fmt.Errorf("inactive"))
	mgr, _ := resolveTestManager(&sys)

	mgr.revertHostDNS(t.Context()) // no-op
	require.Len(t, sys.Calls, 1)
}

func TestRevertHostDNS_InspectNetworkFails(t *testing.T) {
	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil) // systemctl ok
	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult("", "Error\n", fmt.Errorf("not found"))

	mgr.revertHostDNS(t.Context()) // no-op
}

func TestRevertHostDNS_BridgeNotFound(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })

	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil) // systemctl ok
	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult(inspectNetworkJSON, "", nil)

	mgr.revertHostDNS(t.Context()) // no-op
}

func TestRevertHostDNS_RevertError(t *testing.T) {
	dir := t.TempDir()
	old := sysClassNet
	sysClassNet = dir
	t.Cleanup(func() { sysClassNet = old })
	require.NoError(t, os.Mkdir(filepath.Join(dir, "br-abcdef012345"), 0o755))

	var sys cmdexec.MockExecutor
	sys.AddResult("", "", nil)                         // systemctl ok
	sys.AddResult("", "", fmt.Errorf("revert failed")) // resolvectl revert fails

	mgr, dockerMock := resolveTestManager(&sys)
	dockerMock.AddResult(inspectNetworkJSON, "", nil)

	mgr.revertHostDNS(t.Context()) // logs error but doesn't panic
}
