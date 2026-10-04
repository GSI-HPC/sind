// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryMB(t *testing.T) {
	// Docker's size syntax: binary units, optional b or ib, fractions,
	// plain bytes.
	for in, want := range map[string]int{
		"512m":       512,
		"512M":       512,
		"2g":         2048,
		"2G":         2048,
		"2gb":        2048,
		"2GB":        2048,
		"2GiB":       2048,
		"1.5g":       1536,
		".5g":        512,
		"8192k":      8,
		"6291456":    6,
		"6291456b":   6,
		"1t":         1 << 20,
		"1p":         1 << 30,
		"1073741824": 1024,
		"5.9999g":    6143, // rounded down
	} {
		got, err := MemoryMB(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
}

func TestMemoryMB_Invalid(t *testing.T) {
	for in, wantErr := range map[string]string{
		"":        "want a number",
		"abc":     "want a number",
		"2x":      "want a number",
		"g":       "want a number",
		"xm":      "want a number",
		"1.5.5g":  "want a number",
		"-1g":     "want a number",
		"2 g":     "want a number",
		"2gbb":    "want a number",
		"2ib":     "want a number",
		"1e3m":    "want a number",
		"0":       "below Docker's minimum of 6m",
		"0g":      "below Docker's minimum of 6m",
		"5m":      "below Docker's minimum of 6m",
		"6291455": "below Docker's minimum of 6m",
		"8192p":   "too large",
	} {
		_, err := MemoryMB(in)
		require.Error(t, err, in)
		assert.Contains(t, err.Error(), wantErr, in)
	}
}

func TestCheckMemory(t *testing.T) {
	require.NoError(t, CheckMemory("--memory", ""))
	require.NoError(t, CheckMemory("--memory", "1.5g"))
	require.EqualError(t, CheckMemory("--memory", "2x"),
		`invalid --memory "2x": want a number with an optional unit b, k, m, g, t or p, e.g. 512m or 1.5g`)
}

func TestCheckTmpSize(t *testing.T) {
	for _, s := range []string{"", "0", "256m", "1G", "1048576", "2t", "50%"} {
		require.NoError(t, CheckTmpSize("tmpSize", s), s)
	}
	for _, s := range []string{"1.5g", "1gb", "256m,exec", "-1m", "m", "50%m"} {
		assert.EqualError(t, CheckTmpSize("tmpSize", s),
			`invalid tmpSize "`+s+`": want a whole number with an optional unit k, m, g, t, p or e, or a percentage, e.g. 256m`, s)
	}
}

func TestCheckCPUs(t *testing.T) {
	require.NoError(t, CheckCPUs("--cpus", 0))
	require.NoError(t, CheckCPUs("--cpus", 4))
	require.EqualError(t, CheckCPUs("--cpus", -2), "--cpus must not be negative, got -2")
}

func TestValidate_Resources(t *testing.T) {
	valid := func() *Cluster {
		return &Cluster{Kind: "Cluster", Name: "default", Nodes: []Node{{Role: RoleController}, {Role: RoleWorker}}}
	}
	for _, tt := range []struct {
		name    string
		change  func(*Cluster)
		wantErr string
	}{
		{"defaults cpus", func(c *Cluster) { c.Defaults.CPUs = -1 }, "defaults.cpus must not be negative, got -1"},
		{"defaults memory", func(c *Cluster) { c.Defaults.Memory = "2x" }, `invalid defaults.memory "2x"`},
		{"defaults tmpSize", func(c *Cluster) { c.Defaults.TmpSize = "1.5g" }, `invalid defaults.tmpSize "1.5g"`},
		{"node cpus", func(c *Cluster) { c.Nodes[1].CPUs = -2 }, "worker cpus must not be negative, got -2"},
		{"node memory", func(c *Cluster) { c.Nodes[1].Memory = "1g2" }, `invalid worker memory "1g2"`},
		{"node tmpSize", func(c *Cluster) { c.Nodes[0].TmpSize = "1g,exec" }, `invalid controller tmpSize "1g,exec"`},
	} {
		c := valid()
		tt.change(c)
		c.ApplyDefaults()
		err := c.Validate()
		require.Error(t, err, tt.name)
		assert.Contains(t, err.Error(), tt.wantErr, tt.name)
	}

	// Docker's other spellings are valid.
	c := valid()
	c.Defaults.Memory = "1.5GiB"
	c.Nodes[1].Memory = "2gb"
	c.Nodes[1].CPUs = 0
	c.ApplyDefaults()
	require.NoError(t, c.Validate())
}
