// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nodeInspectJSON returns mock docker inspect output for a container with the given IP on the given network.
func nodeInspectJSON(name, ip, network string) string {
	return fmt.Sprintf(`[{"Id":"id","Name":"/%s","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"%s":{"IPAddress":"%s"}}}}]`, name, network, ip)
}

func TestGetSSHConfig_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "ssh-config"})
	require.NoError(t, err)
	assert.Equal(t, "ssh-config", c.Use)
}

func TestGetSSHConfig_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "ssh-config", "extra")
	assert.Error(t, err)
}

func TestGetSSHConfig_Output(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	stdout, _, err := executeCommand("get", "ssh-config")
	require.NoError(t, err)
	assert.Equal(t, "/xdg/state/sind/sind/ssh_config\n", stdout)
}

func TestGetSSHConfig_CustomRealm(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	stdout, _, err := executeCommand("--realm", "ci-42", "get", "ssh-config")
	require.NoError(t, err)
	assert.Equal(t, "/xdg/state/sind/ci-42/ssh_config\n", stdout)
}

func TestGetSSHConfig_XDGFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/user")
	stdout, _, err := executeCommand("get", "ssh-config")
	require.NoError(t, err)
	assert.Equal(t, "/home/user/.local/state/sind/sind/ssh_config\n", stdout)
}

func TestGetClusters_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "clusters"})
	require.NoError(t, err)
	assert.Equal(t, "clusters", c.Use)
}

func TestGetClusters_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "clusters", "extra")
	assert.Error(t, err)
}

func TestGetClusters_Output(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=25.11.0",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "dev")
	assert.Contains(t, stdout, "25.11.0")
	assert.Contains(t, stdout, "running")
}

func TestGetNodes_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "nodes"})
	require.NoError(t, err)
	assert.Equal(t, "nodes [CLUSTER]", c.Use)
}

func TestGetNodes_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("get", "nodes", "a", "b")
	assert.Error(t, err)
}

func TestGetNodes_Output(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
	), "", nil)
	m.AddResult(nodeInspectJSON("sind-dev-controller", "10.0.0.2", "sind-dev-net"), "", nil)
	m.AddResult(nodeInspectJSON("sind-dev-worker-0", "10.0.0.3", "sind-dev-net"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "nodes", "dev")
	require.NoError(t, err)
	assert.Contains(t, stdout, "CONTAINER")
	assert.Contains(t, stdout, "FQDN")
	assert.Contains(t, stdout, "IP")
	assert.Contains(t, stdout, "sind-dev-controller")
	assert.Contains(t, stdout, "controller.dev.sind.sind")
	assert.Contains(t, stdout, "10.0.0.2")
	assert.Contains(t, stdout, "sind-dev-worker-0")
	assert.NotContains(t, stdout, "CLUSTER")
}

func TestGetNodes_AllClusters(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-prod-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=prod,sind.role=controller",
		},
	), "", nil)
	m.AddResult(nodeInspectJSON("sind-dev-controller", "10.0.0.2", "sind-dev-net"), "", nil)
	m.AddResult(nodeInspectJSON("sind-prod-controller", "10.1.0.2", "sind-prod-net"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "nodes")
	require.NoError(t, err)
	assert.Contains(t, stdout, "CONTAINER")
	assert.Contains(t, stdout, "CLUSTER")
	assert.Contains(t, stdout, "FQDN")
	assert.Contains(t, stdout, "sind-dev-controller")
	assert.Contains(t, stdout, "dev")
	assert.Contains(t, stdout, "sind-prod-controller")
	assert.Contains(t, stdout, "prod")
}

func TestGetNetworks_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "networks"})
	require.NoError(t, err)
	assert.Equal(t, "networks", c.Use)
}

func TestGetVolumes_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "volumes"})
	require.NoError(t, err)
	assert.Equal(t, "volumes", c.Use)
}

func TestGetMungeKey_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "munge-key"})
	require.NoError(t, err)
	assert.Equal(t, "munge-key [CLUSTER]", c.Use)
}

func TestGetMungeKey_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("get", "munge-key", "a", "b")
	assert.Error(t, err)
}

func TestGetMungeKey_Output(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "a", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller",
	}), "", nil)
	m.AddResult(testutil.TarArchive("munge.key", "secret-key"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "munge-key", "dev")
	require.NoError(t, err)
	assert.Equal(t, "c2VjcmV0LWtleQ==\n", stdout)
}

func TestGetDNS_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "dns"})
	require.NoError(t, err)
	assert.Equal(t, "dns", c.Use)
}

func TestGetDNS_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "dns", "extra")
	assert.Error(t, err)
}

func TestGetDNS_Output(t *testing.T) {
	corefile := "sind.sind:53 {\n    hosts {\n" +
		"        172.18.0.2 controller.dev.sind.sind\n" +
		"        172.18.0.3 worker-0.dev.sind.sind\n" +
		"        fallthrough\n    }\n    reload\n    log\n    errors\n}\n\n" +
		".:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"

	var m mock.Executor
	m.AddResult(testutil.TarArchive("Corefile", corefile), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "dns")
	require.NoError(t, err)
	assert.Contains(t, stdout, "HOSTNAME")
	assert.Contains(t, stdout, "IP")
	assert.Contains(t, stdout, "controller.dev.sind.sind")
	assert.Contains(t, stdout, "172.18.0.2")
	assert.Contains(t, stdout, "worker-0.dev.sind.sind")
	assert.Contains(t, stdout, "172.18.0.3")
}

func TestGetDNS_Empty(t *testing.T) {
	corefile := "sind.sind:53 {\n    hosts {\n" +
		"        fallthrough\n    }\n    reload\n    log\n    errors\n}\n\n" +
		".:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"

	var m mock.Executor
	m.AddResult(testutil.TarArchive("Corefile", corefile), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "dns")
	require.NoError(t, err)
	assert.Contains(t, stdout, "HOSTNAME")
	assert.NotContains(t, stdout, "sind.sind")
}

func TestGetDNS_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", fmt.Errorf("container not found"))

	_, _, err := executeWithMock(&m, "get", "dns")
	assert.Error(t, err)
}

// --- JSON output ---

func TestGetClusters_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller,sind.slurm.version=25.11.0",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker,sind.slurm.version=25.11.0",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "clusters", "--output", "json")
	require.NoError(t, err)

	var got []struct {
		Name         string `json:"name"`
		SlurmVersion string `json:"slurm_version"`
		Status       string `json:"status"`
		Nodes        int    `json:"nodes"`
		Controllers  int    `json:"controllers"`
		Workers      int    `json:"workers"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "dev", got[0].Name)
	assert.Equal(t, "25.11.0", got[0].SlurmVersion)
	assert.Equal(t, "running", got[0].Status)
	assert.Equal(t, 2, got[0].Nodes)
	assert.Equal(t, 1, got[0].Controllers)
	assert.Equal(t, 1, got[0].Workers)
}

func TestGetClusters_JSONEmpty(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)

	stdout, _, err := executeWithMock(&m, "get", "clusters", "--output", "json")
	require.NoError(t, err)
	assert.Equal(t, "[]\n", stdout)
}

// --- Realms ---

func TestGetRealms_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "realms"})
	require.NoError(t, err)
	assert.Equal(t, "realms", c.Use)
}

func TestGetRealms_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "realms", "extra")
	assert.Error(t, err)
}

func TestGetRealms_Output(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "ci-42-default-controller", State: "running", Image: "img",
			Labels: "sind.realm=ci-42,sind.cluster=default,sind.role=controller",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "realms")
	require.NoError(t, err)
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "CLUSTERS")
	assert.Contains(t, stdout, "sind")
	assert.Contains(t, stdout, "ci-42")
}

func TestGetRealms_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-prod-controller", State: "running", Image: "img",
			Labels: "sind.realm=sind,sind.cluster=prod,sind.role=controller",
		},
	), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "realms", "--output", "json")
	require.NoError(t, err)

	var got []struct {
		Name     string `json:"name"`
		Clusters int    `json:"clusters"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "sind", got[0].Name)
	assert.Equal(t, 2, got[0].Clusters)
}

func TestGetRealms_Empty(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "", nil)

	stdout, _, err := executeWithMock(&m, "get", "realms")
	require.NoError(t, err)
	// Empty output still prints the header row for consistency with the
	// other list commands.
	assert.Contains(t, stdout, "NAME")
	assert.Contains(t, stdout, "CLUSTERS")
}

func TestGetNodes_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(
		testutil.PsEntry{
			ID: "a", Names: "sind-dev-controller", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=controller",
		},
		testutil.PsEntry{
			ID: "b", Names: "sind-dev-worker-0", State: "running", Image: "sind-node:25.11",
			Labels: "sind.cluster=dev,sind.role=worker",
		},
	), "", nil)
	m.AddResult(nodeInspectJSON("sind-dev-controller", "10.0.0.2", "sind-dev-net"), "", nil)
	m.AddResult(nodeInspectJSON("sind-dev-worker-0", "10.0.0.3", "sind-dev-net"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "nodes", "dev", "--output", "json")
	require.NoError(t, err)

	var got []struct {
		Container string `json:"container"`
		Cluster   string `json:"cluster"`
		Role      string `json:"role"`
		FQDN      string `json:"fqdn"`
		IP        string `json:"ip"`
		Status    string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "sind-dev-controller", got[0].Container)
	assert.Equal(t, "dev", got[0].Cluster)
	assert.Equal(t, "controller", got[0].Role)
	assert.Equal(t, "controller.dev.sind.sind", got[0].FQDN)
	assert.Equal(t, "10.0.0.2", got[0].IP)
	assert.Equal(t, "running", got[0].Status)
	assert.Equal(t, "sind-dev-worker-0", got[1].Container)
}

func TestGetDNS_JSON(t *testing.T) {
	corefile := "sind.sind:53 {\n    hosts {\n" +
		"        172.18.0.2 controller.dev.sind.sind\n" +
		"        172.18.0.3 worker-0.dev.sind.sind\n" +
		"        fallthrough\n    }\n    reload\n    log\n    errors\n}\n\n" +
		".:53 {\n    forward . /etc/resolv.conf\n    log\n    errors\n}\n"

	var m mock.Executor
	m.AddResult(testutil.TarArchive("Corefile", corefile), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "dns", "--output", "json")
	require.NoError(t, err)

	var got []mesh.DNSRecord
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "controller.dev.sind.sind", got[0].Hostname)
	assert.Equal(t, "172.18.0.2", got[0].IP)
}

func TestGetMungeKey_JSON(t *testing.T) {
	var m mock.Executor
	m.AddResult(testutil.NDJSON(testutil.PsEntry{
		ID: "a", Names: "sind-dev-controller", State: "running",
		Image: "img:1", Labels: "sind.cluster=dev,sind.role=controller",
	}), "", nil)
	m.AddResult(testutil.TarArchive("munge.key", "secret-key"), "", nil)

	stdout, _, err := executeWithMock(&m, "get", "munge-key", "dev", "--output", "json")
	require.NoError(t, err)

	var got struct {
		Key string `json:"key"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "c2VjcmV0LWtleQ==", got.Key)
}

func TestGetSSHConfig_JSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg/state")
	stdout, _, err := executeCommand("get", "ssh-config", "--output", "json")
	require.NoError(t, err)

	var got struct {
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "/xdg/state/sind/sind/ssh_config", got.Path)
}

// --- Mesh ---

func TestGetMesh_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "mesh"})
	require.NoError(t, err)
	assert.Equal(t, "mesh", c.Use)
}

func TestGetMesh_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "mesh", "extra")
	assert.Error(t, err)
}

func TestGetMesh_Output(t *testing.T) {
	inspectJSON := `[{"Id":"dns1","Name":"/sind-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"10.0.0.2"}}}}]`
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // ContainerExists → true
	m.AddResult(inspectJSON, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "mesh")
	require.NoError(t, err)
	assert.Contains(t, stdout, "PROPERTY")
	assert.Contains(t, stdout, "sind-mesh")
	assert.Contains(t, stdout, "sind-dns")
	assert.Contains(t, stdout, "10.0.0.2")
	assert.Contains(t, stdout, "sind.sind")
	assert.Contains(t, stdout, "sind-ssh")
	assert.Contains(t, stdout, "sind-ssh-config")
}

func TestGetMesh_JSON(t *testing.T) {
	inspectJSON := `[{"Id":"dns1","Name":"/sind-dns","State":{"Status":"running"},"Config":{"Labels":{}},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"10.0.0.2"}}}}]`
	var m mock.Executor
	m.AddResult("[{}]\n", "", nil) // ContainerExists → true
	m.AddResult(inspectJSON, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "mesh", "--output", "json")
	require.NoError(t, err)

	var got mesh.Info
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "sind-mesh", got.Network)
	assert.Equal(t, "sind-dns", got.DNSContainer)
	assert.Equal(t, "10.0.0.2", got.DNSIP)
	assert.Equal(t, "sind.sind", got.DNSZone)
}

func TestGetMesh_Error(t *testing.T) {
	var m mock.Executor
	// ContainerExists returns a non-exit-code-1 error (daemon unreachable).
	m.AddResult("", "", fmt.Errorf("docker daemon unreachable"))

	_, _, err := executeWithMock(&m, "get", "mesh")
	assert.Error(t, err)
}

// --- SSH keys ---

func TestGetSSHPrivateKey_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "ssh-private-key"})
	require.NoError(t, err)
	assert.Equal(t, "ssh-private-key", c.Use)
}

func TestGetSSHPrivateKey_RejectsArgs(t *testing.T) {
	_, _, err := executeCommand("get", "ssh-private-key", "extra")
	assert.Error(t, err)
}

func TestGetSSHPrivateKey_Output(t *testing.T) {
	pemData := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
	var m mock.Executor
	m.AddResult(pemData, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "ssh-private-key")
	require.NoError(t, err)
	assert.Equal(t, pemData, stdout)
}

func TestGetSSHPrivateKey_JSON(t *testing.T) {
	pemData := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
	var m mock.Executor
	m.AddResult(pemData, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "ssh-private-key", "--output", "json")
	require.NoError(t, err)

	var got struct {
		PrivateKey string `json:"private_key"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, pemData, got.PrivateKey)
}

func TestGetSSHPublicKey_Output(t *testing.T) {
	pubKey := "ssh-ed25519 AAAA... comment\n"
	var m mock.Executor
	m.AddResult(pubKey, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "ssh-public-key")
	require.NoError(t, err)
	assert.Equal(t, pubKey, stdout)
}

func TestGetSSHPublicKey_JSON(t *testing.T) {
	pubKey := "ssh-ed25519 AAAA... comment\n"
	var m mock.Executor
	m.AddResult(pubKey, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "ssh-public-key", "--output", "json")
	require.NoError(t, err)

	var got struct {
		PublicKey string `json:"public_key"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, pubKey, got.PublicKey)
}

func TestGetSSHKnownHosts_Output(t *testing.T) {
	hosts := "host1 ssh-ed25519 AAAA...\nhost2 ssh-ed25519 BBBB...\n"
	var m mock.Executor
	m.AddResult(hosts, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "ssh-known-hosts")
	require.NoError(t, err)
	assert.Equal(t, hosts, stdout)
}

func TestGetSSHKnownHosts_JSON(t *testing.T) {
	hosts := "host1 ssh-ed25519 AAAA...\nhost2 ssh-ed25519 BBBB...\n"
	var m mock.Executor
	m.AddResult(hosts, "", nil)

	stdout, _, err := executeWithMock(&m, "get", "ssh-known-hosts", "--output", "json")
	require.NoError(t, err)

	var got struct {
		KnownHosts string `json:"known_hosts"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, hosts, got.KnownHosts)
}

func TestGetSSHPrivateKey_Error(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Error\n", fmt.Errorf("exit status 1"))

	_, _, err := executeWithMock(&m, "get", "ssh-private-key")
	assert.Error(t, err)
}

// --- Cluster (status) ---

func TestGetCluster_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "cluster"})
	require.NoError(t, err)
	assert.Equal(t, "cluster [NAME]", c.Use)
}

func TestGetCluster_TooManyArgs(t *testing.T) {
	_, _, err := executeCommand("get", "cluster", "a", "b")
	assert.Error(t, err)
}

func TestCheckmark(t *testing.T) {
	assert.Equal(t, "\u2713", checkmark(true))
	assert.Equal(t, "\u2717", checkmark(false))
}

func TestFormatState(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*cluster.NodeStatus
		state cluster.State
		want  string
	}{
		{
			"all running",
			[]*cluster.NodeStatus{
				{Health: &cluster.NodeHealth{State: "running"}},
				{Health: &cluster.NodeHealth{State: "running"}},
			},
			cluster.StateRunning,
			"running (2/0/0/2)",
		},
		{
			"mixed",
			[]*cluster.NodeStatus{
				{Health: &cluster.NodeHealth{State: "running"}},
				{Health: &cluster.NodeHealth{State: "exited"}},
			},
			cluster.StateMixed,
			"mixed (1/1/0/2)",
		},
		{
			"empty",
			nil,
			cluster.StateEmpty,
			"empty (0/0/0/0)",
		},
		{
			"paused",
			[]*cluster.NodeStatus{
				{Health: &cluster.NodeHealth{State: "paused"}},
			},
			cluster.StatePaused,
			"paused (0/0/1/1)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &cluster.Status{State: tt.state, Nodes: tt.nodes}
			assert.Equal(t, tt.want, formatState(s))
		})
	}
}

func TestFormatServices(t *testing.T) {
	assert.Equal(t, "", formatServices(nil))
	assert.Equal(t, "slurmctld \u2713", formatServices(cluster.ServiceHealth{"slurmctld": true}))
	assert.Equal(t, "slurmd \u2717", formatServices(cluster.ServiceHealth{"slurmd": false}))
}

func TestFormatServices_Multiple(t *testing.T) {
	services := cluster.ServiceHealth{"slurmctld": true, "slurmd": false}
	got := formatServices(services)
	assert.Equal(t, "slurmctld \u2713 slurmd \u2717", got)
}

// --- Node (single) ---

func TestGetNode_CommandExists(t *testing.T) {
	cmd := NewRootCommand()
	c, _, err := cmd.Find([]string{"get", "node"})
	require.NoError(t, err)
	assert.Equal(t, "node NODE[.CLUSTER]", c.Use)
}

func TestGetNode_RequiresArg(t *testing.T) {
	_, _, err := executeCommand("get", "node")
	assert.Error(t, err)
}

func TestGetNode_RejectsFQDN(t *testing.T) {
	_, _, err := executeCommand("get", "node", "worker-0.dev.sind.sind")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NODE[.CLUSTER]")
	assert.Contains(t, err.Error(), "worker-0.dev.sind.sind")
}

func TestGetNode_Output(t *testing.T) {
	inspectWithLabels := `[{"Id":"abc","Name":"/sind-dev-controller","State":{"Status":"running"},"Config":{"Labels":{"sind.role":"controller","sind.cluster":"dev"}},"NetworkSettings":{"Networks":{"sind-dev-net":{"IPAddress":"10.0.0.2"}}}}]`

	m := &mock.Executor{
		OnCall: func(args []string, _ string) mock.Result {
			if len(args) >= 2 && args[0] == "inspect" {
				return mock.Result{Stdout: inspectWithLabels}
			}
			// probe.Snapshot fuses munge/sshd into a single call; emit one
			// "active" line per requested unit.
			if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
				var b strings.Builder
				for range args[4:] {
					b.WriteString("active\n")
				}
				return mock.Result{Stdout: b.String()}
			}
			if len(args) >= 3 && args[2] == "scontrol" {
				return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
			}
			return mock.Result{Err: fmt.Errorf("unexpected: %v", args)}
		},
	}

	stdout, _, err := executeWithMock(m, "get", "node", "controller.dev")
	require.NoError(t, err)
	assert.Contains(t, stdout, "CONTAINER")
	assert.Contains(t, stdout, "FQDN")
	assert.Contains(t, stdout, "IP")
	assert.Contains(t, stdout, "sind-dev-controller")
	assert.Contains(t, stdout, "controller.dev.sind.sind")
	assert.Contains(t, stdout, "10.0.0.2")
	assert.Contains(t, stdout, "SERVICES")
	assert.Contains(t, stdout, "munge")
	assert.Contains(t, stdout, "sshd")
	assert.Contains(t, stdout, "slurmctld")
	assert.Contains(t, stdout, "\u2713")
}

func TestGetNode_JSON(t *testing.T) {
	inspectWithLabels := `[{"Id":"abc","Name":"/sind-dev-controller","State":{"Status":"running"},"Config":{"Labels":{"sind.role":"controller","sind.cluster":"dev"}},"NetworkSettings":{"Networks":{"sind-dev-net":{"IPAddress":"10.0.0.2"}}}}]`

	m := &mock.Executor{
		OnCall: func(args []string, _ string) mock.Result {
			if len(args) >= 2 && args[0] == "inspect" {
				return mock.Result{Stdout: inspectWithLabels}
			}
			if len(args) >= 4 && args[2] == "systemctl" && args[3] == "is-active" {
				var b strings.Builder
				for range args[4:] {
					b.WriteString("active\n")
				}
				return mock.Result{Stdout: b.String()}
			}
			if len(args) >= 3 && args[2] == "scontrol" {
				return mock.Result{Stdout: "Slurmctld(primary) at controller is UP\n"}
			}
			return mock.Result{Err: fmt.Errorf("unexpected: %v", args)}
		},
	}

	stdout, _, err := executeWithMock(m, "get", "node", "controller.dev", "--output", "json")
	require.NoError(t, err)

	var got cluster.NodeDetail
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "sind-dev-controller", got.Container)
	assert.Equal(t, "dev", got.Cluster)
	assert.Equal(t, config.RoleController, got.Role)
	assert.Equal(t, "controller.dev.sind.sind", got.FQDN)
	assert.Equal(t, "10.0.0.2", got.IP)
	assert.Equal(t, docker.StateRunning, got.Status)
	assert.True(t, got.Services["munge"])
	assert.True(t, got.Services["sshd"])
	assert.True(t, got.Services["slurmctld"])
}

// --- Integration ---

func TestGetClustersEmpty(t *testing.T) {
	t.Parallel()
	realClient(t)
	realm := "it-empty-" + testID
	stdout, _, err := executeWithRealm(realm, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, "NAME")
}

func TestGetLifecycle(t *testing.T) {
	t.Parallel()
	c := realClient(t)
	ctx := t.Context()
	realm := "it-get-" + testID
	cluster := "cli-get-" + testID

	netName := docker.NetworkName(realm + "-" + cluster + "-net")
	ctrName := docker.ContainerName(realm + "-" + cluster + "-controller")
	volConfig := docker.VolumeName(realm + "-" + cluster + "-config")
	volMunge := docker.VolumeName(realm + "-" + cluster + "-munge")
	volData := docker.VolumeName(realm + "-" + cluster + "-data")

	t.Cleanup(func() {
		bg := context.Background()
		_ = c.KillContainer(bg, ctrName)
		_ = c.RemoveContainer(bg, ctrName)
		_ = c.RemoveVolume(bg, volConfig)
		_ = c.RemoveVolume(bg, volMunge)
		_ = c.RemoveVolume(bg, volData)
		_ = c.RemoveNetwork(bg, netName)
	})

	resourceLabels := docker.Labels{"sind.realm": realm, "sind.cluster": cluster}
	_, err := c.CreateNetwork(ctx, netName, resourceLabels)
	require.NoError(t, err)
	require.NoError(t, c.CreateVolume(ctx, volConfig, resourceLabels))
	require.NoError(t, c.CreateVolume(ctx, volMunge, resourceLabels))
	require.NoError(t, c.CreateVolume(ctx, volData, resourceLabels))

	_, err = c.RunContainer(ctx,
		"--name", string(ctrName),
		"--network", string(netName),
		"--label", "sind.realm="+realm,
		"--label", "sind.cluster="+cluster,
		"--label", "sind.role=controller",
		"--label", "sind.slurm.version=25.11.0",
		"-v", string(volMunge)+":/etc/munge",
		"busybox:latest", "sleep", "120",
	)
	require.NoError(t, err)
	require.NoError(t, c.WriteFile(ctx, ctrName, "/etc/munge/munge.key", "test-munge-key"))

	// get clusters
	stdout, _, err := executeWithRealm(realm, "get", "clusters")
	require.NoError(t, err)
	assert.Contains(t, stdout, cluster)
	assert.Contains(t, stdout, "25.11.0")
	assert.Contains(t, stdout, "running")

	// get nodes
	stdout, _, err = executeWithRealm(realm, "get", "nodes", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, string(ctrName))

	// get networks
	stdout, _, err = executeWithRealm(realm, "get", "networks")
	require.NoError(t, err)
	assert.Contains(t, stdout, string(netName))

	// get volumes
	stdout, _, err = executeWithRealm(realm, "get", "volumes")
	require.NoError(t, err)
	assert.Contains(t, stdout, string(volConfig))
	assert.Contains(t, stdout, string(volMunge))
	assert.Contains(t, stdout, string(volData))

	// get munge-key
	stdout, _, err = executeWithRealm(realm, "get", "munge-key", cluster)
	require.NoError(t, err)
	assert.Contains(t, stdout, "dGVzdC1tdW5nZS1rZXk=") // base64("test-munge-key")
}
