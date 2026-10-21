// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// imagePin is the form of a pinned image: a repository and a version tag
// of digits, without a variant suffix such as -slim or -distroless.
var imagePin = regexp.MustCompile(`^[a-z0-9./-]+:v?[0-9]+\.[0-9]+\.[0-9]+$`)

func TestImages(t *testing.T) {
	images := Images()
	assert.Len(t, images, len(Services))
	for i, s := range Services {
		assert.Equal(t, s.Image(), images[i])
		assert.Regexp(t, imagePin, images[i])
		assert.NotContains(t, images[i], "latest")
	}
	assert.False(t, strings.HasSuffix(GrafanaImage, "-slim"))
}

func TestNames(t *testing.T) {
	assert.Equal(t, "dev-grafana", string(ContainerName("dev", ServiceGrafana)))
	assert.Equal(t, "dev-loki", string(VolumeName("dev", ServiceLoki)))
	assert.Equal(t, "dev-journal", string(JournalVolume("dev")))
	assert.Equal(t, "c1/worker-0", JournalSubpath("c1", "worker-0"))
	assert.Equal(t, "prometheus.dev.sind", DNSName("dev", ServicePrometheus))
}
