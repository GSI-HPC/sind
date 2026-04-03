// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

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
