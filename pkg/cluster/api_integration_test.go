// SPDX-License-Identifier: LGPL-3.0-or-later

//go:build integration

package cluster

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restVersion is the data format version of the REST requests: the newest
// of Slurm 26.05, the first release line whose images have slurmrestd.
const restVersion = "v0.0.45"

// apiCluster creates a cluster with an api node from the config body
// (everything after kind, name and defaults). With an image of Slurm 25.11,
// which has no slurmrestd, it checks that the create fails with the image
// check and skips the rest of the test.
func apiCluster(ctx context.Context, t *testing.T, c *docker.Client, name, body string) (meshMgr *mesh.Manager, realm string, result *Cluster) {
	t.Helper()
	checkPrerequisites(t, c)

	img := os.Getenv("SIND_TEST_IMAGE")
	if img == "" {
		img = "ghcr.io/gsi-hpc/sind-node:latest"
	}
	version, err := slurm.DiscoverVersion(ctx, c, img)
	require.NoError(t, err)

	realm = testutil.Realm(name)
	meshMgr = mesh.NewManager(c, realm)
	t.Cleanup(func() {
		bg := context.Background()
		_ = Delete(bg, c, meshMgr, name)
		_ = meshMgr.CleanupMesh(bg)
	})
	require.NoError(t, meshMgr.EnsureMesh(ctx))

	cfg, err := config.Parse([]byte(fmt.Sprintf("kind: Cluster\nname: %s\ndefaults:\n  image: %s\n%s", name, img, body)))
	require.NoError(t, err)
	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	result, err = Create(ctx, c, meshMgr, cfg, probeInterval)
	if strings.HasPrefix(version, "25.") {
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no slurmrestd with its systemd unit, which the api node runs")
		t.Skipf("Slurm %s images have no slurmrestd; the create failed as it should", version)
	}
	require.NoError(t, err)
	return meshMgr, realm, result
}

// apiURL returns the URL of a REST path on the cluster's api node, by the
// node's address on the cluster network, which the Docker host reaches.
func apiURL(t *testing.T, result *Cluster, path string) string {
	t.Helper()
	i := slices.IndexFunc(result.Nodes, func(n *Node) bool { return n.Role == config.RoleAPI })
	require.NotEqual(t, -1, i, "api node missing")
	return "http://" + result.Nodes[i].IP + ":" + probe.SlurmrestdPort + path
}

// restResponse is the part of slurmrestd's responses the tests read.
type restResponse struct {
	JobID  int `json:"job_id"`
	Errors []struct {
		Description string `json:"description"`
		Error       string `json:"error"`
	} `json:"errors"`
}

// rest sends a request with a user's token and returns the HTTP status and
// the decoded body.
func rest(ctx context.Context, t *testing.T, method, url, user, token, body string) (int, restResponse, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-SLURM-USER-NAME", user)
	req.Header.Set("X-SLURM-USER-TOKEN", token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded restResponse
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, decoded, string(raw)
}

// scontrolToken returns a token from scontrol token, run as root on node.
func scontrolToken(ctx context.Context, t *testing.T, c *docker.Client, node docker.ContainerName, user string) string {
	t.Helper()
	out, err := c.Exec(ctx, node, "scontrol", "token", "username="+user, "lifespan=600")
	require.NoError(t, err)
	token, ok := strings.CutPrefix(strings.TrimSpace(out), "SLURM_JWT=")
	require.True(t, ok, "scontrol token printed %q", out)
	return token
}

// signToken signs claims as an HS256 JSON Web Token with key.
func signToken(t *testing.T, key []byte, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	unsigned := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(mac.Sum(nil))
}

// jobScript is the body of a job submission that prints its user.
const jobScript = `{"job": {"name": "rest", "script": "#!/bin/sh\nid -un", "current_working_directory": "/tmp", "environment": ["PATH=/usr/bin:/bin"]}}`

// waitJob waits until sacct on controller reports the job with want
// (User|State).
func waitJob(ctx context.Context, t *testing.T, c *docker.Client, controller docker.ContainerName, jobID int, want string) {
	t.Helper()
	assert.Eventually(t, func() bool {
		out, err := c.Exec(ctx, controller, "sacct", "-a", "-n", "-X", "-P", "-o", "User,State", "-j", strconv.Itoa(jobID))
		return err == nil && strings.TrimSpace(out) == want
	}, 2*time.Minute, time.Second, "job %d not %s", jobID, want)
}

func TestAPINode(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()

	clusterName := "it-api"
	meshMgr, realm, result := apiCluster(ctx, t, c, clusterName, `
users:
  - name: alice
    uid: 2001
    accounts: [physics]
accounts: [physics]
nodes:
  - controller
  - db
  - api
  - worker
`)

	controller := ContainerName(realm, clusterName, "controller")
	api := ContainerName(realm, clusterName, "api")
	assertDNSRecord(t, c, meshMgr, clusterName, "api")

	// slurm.conf sets up auth/jwt with sind's key, which only slurm reads.
	params := slurmConfig(ctx, t, c, controller)
	assert.Equal(t, "auth/jwt", params["AuthAltTypes"])
	assert.Equal(t, "jwt_key=/etc/slurm/jwt_hs256.key", params["AuthAltParameters"])
	out, err := c.Exec(ctx, api, "stat", "-c", "%U:%G %a %s", slurm.JWTKeyPath)
	require.NoError(t, err)
	assert.Equal(t, "slurm:slurm 600 32", strings.TrimSpace(out))
	out, err = c.Exec(ctx, api, "systemctl", "show", "-p", "User", "--value", "slurmrestd")
	require.NoError(t, err)
	assert.Equal(t, "slurmrestd", strings.TrimSpace(out))

	// get cluster reports slurmrestd on the api node.
	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	i := slices.IndexFunc(status.Nodes, func(n *NodeStatus) bool { return n.Role == config.RoleAPI })
	require.NotEqual(t, -1, i)
	assert.Equal(t, ServiceHealth{probe.ServiceMunge: true, probe.ServiceSSHD: true, probe.ServiceSlurmrestd: true}, status.Nodes[i].Health.Services)

	// root's token from slurmctld reaches both daemons.
	rootToken := scontrolToken(ctx, t, c, controller, "root")
	code, _, body := rest(ctx, t, http.MethodGet, apiURL(t, result, "/slurm/"+restVersion+"/ping/"), "root", rootToken, "")
	assert.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, `"pings"`)
	code, _, body = rest(ctx, t, http.MethodGet, apiURL(t, result, "/slurmdb/"+restVersion+"/jobs/"), "root", rootToken, "")
	assert.Equal(t, http.StatusOK, code, body)

	// With identity local the controller and the db node have alice, so
	// her token from slurmctld works: her job runs as her.
	waitNodeIdle(t, c, controller, "worker-0")
	aliceToken := scontrolToken(ctx, t, c, controller, "alice")
	code, job, body := rest(ctx, t, http.MethodPost, apiURL(t, result, "/slurm/"+restVersion+"/job/submit"), "alice", aliceToken, jobScript)
	require.Equal(t, http.StatusOK, code, body)
	require.Positive(t, job.JobID, body)
	waitJob(ctx, t, c, controller, job.JobID, "alice|COMPLETED")
	code, _, body = rest(ctx, t, http.MethodGet, apiURL(t, result, "/slurmdb/"+restVersion+"/jobs/"), "alice", aliceToken, "")
	assert.Equal(t, http.StatusOK, code, body)

	// The exported key is the cluster's: a token signed with it works.
	key, err := GetAuthKey(ctx, c, realm, clusterName, AuthJWT)
	require.NoError(t, err)
	assert.Equal(t, AuthJWT, key.Type)
	now := time.Now().Unix()
	signed := signToken(t, key.Key, map[string]any{"sun": "root", "iat": now, "exp": now + 600})
	code, _, body = rest(ctx, t, http.MethodGet, apiURL(t, result, "/slurm/"+restVersion+"/ping/"), "root", signed, "")
	assert.Equal(t, http.StatusOK, code, body)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestAPINodeClientIDs(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()

	clusterName := "it-api-cid"
	_, realm, result := apiCluster(ctx, t, c, clusterName, `
identity: clientIds
users:
  - name: alice
    uid: 2001
    accounts: [physics]
accounts: [physics]
nodes:
  - controller
  - db
  - api
  - submitter
  - worker
`)

	controller := ContainerName(realm, clusterName, "controller")

	// Under clientIds, tokens may carry the user's identity.
	params := slurmConfig(ctx, t, c, controller)
	assert.Equal(t, "auth/jwt", params["AuthAltTypes"])
	assert.Equal(t, "jwt_key=/etc/slurm/jwt_hs256.key,use_jwt_client_ids", params["AuthAltParameters"])

	// The controller has no alice: a token from slurmctld names her, but
	// slurmctld cannot look her up, and refuses her job.
	waitNodeIdle(t, c, controller, "worker-0")
	submit := apiURL(t, result, "/slurm/"+restVersion+"/job/submit")
	aliceToken := scontrolToken(ctx, t, c, controller, "alice")
	code, job, body := rest(ctx, t, http.MethodPost, submit, "alice", aliceToken, jobScript)
	assert.Zero(t, job.JobID, body)
	assert.True(t, code != http.StatusOK || len(job.Errors) > 0, "alice's job accepted: %s", body)

	// A token with her identity works without the accounts: her job runs
	// as her on the worker (nss_slurm), under her association.
	key, err := GetAuthKey(ctx, c, realm, clusterName, AuthJWT)
	require.NoError(t, err)
	now := time.Now().Unix()
	signed := signToken(t, key.Key, map[string]any{
		"sun": "alice", "iat": now, "exp": now + 600,
		"uid": 2001, "gid": 2001,
		"id": map[string]any{"name": "alice", "gecos": "alice", "dir": "/home/alice", "shell": "/bin/bash", "gids": []int{2001}},
	})
	code, job, body = rest(ctx, t, http.MethodPost, submit, "alice", signed, jobScript)
	require.Equal(t, http.StatusOK, code, body)
	require.Positive(t, job.JobID, body)
	waitJob(ctx, t, c, controller, job.JobID, "alice|COMPLETED")
	code, _, body = rest(ctx, t, http.MethodGet, apiURL(t, result, "/slurmdb/"+restVersion+"/jobs/"), "alice", signed, "")
	assert.Equal(t, http.StatusOK, code, body)

	// The api node has no users and no munge under clientIds.
	status, err := GetStatus(ctx, c, realm, clusterName)
	require.NoError(t, err)
	i := slices.IndexFunc(status.Nodes, func(n *NodeStatus) bool { return n.Role == config.RoleAPI })
	require.NotEqual(t, -1, i)
	assert.Equal(t, ServiceHealth{probe.ServiceSSHD: true, probe.ServiceSlurmrestd: true}, status.Nodes[i].Health.Services)
	assert.Equal(t, "2", lookupStatus(ctx, t, c, ContainerName(realm, clusterName, "api"), "alice"))

	t.Logf("docker I/O:\n%s", rec.Dump())
}
