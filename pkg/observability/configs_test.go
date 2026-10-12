// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrafanaProvisioning(t *testing.T) {
	files := GrafanaProvisioning("dev")
	var names []string
	for name := range files {
		names = append(names, name)
	}
	assert.ElementsMatch(t, []string{
		"datasources/sind.yaml",
		"dashboards/sind.yaml",
		"dashboards/sind/slurm-cluster.json",
		"dashboards/sind/nodes.json",
		"dashboards/sind/service-health.json",
		"dashboards/sind/users-accounts.json",
		"dashboards/sind/logs.json",
	}, names)
	ds := string(files["datasources/sind.yaml"])
	assert.Contains(t, ds, "uid: sind-prometheus\n    access: proxy\n    url: http://dev-prometheus:9090\n")
	assert.Contains(t, ds, "uid: sind-loki\n    access: proxy\n    url: http://dev-loki:3100\n")
	assert.Contains(t, string(files["dashboards/sind.yaml"]), "path: "+DashboardsDir+"\n")
	assert.Contains(t, grafanaEnv, "GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH=/etc/grafana/provisioning/dashboards/sind/slurm-cluster.json")
	assert.Contains(t, grafanaEnv, "GF_DASHBOARDS_MIN_REFRESH_INTERVAL=15s")
}

func TestServiceFiles(t *testing.T) {
	tests := []struct {
		s     Service
		dir   string
		names []string
	}{
		{ServicePrometheus, "/etc/prometheus", []string{"sind.yml"}},
		{ServiceLoki, "/etc/loki", []string{"sind.yaml"}},
		{ServiceAlloy, "/etc/alloy", []string{"sind.alloy"}},
		{ServiceGrafana, "/etc/grafana/provisioning", nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.s), func(t *testing.T) {
			dir, files := serviceFiles("dev", tt.s, "// config\n")
			assert.Equal(t, tt.dir, dir)
			for name, f := range files {
				assert.Equal(t, int64(0o644), f.Mode, name)
			}
			if tt.names != nil {
				require.Len(t, files, 1)
				assert.Contains(t, files, tt.names[0])
			} else {
				assert.Len(t, files, len(GrafanaProvisioning("dev")))
			}
		})
	}
	_, files := serviceFiles("dev", ServiceAlloy, "// config\n")
	assert.Equal(t, "// config\n", string(files["sind.alloy"].Content))
}

func TestStoreConfigs(t *testing.T) {
	assert.Contains(t, prometheusArgs, "--web.enable-remote-write-receiver")
	assert.Contains(t, prometheusArgs, "--storage.tsdb.retention.time=7d")
	assert.Contains(t, lokiConfig, "retention_period: 168h")
	assert.Contains(t, lokiConfig, "retention_enabled: true")
	assert.Contains(t, lokiConfig, "reporting_enabled: false")
	assert.Contains(t, alloyArgs, "--disable-reporting")
}
