// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- parseVersion ---

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "standard output",
			input: "slurm 25.11.0\n",
			want:  "25.11.0",
		},
		{
			name:  "patch version",
			input: "slurm 25.11.2\n",
			want:  "25.11.2",
		},
		{
			name:  "no trailing newline",
			input: "slurm 25.11.0",
			want:  "25.11.0",
		},
		{
			name:    "empty output",
			input:   "",
			wantErr: "unexpected slurmctld -V output",
		},
		{
			name:    "wrong prefix",
			input:   "SLURM 25.11.0\n",
			wantErr: "unexpected slurmctld -V output",
		},
		{
			name:    "missing version",
			input:   "slurm \n",
			wantErr: "unexpected slurmctld -V output",
		},
		{
			name:    "single word",
			input:   "slurm\n",
			wantErr: "unexpected slurmctld -V output",
		},
		{
			name:  "pre-release",
			input: "slurm 26.05.0-0rc1\n",
			want:  "26.05.0-0rc1",
		},
		{
			name:    "two numbers",
			input:   "slurm 25.11\n",
			wantErr: "unexpected slurmctld -V output",
		},
		{
			name:    "trailing words",
			input:   "slurm 25.11.0 (patched)\n",
			wantErr: "unexpected slurmctld -V output",
		},
		{
			// An image that writes the clipboard (OSC 52) through the
			// version sind prints in get clusters.
			name:    "control sequence",
			input:   "slurm 25.11.0\x1b]52;c;cm0gLXJmIH4K\x07\n",
			wantErr: "unexpected slurmctld -V output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVersion(tt.input)
			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Empty(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// --- DiscoverVersion ---

func TestDiscoverVersionLifecycle(t *testing.T) {
	t.Parallel()
	c, rec := testutil.NewClient(t)
	image := "ghcr.io/gsi-hpc/sind-node:latest"

	if !rec.IsIntegration() {
		rec.AddResult("slurm 25.11.8\n", "", nil)
	}

	// The image may carry any supported Slurm release line.
	version, err := DiscoverVersion(t.Context(), c, image)
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9]+\.[0-9]+\.[0-9]+$`, version)

	t.Logf("docker I/O:\n%s", rec.Dump())
}

func TestDiscoverVersion(t *testing.T) {
	const image = "ghcr.io/gsi-hpc/sind-node:25.11"

	var m mock.Executor
	m.AddResult("slurm 25.11.0\n", "", nil)
	c := docker.NewClient(&m)

	version, err := DiscoverVersion(t.Context(), c, image)
	require.NoError(t, err)
	assert.Equal(t, "25.11.0", version)

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{"run", "--rm", image, "slurmctld", "-V"}, m.Calls[0].Args)
}

func TestDiscoverVersion_RunError(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "Unable to find image\n", fmt.Errorf("exit status 125"))
	c := docker.NewClient(&m)

	version, err := DiscoverVersion(t.Context(), c, "missing:latest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "running slurmctld -V")
	assert.Empty(t, version)
}

func TestDiscoverVersion_ParseError(t *testing.T) {
	var m mock.Executor
	m.AddResult("unexpected output\n", "", nil)
	c := docker.NewClient(&m)

	version, err := DiscoverVersion(t.Context(), c, "bad:image")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected slurmctld -V output")
	assert.Empty(t, version)
}
