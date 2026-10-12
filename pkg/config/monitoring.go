// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import "fmt"

// MetricsType is the slurm.conf MetricsType of a monitored cluster: the
// plugin behind slurmctld's /metrics endpoints, which the monitoring
// stack scrapes.
const MetricsType = "metrics/openmetrics"

// validateMonitoring checks a monitored cluster's main section against what
// the monitoring stack needs from slurmctld: the OpenMetrics plugin, HTTP on
// its port, and metrics that need no authentication. The collector has no
// JWT and is not SlurmUser, so MetricsAuthUsers would lock it out, and so
// would PrivateData unless MetricsParameters lists ignore_private_data.
// The unmanaged rejection is in Validate, with the others of an unmanaged
// cluster.
func (c *Cluster) validateMonitoring() error {
	if !c.Monitoring || !c.Managed() {
		return nil
	}
	main := c.Slurm.Main
	if value, ok := main.Parameter("MetricsType"); ok && value != MetricsType {
		return fmt.Errorf("slurm main sets MetricsType=%s: monitoring needs %s", value, MetricsType)
	}
	if value, ok := main.Parameter("CommunicationParameters"); ok && ListsValue(value, "disable_http") {
		return fmt.Errorf("slurm main sets CommunicationParameters=%s: monitoring needs slurmctld's HTTP endpoints, which disable_http turns off", value)
	}
	if main.SetsParameter("MetricsAuthUsers") {
		return fmt.Errorf("slurm main sets MetricsAuthUsers: monitoring reads slurmctld's metrics without a token")
	}
	if main.SetsParameter("PrivateData") {
		params, _ := main.Parameter("MetricsParameters")
		if !ListsValue(params, "ignore_private_data") {
			return fmt.Errorf("slurm main sets PrivateData, which hides slurmctld's metrics from monitoring: add MetricsParameters=ignore_private_data to slurm main")
		}
	}
	return nil
}
