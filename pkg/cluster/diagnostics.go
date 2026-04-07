// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// logTailLines is the number of container log lines captured during cleanup.
const logTailLines = 20

// logContainerDiagnostics inspects a container and logs its state, exit code,
// and last log lines at error level. This is called before removing failed
// containers so the cause of failure is preserved.
func logContainerDiagnostics(ctx context.Context, client *docker.Client, name docker.ContainerName) {
	log := sindlog.From(ctx)

	info, err := client.InspectContainer(ctx, name)
	if err != nil {
		return
	}
	if info.Status == docker.StateRunning {
		return
	}

	attrs := []any{
		"container", string(name),
		"status", string(info.Status),
		"exitCode", info.ExitCode,
	}
	if info.OOMKilled {
		attrs = append(attrs, "oomKilled", true)
	}

	logs, err := client.Logs(ctx, name, logTailLines)
	if err == nil && strings.TrimSpace(logs) != "" {
		attrs = append(attrs, "logs", strings.TrimSpace(logs))
	}

	log.ErrorContext(ctx, "container failed", attrs...)
}

// logClusterDiagnostics logs diagnostics for all non-running containers in a
// cluster. Called before cleanup removes them so that failures can be diagnosed.
func logClusterDiagnostics(ctx context.Context, client *docker.Client, realm, clusterName string) {
	containers, err := client.ListContainers(ctx,
		"label="+LabelRealm+"="+realm,
		"label="+LabelCluster+"="+clusterName,
	)
	if err != nil {
		return
	}
	for _, c := range containers {
		logContainerDiagnostics(ctx, client, c.Name)
	}
}
