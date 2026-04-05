// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import "strconv"

// Docker Compose compatibility labels.
// See https://github.com/docker/compose/blob/main/pkg/api/labels.go
const (
	ComposeProjectLabel         = "com.docker.compose.project"
	ComposeServiceLabel         = "com.docker.compose.service"
	ComposeContainerNumberLabel = "com.docker.compose.container-number"
	ComposeOneoffLabel          = "com.docker.compose.oneoff"
	ComposeConfigHashLabel      = "com.docker.compose.config-hash"
	ComposeConfigFilesLabel     = "com.docker.compose.project.config_files"
	ComposeNetworkLabel         = "com.docker.compose.network"
	ComposeVolumeLabel          = "com.docker.compose.volume"
)

// ComposeLabels returns the standard Docker Compose compatibility labels
// for a container. These labels make sind containers appear as part of a
// compose project.
func ComposeLabels(project, service string, containerNumber int) map[string]string {
	return map[string]string{
		ComposeProjectLabel:         project,
		ComposeServiceLabel:         service,
		ComposeContainerNumberLabel: strconv.Itoa(containerNumber),
		ComposeOneoffLabel:          "False",
		ComposeConfigHashLabel:      "",
		ComposeConfigFilesLabel:     "",
	}
}
