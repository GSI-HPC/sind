// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDNSName(t *testing.T) {
	tests := []struct {
		name      string
		shortName string
		cluster   string
		want      string
	}{
		{"controller", "controller", "dev", "controller.dev.sind.local"},
		{"submitter", "submitter", "dev", "submitter.dev.sind.local"},
		{"worker-0", "worker-0", "dev", "worker-0.dev.sind.local"},
		{"worker-1", "worker-1", "dev", "worker-1.dev.sind.local"},
		{"default cluster", "controller", "default", "controller.default.sind.local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DNSName(tt.shortName, tt.cluster))
		})
	}
}

func TestDNSSearchDomain(t *testing.T) {
	tests := []struct {
		cluster string
		want    string
	}{
		{"dev", "dev.sind.local"},
		{"default", "default.sind.local"},
	}
	for _, tt := range tests {
		t.Run(tt.cluster, func(t *testing.T) {
			assert.Equal(t, tt.want, DNSSearchDomain(tt.cluster))
		})
	}
}
