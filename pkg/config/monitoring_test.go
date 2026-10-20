// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Monitoring(t *testing.T) {
	cfg, err := Parse([]byte("kind: Cluster\nmonitoring: true\n"))
	require.NoError(t, err)
	assert.True(t, cfg.Monitoring)

	cfg, err = Parse([]byte("kind: Cluster\n"))
	require.NoError(t, err)
	assert.False(t, cfg.Monitoring)
}

func TestValidate_Monitoring(t *testing.T) {
	tests := []struct {
		name    string
		main    string
		wantErr string
	}{
		{"defaults", "", ""},
		{"MetricsType openmetrics", "MetricsType=metrics/openmetrics\n", ""},
		{"other MetricsType", "MetricsType=metrics/none\n", "slurm main sets MetricsType=metrics/none: monitoring needs metrics/openmetrics"},
		{"CommunicationParameters without disable_http", "CommunicationParameters=block_null_hash\n", ""},
		{
			"disable_http",
			"CommunicationParameters=block_null_hash,disable_http\n",
			"slurm main sets CommunicationParameters=block_null_hash,disable_http: monitoring needs slurmctld's HTTP endpoints, which disable_http turns off",
		},
		{"MetricsAuthUsers", "MetricsAuthUsers=alice\n", "slurm main sets MetricsAuthUsers: monitoring reads slurmctld's metrics without a token"},
		{
			"PrivateData alone",
			"PrivateData=jobs\n",
			"slurm main sets PrivateData, which hides slurmctld's metrics from monitoring: add MetricsParameters=ignore_private_data to slurm main",
		},
		{"PrivateData with ignore_private_data", "PrivateData=jobs\nMetricsParameters=ignore_private_data\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{
				Kind:       "Cluster",
				Name:       "default",
				Monitoring: true,
				Nodes:      []Node{{Role: RoleController}, {Role: RoleWorker}},
				Slurm:      Slurm{Main: Section{Content: tt.main}},
			}
			cfg.ApplyDefaults()
			err := cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestValidate_MonitoringConflictsIgnoredWithoutMonitoring(t *testing.T) {
	cfg := &Cluster{
		Kind:  "Cluster",
		Name:  "default",
		Nodes: []Node{{Role: RoleController}, {Role: RoleWorker}},
		Slurm: Slurm{Main: Section{Content: "MetricsType=metrics/none\nPrivateData=jobs\n"}},
	}
	cfg.ApplyDefaults()
	assert.NoError(t, cfg.Validate())
}

func TestValidate_MonitoringNeedsManagedController(t *testing.T) {
	unmanaged := func() *Cluster {
		cfg := &Cluster{
			Kind:       "Cluster",
			Name:       "default",
			Monitoring: true,
			Nodes:      []Node{{Role: RoleController, Managed: new(false)}, {Role: RoleWorker}},
		}
		cfg.ApplyDefaults()
		return cfg
	}

	err := unmanaged().Validate()
	require.EqualError(t, err, "monitoring requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster")

	// The rejections of an unmanaged cluster that Validate checks earlier
	// win: identity first, then the slurm sections.
	cfg := unmanaged()
	cfg.Identity = Identity{Mode: IdentityNSSSlurm}
	err = cfg.Validate()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "monitoring")

	cfg = unmanaged()
	cfg.Slurm.Main = Section{Content: "MetricsType=metrics/none\n"}
	err = cfg.Validate()
	require.EqualError(t, err, "slurm main requires a managed controller: sind writes no Slurm configuration for an unmanaged cluster")

	// Unmanaged workers and db nodes of a managed cluster stay allowed.
	cfg = &Cluster{
		Kind:       "Cluster",
		Name:       "default",
		Monitoring: true,
		Nodes: []Node{
			{Role: RoleController},
			{Role: RoleDB, Managed: new(false)},
			{Role: RoleWorker, Managed: new(false)},
		},
	}
	cfg.ApplyDefaults()
	assert.NoError(t, cfg.Validate())
}
