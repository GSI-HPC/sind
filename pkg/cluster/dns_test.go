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
		realm     string
		want      string
	}{
		{"controller", "controller", "dev", "sind", "controller.dev.sind.sind"},
		{"submitter", "submitter", "dev", "sind", "submitter.dev.sind.sind"},
		{"worker-0", "worker-0", "dev", "sind", "worker-0.dev.sind.sind"},
		{"worker-1", "worker-1", "dev", "sind", "worker-1.dev.sind.sind"},
		{"default cluster", "controller", "default", "sind", "controller.default.sind.sind"},
		{"custom realm", "worker-0", "dev", "ci-42", "worker-0.dev.ci-42.sind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DNSName(tt.shortName, tt.cluster, tt.realm))
		})
	}
}

func TestDNSSearchDomain(t *testing.T) {
	tests := []struct {
		name    string
		cluster string
		realm   string
		want    string
	}{
		{"default realm", "dev", "sind", "dev.sind.sind"},
		{"default cluster", "default", "sind", "default.sind.sind"},
		{"custom realm", "dev", "ci-42", "dev.ci-42.sind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DNSSearchDomain(tt.cluster, tt.realm))
		})
	}
}
