// SPDX-License-Identifier: LGPL-3.0-or-later

package nodefold

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/GSI-HPC/go-nodeset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestSharedNodeSets reads the cases of testdata/nodesets.json with
// go-nodeset, as the CLI reads a node argument. The test of the page script
// (internal/ui/jstest/nodeset.test.js, make test-js) reads the same cases
// with nodeset.js: a node set that a page writes names the same nodes for
// the CLI, and a text that a person types in a page does too.
func TestSharedNodeSets(t *testing.T) {
	raw, err := os.ReadFile("testdata/nodesets.json")
	require.NoError(t, err)
	var cases struct {
		Folds []struct {
			Names []string `json:"names"`
			Set   string   `json:"set"`
		} `json:"folds"`
		Reads []struct {
			Text  string   `json:"text"`
			Names []string `json:"names"`
		} `json:"reads"`
		Rejects []string `json:"rejects"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.NotEmpty(t, cases.Folds)
	require.NotEmpty(t, cases.Reads)
	require.NotEmpty(t, cases.Rejects)

	nodes := func(text string) []string {
		set, err := nodeset.Parse(text)
		require.NoError(t, err, text)
		return slices.Sorted(slices.Values(set.Expand()))
	}
	for _, c := range cases.Folds {
		assert.Equal(t, slices.Sorted(slices.Values(c.Names)), nodes(c.Set), c.Set)
	}
	for _, c := range cases.Reads {
		assert.Equal(t, slices.Sorted(slices.Values(c.Names)), nodes(c.Text), c.Text)
	}
	for _, text := range cases.Rejects {
		_, err := nodeset.Parse(text)
		assert.Error(t, err, text)
	}
}
