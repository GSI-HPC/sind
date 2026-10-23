// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuery(t *testing.T) {
	ex := &mock.Executor{}
	m := NewManager(docker.NewClient(ex), "sind")

	ex.AddResult(`{"status":"success","data":{"resultType":"vector","result":[`+
		`{"metric":{"__name__":"slurm_jobs_running","cluster":"dev"},"value":[1761040500.1,"3"]},`+
		`{"metric":{"cluster":"dev"},"value":[1761040500.1,"0.5"]}]}}`, "", nil)
	samples, err := m.Query(t.Context(), `max by (cluster) (slurm_nodes{realm="sind"})`)
	require.NoError(t, err)
	assert.Equal(t, []Sample{
		{Labels: map[string]string{"__name__": "slurm_jobs_running", "cluster": "dev"}, Value: 3},
		{Labels: map[string]string{"cluster": "dev"}, Value: 0.5},
	}, samples)
	require.Len(t, ex.Calls, 1)
	assert.Equal(t, []string{
		"exec", "sind-grafana", "curl", "-fsS", "--max-time", "5", "-G", "--data-urlencode",
		`query=max by (cluster) (slurm_nodes{realm="sind"})`, "http://sind-prometheus:9090/api/v1/query",
	}, ex.Calls[0].Args)

	ex.AddResult(`{"status":"success","data":{"resultType":"vector","result":[]}}`, "", nil)
	samples, err = m.Query(t.Context(), "up")
	require.NoError(t, err)
	assert.Empty(t, samples)

	for _, tc := range []struct {
		name, stdout, want string
		err                error
	}{
		{"exec", "", "querying Prometheus: ", testutil.ExitCode1(t)},
		{"json", "not json", "reading the answer of Prometheus: ", nil},
		{"refused", `{"status":"error","error":"parse error"}`, "prometheus refused the query: parse error", nil},
		{"matrix", `{"status":"success","data":{"resultType":"matrix","result":[]}}`, "prometheus answered a matrix, not a vector", nil},
		{"value", `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,7]}]}}`, "reading the value 7 of Prometheus: ", nil},
	} {
		ex.AddResult(tc.stdout, "", tc.err)
		_, err := m.Query(t.Context(), "up")
		require.ErrorContains(t, err, tc.want, tc.name)
	}
}
