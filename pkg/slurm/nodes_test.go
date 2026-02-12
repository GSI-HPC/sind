// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"testing"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/stretchr/testify/assert"
)

func boolPtr(b bool) *bool { return &b }

func TestGenerateNodesConf_Minimal(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "2g"},
		{Role: "compute", CPUs: 2, Memory: "2g"},
	}

	conf := GenerateNodesConf(nodes)

	assert.Contains(t, conf, "NodeName=compute-0 CPUs=2 RealMemory=2048 State=UNKNOWN")
	assert.Contains(t, conf, "PartitionName=all Nodes=compute-0 Default=YES MaxTime=INFINITE State=UP")
}

func TestGenerateNodesConf_MultiCompute(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "4g"},
		{Role: "compute", Count: 3, CPUs: 4, Memory: "8g"},
	}

	conf := GenerateNodesConf(nodes)

	assert.Contains(t, conf, "NodeName=compute-0 CPUs=4 RealMemory=8192 State=UNKNOWN")
	assert.Contains(t, conf, "NodeName=compute-1 CPUs=4 RealMemory=8192 State=UNKNOWN")
	assert.Contains(t, conf, "NodeName=compute-2 CPUs=4 RealMemory=8192 State=UNKNOWN")
	assert.Contains(t, conf, "PartitionName=all Nodes=compute-0,compute-1,compute-2 Default=YES MaxTime=INFINITE State=UP")
}

func TestGenerateNodesConf_MultipleGroups(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "2g"},
		{Role: "compute", Count: 2, CPUs: 2, Memory: "2g"},
		{Role: "compute", Count: 2, CPUs: 4, Memory: "8g"},
	}

	conf := GenerateNodesConf(nodes)

	assert.Contains(t, conf, "NodeName=compute-0 CPUs=2 RealMemory=2048 State=UNKNOWN")
	assert.Contains(t, conf, "NodeName=compute-1 CPUs=2 RealMemory=2048 State=UNKNOWN")
	assert.Contains(t, conf, "NodeName=compute-2 CPUs=4 RealMemory=8192 State=UNKNOWN")
	assert.Contains(t, conf, "NodeName=compute-3 CPUs=4 RealMemory=8192 State=UNKNOWN")
	assert.Contains(t, conf, "Nodes=compute-0,compute-1,compute-2,compute-3")
}

func TestGenerateNodesConf_SkipsUnmanaged(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "2g"},
		{Role: "compute", Count: 2, CPUs: 2, Memory: "2g"},
		{Role: "compute", Count: 2, CPUs: 2, Memory: "2g", Managed: boolPtr(false)},
	}

	conf := GenerateNodesConf(nodes)

	assert.Contains(t, conf, "NodeName=compute-0")
	assert.Contains(t, conf, "NodeName=compute-1")
	assert.NotContains(t, conf, "compute-2")
	assert.NotContains(t, conf, "compute-3")
	assert.Contains(t, conf, "Nodes=compute-0,compute-1")
}

func TestGenerateNodesConf_SkipsNonCompute(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "2g"},
		{Role: "submitter", CPUs: 2, Memory: "2g"},
		{Role: "compute", CPUs: 2, Memory: "2g"},
	}

	conf := GenerateNodesConf(nodes)

	assert.NotContains(t, conf, "controller")
	assert.NotContains(t, conf, "submitter")
	assert.Contains(t, conf, "NodeName=compute-0")
}

func TestGenerateNodesConf_AllUnmanaged(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "2g"},
		{Role: "compute", Count: 2, CPUs: 2, Memory: "2g", Managed: boolPtr(false)},
	}

	conf := GenerateNodesConf(nodes)

	assert.NotContains(t, conf, "NodeName=")
	assert.NotContains(t, conf, "PartitionName=")
}

func TestGenerateNodesConf_ExplicitManaged(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "2g"},
		{Role: "compute", CPUs: 2, Memory: "2g", Managed: boolPtr(true)},
	}

	conf := GenerateNodesConf(nodes)

	assert.Contains(t, conf, "NodeName=compute-0")
	assert.Contains(t, conf, "PartitionName=all Nodes=compute-0")
}

func TestGenerateNodesConf_EmptyInput(t *testing.T) {
	conf := GenerateNodesConf(nil)

	assert.NotContains(t, conf, "NodeName=")
	assert.NotContains(t, conf, "PartitionName=")
}

func TestGenerateNodesConf_MemoryMB(t *testing.T) {
	nodes := []config.Node{
		{Role: "controller", CPUs: 2, Memory: "2g"},
		{Role: "compute", CPUs: 2, Memory: "512m"},
	}

	conf := GenerateNodesConf(nodes)

	assert.Contains(t, conf, "RealMemory=512")
}

// --- parseMemoryMB ---

func TestParseMemoryMB(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"2g", 2048},
		{"4g", 4096},
		{"512m", 512},
		{"1024m", 1024},
		{"2G", 2048},
		{"512M", 512},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseMemoryMB(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseMemoryMB_Zero(t *testing.T) {
	got, err := parseMemoryMB("0g")
	assert.NoError(t, err)
	assert.Equal(t, 0, got)

	got, err = parseMemoryMB("0m")
	assert.NoError(t, err)
	assert.Equal(t, 0, got)
}

func TestParseMemoryMB_Invalid(t *testing.T) {
	tests := []string{"", "abc", "2x", "g", "xm"}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := parseMemoryMB(input)
			assert.Error(t, err)
		})
	}
}
