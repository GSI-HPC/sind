// SPDX-License-Identifier: LGPL-3.0-or-later

package nodefold

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWords(t *testing.T) {
	for name, tc := range map[string]struct {
		names, want []string
	}{
		"none":       {nil, nil},
		"one":        {[]string{"worker-2"}, []string{"worker-2"}},
		"a range":    {[]string{"worker-2", "worker-0", "worker-1"}, []string{"worker-[0-2]"}},
		"one set":    {[]string{"worker-4", "controller", "worker-0"}, []string{"controller,worker-[0,4]"}},
		"twice":      {[]string{"w2", "w2"}, []string{"w2"}},
		"a cluster":  {[]string{"worker-1.dev", "controller.dev", "worker-0.dev"}, []string{"controller.dev,worker-[0-1].dev"}},
		"two of one": {[]string{"worker-0.dev1", "worker-0.dev2"}, []string{"worker-0.dev1", "worker-0.dev2"}},
		"apart": {
			[]string{"worker-1.dev", "worker-0", "worker-0.dev", "worker-1"},
			[]string{"worker-[0-1].dev", "worker-[0-1]"},
		},
		"no fold back": {[]string{"w1", "w01"}, []string{"w1", "w01"}},
		"no node set":  {[]string{"-", "w1"}, []string{"-", "w1"}},
		"an image":     {[]string{"ghcr.io/gsi-hpc/sind-node:26.05", "worker-0"}, []string{"ghcr.io/gsi-hpc/sind-node:26.05", "worker-0"}},
	} {
		assert.Equal(t, tc.want, Words(tc.names), name)
	}
}

func TestOfAndSet(t *testing.T) {
	assert.Equal(t, []string{"worker-[0-3].dev"}, Of("dev", []string{"worker-3", "worker-0", "worker-1", "worker-2"}))
	assert.Equal(t, []string{"controller,worker-[0-1]"}, Of("default", []string{"worker-1", "controller", "worker-0"}))
	assert.Equal(t, "worker-[0-1].dev", Set("dev", []string{"worker-0", "worker-1"}))
	assert.Equal(t, "w1,w01", Set("default", []string{"w1", "w01"}))
	assert.Empty(t, Set("default", nil))
}
