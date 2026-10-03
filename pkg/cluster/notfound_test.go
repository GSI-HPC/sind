// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"fmt"
	"slices"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// psLine is one `docker ps --format json` line of a node container with the
// given realm and cluster labels.
func psLine(realm, clusterName, node string) string {
	return fmt.Sprintf(`{"ID":"%[1]s-%[2]s-%[3]s","Names":"%[1]s-%[2]s-%[3]s","State":"running","Image":"img","Labels":"sind.realm=%[1]s,sind.cluster=%[2]s"}`,
		realm, clusterName, node)
}

// notFoundExecutor answers like a daemon on which realm has no cluster
// "dev": no containers and no network there, while the listing of every
// realm's "dev" containers returns others.
func notFoundExecutor(t *testing.T, others mock.Result) *mock.Executor {
	exitErr := notFoundErr(t)
	return &mock.Executor{OnCall: func(args []string, _ string) mock.Result {
		switch {
		case args[0] == "ps" && slices.Contains(args, "label=sind.realm=ci-42"):
			return mock.Result{}
		case args[0] == "ps":
			return others
		case args[0] == "network" && args[1] == "inspect":
			return mock.Result{Stderr: "Error: No such network: ci-42-dev-net\n", Err: exitErr}
		}
		return mock.Result{Err: fmt.Errorf("unexpected call: %v", args)}
	}}
}

func TestGetStatus_NotFoundNamesOtherRealms(t *testing.T) {
	tests := []struct {
		name   string
		others mock.Result
		want   string
	}{
		{"none", mock.Result{}, `cluster "dev" not found in realm "ci-42"`},
		{"one", mock.Result{Stdout: psLine("dev", "dev", "controller") + "\n" + psLine("dev", "dev", "worker-0")},
			`cluster "dev" not found in realm "ci-42" (it exists in realm "dev")`},
		{"several", mock.Result{Stdout: psLine("sind", "dev", "controller") + "\n" + psLine("b", "dev", "controller") + "\n" +
			psLine("Bad_Realm", "dev", "controller")},
			`cluster "dev" not found in realm "ci-42" (it exists in realms "b", "sind")`},
		{"listing fails", mock.Result{Err: fmt.Errorf("docker daemon not running")}, `cluster "dev" not found in realm "ci-42"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := docker.NewClient(notFoundExecutor(t, tt.others))
			_, err := GetStatus(t.Context(), c, "ci-42", "dev")
			require.EqualError(t, err, tt.want)
		})
	}
}

func TestOtherRealms(t *testing.T) {
	var m mock.Executor
	m.AddResult(psLine("ci-42", "dev", "controller")+"\n"+psLine("a", "dev", "controller")+"\n"+psLine("a", "dev", "worker-0"), "", nil)
	realms, err := OtherRealms(t.Context(), docker.NewClient(&m), "ci-42", "dev")
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, realms)
	assert.Equal(t, []string{"ps", "-a", "--no-trunc", "--format", "json", "--filter", "label=sind.cluster=dev"}, m.Calls[0].Args)

	m = mock.Executor{}
	m.AddResult("", "", fmt.Errorf("docker daemon not running"))
	_, err = OtherRealms(t.Context(), docker.NewClient(&m), "ci-42", "dev")
	require.ErrorContains(t, err, "listing containers: docker daemon not running")
}
