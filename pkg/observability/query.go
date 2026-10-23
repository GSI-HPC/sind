// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Sample is one series of the answer to an instant query: its labels,
// with the metric name under __name__ when the query keeps it, and its
// value.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// queryAnswer is the part of Prometheus's answer to /api/v1/query that
// Query reads.
type queryAnswer struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// Query runs the PromQL instant query expr on the realm's Prometheus,
// through Grafana's curl as ServiceReady does, and returns the series of
// its answer, which must be a vector.
func (m *Manager) Query(ctx context.Context, expr string) ([]Sample, error) {
	out, err := m.curl(ctx, probeTimeout, "-G", "--data-urlencode", "query="+expr, m.URL(ServicePrometheus, "/api/v1/query"))
	if err != nil {
		return nil, fmt.Errorf("querying Prometheus: %w", err)
	}
	var a queryAnswer
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return nil, fmt.Errorf("reading the answer of Prometheus: %w", err)
	}
	if a.Status != "success" {
		return nil, fmt.Errorf("prometheus refused the query: %s", a.Error)
	}
	if a.Data.ResultType != "vector" {
		return nil, fmt.Errorf("prometheus answered a %s, not a vector", a.Data.ResultType)
	}
	samples := make([]Sample, 0, len(a.Data.Result))
	for _, r := range a.Data.Result {
		s, _ := r.Value[1].(string)
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("reading the value %v of Prometheus: %w", r.Value[1], err)
		}
		samples = append(samples, Sample{Labels: r.Metric, Value: v})
	}
	return samples, nil
}
