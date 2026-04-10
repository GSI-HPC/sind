// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"testing"

	"github.com/GSI-HPC/sind/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_MinimalYAML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
	}{
		{
			name:     "kind only",
			input:    "kind: Cluster",
			wantName: "default",
		},
		{
			name:     "kind with explicit name",
			input:    "kind: Cluster\nname: dev",
			wantName: "dev",
		},
		{
			name:     "kind with explicit default name",
			input:    "kind: Cluster\nname: default",
			wantName: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.input))
			require.NoError(t, err)
			assert.Equal(t, "Cluster", cfg.Kind)
			assert.Equal(t, tt.wantName, cfg.Name)
		})
	}
}

func TestParse_WithDefaults(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantImage string
		wantCPUs  int
		wantMem   string
		wantTmp   string
	}{
		{
			name: "all defaults specified",
			input: `kind: Cluster
defaults:
  image: ghcr.io/gsi-hpc/sind-node:25.11.2
  cpus: 4
  memory: 8g
  tmpSize: 2g`,
			wantImage: "ghcr.io/gsi-hpc/sind-node:25.11.2",
			wantCPUs:  4,
			wantMem:   "8g",
			wantTmp:   "2g",
		},
		{
			name: "partial defaults",
			input: `kind: Cluster
defaults:
  image: custom:latest`,
			wantImage: "custom:latest",
		},
		{
			name:  "no defaults section",
			input: "kind: Cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.input))
			require.NoError(t, err)
			if tt.wantImage != "" {
				assert.Equal(t, tt.wantImage, cfg.Defaults.Image)
			}
			if tt.wantCPUs != 0 {
				assert.Equal(t, tt.wantCPUs, cfg.Defaults.CPUs)
			}
			if tt.wantMem != "" {
				assert.Equal(t, tt.wantMem, cfg.Defaults.Memory)
			}
			if tt.wantTmp != "" {
				assert.Equal(t, tt.wantTmp, cfg.Defaults.TmpSize)
			}
		})
	}
}

func TestParse_NodesFullForm(t *testing.T) {
	input := `kind: Cluster
nodes:
  - role: controller
    cpus: 2
    memory: 4g
    tmpSize: 2g
  - role: submitter
  - role: worker
    count: 3
    cpus: 4
    memory: 8g
  - role: worker
    count: 2
    managed: false`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 4)

	// controller
	assert.Equal(t, RoleController, cfg.Nodes[0].Role)
	assert.Equal(t, 2, cfg.Nodes[0].CPUs)
	assert.Equal(t, "4g", cfg.Nodes[0].Memory)
	assert.Equal(t, "2g", cfg.Nodes[0].TmpSize)

	// submitter
	assert.Equal(t, RoleSubmitter, cfg.Nodes[1].Role)

	// managed worker
	assert.Equal(t, RoleWorker, cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
	assert.Equal(t, 4, cfg.Nodes[2].CPUs)
	assert.Equal(t, "8g", cfg.Nodes[2].Memory)
	assert.Nil(t, cfg.Nodes[2].Managed)

	// unmanaged worker
	assert.Equal(t, RoleWorker, cfg.Nodes[3].Role)
	assert.Equal(t, 2, cfg.Nodes[3].Count)
	require.NotNil(t, cfg.Nodes[3].Managed)
	assert.False(t, *cfg.Nodes[3].Managed)
}

func TestParse_NodesShorthand(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - submitter
  - worker: 3`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 3)

	assert.Equal(t, RoleController, cfg.Nodes[0].Role)
	assert.Equal(t, 0, cfg.Nodes[0].Count)

	assert.Equal(t, RoleSubmitter, cfg.Nodes[1].Role)

	assert.Equal(t, RoleWorker, cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
}

func TestParse_NodesMixed(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - role: worker
    count: 3
    cpus: 4
  - worker: 2`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 3)

	assert.Equal(t, RoleController, cfg.Nodes[0].Role)

	assert.Equal(t, RoleWorker, cfg.Nodes[1].Role)
	assert.Equal(t, 3, cfg.Nodes[1].Count)
	assert.Equal(t, 4, cfg.Nodes[1].CPUs)

	assert.Equal(t, RoleWorker, cfg.Nodes[2].Role)
	assert.Equal(t, 2, cfg.Nodes[2].Count)
}

func TestParse_Storage(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantType      StorageType
		wantHostPath  string
		wantMountPath string
	}{
		{
			name: "volume type",
			input: `kind: Cluster
storage:
  dataStorage:
    type: volume`,
			wantType: StorageVolume,
		},
		{
			name: "hostPath type",
			input: `kind: Cluster
storage:
  dataStorage:
    type: hostPath
    hostPath: ./data
    mountPath: /data`,
			wantType:      StorageHostPath,
			wantHostPath:  "./data",
			wantMountPath: "/data",
		},
		{
			name:  "no storage section",
			input: "kind: Cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tt.input))
			require.NoError(t, err)
			if tt.wantType != StorageType("") {
				assert.Equal(t, tt.wantType, cfg.Storage.DataStorage.Type)
			}
			if tt.wantHostPath != "" {
				assert.Equal(t, tt.wantHostPath, cfg.Storage.DataStorage.HostPath)
			}
			if tt.wantMountPath != "" {
				assert.Equal(t, tt.wantMountPath, cfg.Storage.DataStorage.MountPath)
			}
		})
	}
}

func TestApplyDefaults_MinimalConfig(t *testing.T) {
	cfg := &Cluster{Kind: "Cluster", Name: "default"}
	cfg.ApplyDefaults()

	require.Len(t, cfg.Nodes, 2)
	assert.Equal(t, RoleController, cfg.Nodes[0].Role)
	assert.Equal(t, RoleWorker, cfg.Nodes[1].Role)
}

func TestApplyDefaults_InheritsGlobalDefaults(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Defaults: Defaults{
			Image:   "custom:latest",
			CPUs:    4,
			Memory:  "8g",
			TmpSize: "2g",
		},
		Nodes: []Node{
			{Role: RoleController},
			{Role: RoleWorker},
		},
	}
	cfg.ApplyDefaults()

	for _, n := range cfg.Nodes {
		assert.Equal(t, "custom:latest", n.Image, "node %s", n.Role)
		assert.Equal(t, 4, n.CPUs, "node %s", n.Role)
		assert.Equal(t, "8g", n.Memory, "node %s", n.Role)
		assert.Equal(t, "2g", n.TmpSize, "node %s", n.Role)
	}
}

func TestApplyDefaults_NodeOverridesGlobal(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Defaults: Defaults{
			Image:  "default:latest",
			CPUs:   2,
			Memory: "4g",
		},
		Nodes: []Node{
			{Role: RoleController, CPUs: 8},
			{Role: RoleWorker, Image: "special:latest"},
		},
	}
	cfg.ApplyDefaults()

	// controller: overrides CPUs, inherits rest
	assert.Equal(t, "default:latest", cfg.Nodes[0].Image)
	assert.Equal(t, 8, cfg.Nodes[0].CPUs)
	assert.Equal(t, "4g", cfg.Nodes[0].Memory)

	// worker: overrides image, inherits rest
	assert.Equal(t, "special:latest", cfg.Nodes[1].Image)
	assert.Equal(t, 2, cfg.Nodes[1].CPUs)
	assert.Equal(t, "4g", cfg.Nodes[1].Memory)
}

func TestApplyDefaults_BuiltinDefaults(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Nodes: []Node{
			{Role: RoleController},
			{Role: RoleWorker},
		},
	}
	cfg.ApplyDefaults()

	for _, n := range cfg.Nodes {
		assert.Equal(t, "ghcr.io/gsi-hpc/sind-node:latest", n.Image, "node %s", n.Role)
		assert.Equal(t, 1, n.CPUs, "node %s", n.Role)
		assert.Equal(t, "512m", n.Memory, "node %s", n.Role)
		assert.Equal(t, "256m", n.TmpSize, "node %s", n.Role)
	}
}

func TestValidate_Valid(t *testing.T) {
	tests := []struct {
		name  string
		nodes []Node
	}{
		{
			name: "controller and worker",
			nodes: []Node{
				{Role: RoleController},
				{Role: RoleWorker},
			},
		},
		{
			name: "controller, submitter, and worker",
			nodes: []Node{
				{Role: RoleController},
				{Role: RoleSubmitter},
				{Role: RoleWorker},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{
				Kind:  "Cluster",
				Name:  "default",
				Nodes: tt.nodes,
			}
			require.NoError(t, cfg.Validate())
		})
	}
}

func TestValidate_RequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []Node
		wantErr string
	}{
		{
			name: "missing controller",
			nodes: []Node{
				{Role: RoleWorker},
			},
			wantErr: "exactly one controller",
		},
		{
			name: "multiple controllers",
			nodes: []Node{
				{Role: RoleController},
				{Role: RoleController},
				{Role: RoleWorker},
			},
			wantErr: "exactly one controller",
		},
		{
			name: "no worker",
			nodes: []Node{
				{Role: RoleController},
			},
			wantErr: "at least one worker",
		},
		{
			name:    "empty nodes",
			nodes:   []Node{},
			wantErr: "exactly one controller",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{
				Kind:  "Cluster",
				Name:  "default",
				Nodes: tt.nodes,
			}
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidate_Constraints(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []Node
		wantErr string
	}{
		{
			name: "multiple submitters",
			nodes: []Node{
				{Role: RoleController},
				{Role: RoleSubmitter},
				{Role: RoleSubmitter},
				{Role: RoleWorker},
			},
			wantErr: "at most one submitter",
		},
		{
			name: "count on controller",
			nodes: []Node{
				{Role: RoleController, Count: 2},
				{Role: RoleWorker},
			},
			wantErr: "count is only valid for worker",
		},
		{
			name: "count on submitter",
			nodes: []Node{
				{Role: RoleController},
				{Role: RoleSubmitter, Count: 2},
				{Role: RoleWorker},
			},
			wantErr: "count is only valid for worker",
		},
		{
			name: "invalid role",
			nodes: []Node{
				{Role: RoleController},
				{Role: "compute"},
				{Role: RoleWorker},
			},
			wantErr: `invalid role "compute"`,
		},
		{
			name: "managed false on non-worker",
			nodes: []Node{
				{Role: RoleController, Managed: testutil.Ptr(false)},
				{Role: RoleWorker},
			},
			wantErr: "managed is only valid for worker",
		},
		{
			name: "managed true on non-worker",
			nodes: []Node{
				{Role: RoleController, Managed: testutil.Ptr(true)},
				{Role: RoleWorker},
			},
			wantErr: "managed is only valid for worker",
		},
		{
			name: "negative count",
			nodes: []Node{
				{Role: RoleController},
				{Role: RoleWorker, Count: -1},
			},
			wantErr: "count must not be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Cluster{
				Kind:  "Cluster",
				Name:  "default",
				Nodes: tt.nodes,
			}
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "empty input",
			input:   "",
			wantErr: "empty configuration",
		},
		{
			name:    "invalid YAML",
			input:   "{{invalid",
			wantErr: "parsing config",
		},
		{
			name:    "missing kind",
			input:   "name: dev",
			wantErr: `kind must be "Cluster"`,
		},
		{
			name:    "wrong kind",
			input:   "kind: Pod",
			wantErr: `kind must be "Cluster"`,
		},
		{
			name: "node is neither string nor object",
			input: `kind: Cluster
nodes:
  - [1, 2]`,
			wantErr: "node must be a string",
		},
		{
			name: "node full form with invalid field type",
			input: `kind: Cluster
nodes:
  - role: controller
    cpus: not-a-number`,
			wantErr: "parsing config",
		},
		{
			name: "shorthand node with multiple keys",
			input: `kind: Cluster
nodes:
  - worker: 3
    memory: 4g`,
			wantErr: "shorthand node must have exactly one key",
		},
		{
			name: "shorthand node with non-integer count",
			input: `kind: Cluster
nodes:
  - worker: abc`,
			wantErr: "shorthand node count",
		},
		{
			name:    "unknown top-level field",
			input:   "kind: Cluster\nunknownField: value",
			wantErr: "unknown field",
		},
		{
			name: "unknown node field",
			input: `kind: Cluster
nodes:
  - role: controller
    unknownField: value`,
			wantErr: "unknown field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParse_ShorthandZeroCount(t *testing.T) {
	input := `kind: Cluster
nodes:
  - controller
  - worker: 0`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 2)
	assert.Equal(t, RoleWorker, cfg.Nodes[1].Role)
	assert.Equal(t, 0, cfg.Nodes[1].Count)
}

func TestParse_Pipeline(t *testing.T) {
	input := `kind: Cluster
name: test
defaults:
  image: custom:latest
  cpus: 4
nodes:
  - controller
  - submitter
  - worker: 3`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)

	cfg.ApplyDefaults()
	require.NoError(t, cfg.Validate())

	assert.Equal(t, "test", cfg.Name)
	require.Len(t, cfg.Nodes, 3)
	assert.Equal(t, RoleController, cfg.Nodes[0].Role)
	assert.Equal(t, "custom:latest", cfg.Nodes[0].Image)
	assert.Equal(t, 4, cfg.Nodes[0].CPUs)
	assert.Equal(t, "512m", cfg.Nodes[0].Memory)
	assert.Equal(t, RoleSubmitter, cfg.Nodes[1].Role)
	assert.Equal(t, RoleWorker, cfg.Nodes[2].Role)
	assert.Equal(t, 3, cfg.Nodes[2].Count)
}

func TestParse_Realm(t *testing.T) {
	t.Run("omitted defaults to empty", func(t *testing.T) {
		cfg, err := Parse([]byte("kind: Cluster"))
		require.NoError(t, err)
		assert.Equal(t, "", cfg.Realm)
	})

	t.Run("explicit realm", func(t *testing.T) {
		cfg, err := Parse([]byte("kind: Cluster\nrealm: myrealm"))
		require.NoError(t, err)
		assert.Equal(t, "myrealm", cfg.Realm)
	})

	t.Run("realm with name", func(t *testing.T) {
		cfg, err := Parse([]byte("kind: Cluster\nname: dev\nrealm: testrealm"))
		require.NoError(t, err)
		assert.Equal(t, "dev", cfg.Name)
		assert.Equal(t, "testrealm", cfg.Realm)
	})
}

// --- Section type ---

func TestSection_UnmarshalJSON(t *testing.T) {
	t.Run("string form", func(t *testing.T) {
		var s Section
		err := s.UnmarshalJSON([]byte(`"SchedulerType=sched/backfill\n"`))
		require.NoError(t, err)
		assert.Equal(t, "SchedulerType=sched/backfill\n", s.Content)
		assert.Nil(t, s.Fragments)
	})

	t.Run("map form", func(t *testing.T) {
		var s Section
		err := s.UnmarshalJSON([]byte(`{"scheduling":"SchedulerType=sched/backfill\n","resources":"SelectType=select/cons_tres\n"}`))
		require.NoError(t, err)
		assert.Empty(t, s.Content)
		require.Len(t, s.Fragments, 2)
		assert.Equal(t, "SchedulerType=sched/backfill\n", s.Fragments["scheduling"])
		assert.Equal(t, "SelectType=select/cons_tres\n", s.Fragments["resources"])
	})

	t.Run("null", func(t *testing.T) {
		var s Section
		err := s.UnmarshalJSON([]byte(`null`))
		require.NoError(t, err)
		assert.True(t, s.IsEmpty())
	})

	t.Run("invalid type", func(t *testing.T) {
		var s Section
		err := s.UnmarshalJSON([]byte(`42`))
		require.Error(t, err)
	})
}

func TestSection_IsEmpty(t *testing.T) {
	assert.True(t, Section{}.IsEmpty())
	assert.False(t, Section{Content: "x"}.IsEmpty())
	assert.False(t, Section{Fragments: map[string]string{"a": "b"}}.IsEmpty())
}

func TestSection_IsMap(t *testing.T) {
	assert.False(t, Section{}.IsMap())
	assert.False(t, Section{Content: "x"}.IsMap())
	assert.True(t, Section{Fragments: map[string]string{"a": "b"}}.IsMap())
}

func TestSection_FragmentNames(t *testing.T) {
	s := Section{Fragments: map[string]string{
		"zebra":  "z",
		"alpha":  "a",
		"middle": "m",
	}}
	assert.Equal(t, []string{"alpha", "middle", "zebra"}, s.FragmentNames())
}

func TestSection_FragmentNamesEmpty(t *testing.T) {
	assert.Empty(t, Section{}.FragmentNames())
	assert.Empty(t, Section{Content: "x"}.FragmentNames())
}

// --- Parse Slurm sections ---

func TestParse_Slurm(t *testing.T) {
	t.Run("main string form", func(t *testing.T) {
		input := `kind: Cluster
slurm:
  main: |
    SchedulerType=sched/backfill`

		cfg, err := Parse([]byte(input))
		require.NoError(t, err)
		assert.Contains(t, cfg.Slurm.Main.Content, "SchedulerType=sched/backfill")
	})

	t.Run("main map form", func(t *testing.T) {
		input := `kind: Cluster
slurm:
  main:
    scheduling: |
      SchedulerType=sched/backfill
    resources: |
      SelectType=select/cons_tres`

		cfg, err := Parse([]byte(input))
		require.NoError(t, err)
		require.Len(t, cfg.Slurm.Main.Fragments, 2)
		assert.Contains(t, cfg.Slurm.Main.Fragments["scheduling"], "SchedulerType=sched/backfill")
		assert.Contains(t, cfg.Slurm.Main.Fragments["resources"], "SelectType=select/cons_tres")
	})

	t.Run("cgroup string form", func(t *testing.T) {
		input := `kind: Cluster
slurm:
  cgroup: |
    ConstrainCores=yes`

		cfg, err := Parse([]byte(input))
		require.NoError(t, err)
		assert.Contains(t, cfg.Slurm.Cgroup.Content, "ConstrainCores=yes")
	})

	t.Run("gres map form", func(t *testing.T) {
		input := `kind: Cluster
slurm:
  gres:
    gpu: |
      Name=gpu Type=tesla File=/dev/nvidia0`

		cfg, err := Parse([]byte(input))
		require.NoError(t, err)
		require.Len(t, cfg.Slurm.Gres.Fragments, 1)
		assert.Contains(t, cfg.Slurm.Gres.Fragments["gpu"], "Name=gpu")
	})

	t.Run("multiple sections", func(t *testing.T) {
		input := `kind: Cluster
slurm:
  main: |
    SchedulerType=sched/backfill
  cgroup: |
    ConstrainCores=yes
  plugstack:
    pbs: |
      optional /usr/lib/slurm/spank_pbs.so`

		cfg, err := Parse([]byte(input))
		require.NoError(t, err)
		assert.NotEmpty(t, cfg.Slurm.Main.Content)
		assert.NotEmpty(t, cfg.Slurm.Cgroup.Content)
		require.Len(t, cfg.Slurm.Plugstack.Fragments, 1)
	})

	t.Run("no slurm section", func(t *testing.T) {
		cfg, err := Parse([]byte("kind: Cluster"))
		require.NoError(t, err)
		assert.True(t, cfg.Slurm.Main.IsEmpty())
		assert.True(t, cfg.Slurm.Cgroup.IsEmpty())
		assert.True(t, cfg.Slurm.Gres.IsEmpty())
		assert.True(t, cfg.Slurm.Topology.IsEmpty())
		assert.True(t, cfg.Slurm.Plugstack.IsEmpty())
	})
}

// --- Validate Slurm sections ---

func TestValidate_SlurmSections(t *testing.T) {
	base := func() *Cluster {
		return &Cluster{
			Kind: "Cluster",
			Name: "default",
			Nodes: []Node{
				{Role: "controller"},
				{Role: "worker"},
			},
		}
	}

	t.Run("valid string form", func(t *testing.T) {
		cfg := base()
		cfg.Slurm.Main = Section{Content: "SchedulerType=sched/backfill\n"}
		require.NoError(t, cfg.Validate())
	})

	t.Run("valid map form", func(t *testing.T) {
		cfg := base()
		cfg.Slurm.Main = Section{Fragments: map[string]string{
			"scheduling": "SchedulerType=sched/backfill\n",
		}}
		require.NoError(t, cfg.Validate())
	})

	t.Run("empty fragment name", func(t *testing.T) {
		cfg := base()
		cfg.Slurm.Main = Section{Fragments: map[string]string{
			"": "content",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not be empty")
	})

	t.Run("fragment name with path separator", func(t *testing.T) {
		cfg := base()
		cfg.Slurm.Cgroup = Section{Fragments: map[string]string{
			"../escape": "content",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plain filename")
	})

	t.Run("empty fragment content", func(t *testing.T) {
		cfg := base()
		cfg.Slurm.Gres = Section{Fragments: map[string]string{
			"gpu": "",
		}}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not be empty")
	})

	t.Run("all sections valid", func(t *testing.T) {
		cfg := base()
		cfg.Slurm.Main = Section{Content: "SchedulerType=sched/backfill\n"}
		cfg.Slurm.Cgroup = Section{Content: "ConstrainCores=yes\n"}
		cfg.Slurm.Gres = Section{Content: "Name=gpu Type=tesla\n"}
		cfg.Slurm.Topology = Section{Fragments: map[string]string{
			"switches": "SwitchName=s0 Nodes=worker-[0-3]\n",
		}}
		cfg.Slurm.Plugstack = Section{Fragments: map[string]string{
			"pbs": "optional /usr/lib/slurm/spank_pbs.so\n",
		}}
		require.NoError(t, cfg.Validate())
	})
}

// --- Security fields: capAdd, capDrop, devices, securityOpt ---

func TestParse_SecurityFields(t *testing.T) {
	input := `kind: Cluster
nodes:
  - role: controller
  - role: worker
    count: 3
    capAdd:
      - SYS_ADMIN
    devices:
      - /dev/fuse`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 2)

	assert.Empty(t, cfg.Nodes[0].CapAdd)
	assert.Equal(t, []string{"SYS_ADMIN"}, cfg.Nodes[1].CapAdd)
	assert.Equal(t, []string{"/dev/fuse"}, cfg.Nodes[1].Devices)
}

func TestParse_SecurityFieldsAllFields(t *testing.T) {
	input := `kind: Cluster
nodes:
  - role: controller
  - role: worker
    capAdd:
      - SYS_ADMIN
      - NET_ADMIN
    capDrop:
      - MKNOD
    devices:
      - /dev/fuse
      - /dev/sda:/dev/xvda:rwm
    securityOpt:
      - apparmor=unconfined`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	require.Len(t, cfg.Nodes, 2)

	w := cfg.Nodes[1]
	assert.Equal(t, []string{"SYS_ADMIN", "NET_ADMIN"}, w.CapAdd)
	assert.Equal(t, []string{"MKNOD"}, w.CapDrop)
	assert.Equal(t, []string{"/dev/fuse", "/dev/sda:/dev/xvda:rwm"}, w.Devices)
	assert.Equal(t, []string{"apparmor=unconfined"}, w.SecurityOpt)
}

func TestParse_SecurityFieldsInDefaults(t *testing.T) {
	input := `kind: Cluster
defaults:
  capAdd:
    - SYS_ADMIN
  devices:
    - /dev/fuse
nodes:
  - role: controller
  - role: worker`

	cfg, err := Parse([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, []string{"SYS_ADMIN"}, cfg.Defaults.CapAdd)
	assert.Equal(t, []string{"/dev/fuse"}, cfg.Defaults.Devices)
}

func TestApplyDefaults_SecurityFieldsMerge(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Defaults: Defaults{
			CapAdd:  []string{"SYS_ADMIN"},
			Devices: []string{"/dev/fuse"},
		},
		Nodes: []Node{
			{Role: RoleController},
			{Role: RoleWorker, CapAdd: []string{"NET_ADMIN"}, Devices: []string{"/dev/sda"}},
		},
	}
	cfg.ApplyDefaults()

	// Controller inherits defaults only
	assert.Equal(t, []string{"SYS_ADMIN"}, cfg.Nodes[0].CapAdd)
	assert.Equal(t, []string{"/dev/fuse"}, cfg.Nodes[0].Devices)

	// Worker merges defaults + per-node
	assert.Equal(t, []string{"SYS_ADMIN", "NET_ADMIN"}, cfg.Nodes[1].CapAdd)
	assert.Equal(t, []string{"/dev/fuse", "/dev/sda"}, cfg.Nodes[1].Devices)
}

func TestApplyDefaults_SecurityFieldsMergeDeduplicate(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Defaults: Defaults{
			CapAdd: []string{"SYS_ADMIN"},
		},
		Nodes: []Node{
			{Role: RoleController},
			{Role: RoleWorker, CapAdd: []string{"SYS_ADMIN", "NET_ADMIN"}},
		},
	}
	cfg.ApplyDefaults()

	// Duplicate SYS_ADMIN from defaults + node should be deduplicated
	assert.Equal(t, []string{"SYS_ADMIN", "NET_ADMIN"}, cfg.Nodes[1].CapAdd)
}

func TestApplyDefaults_SecurityFieldsEmpty(t *testing.T) {
	cfg := &Cluster{
		Kind: "Cluster",
		Name: "default",
		Nodes: []Node{
			{Role: RoleController},
			{Role: RoleWorker},
		},
	}
	cfg.ApplyDefaults()

	// No security fields set — should remain nil
	assert.Nil(t, cfg.Nodes[0].CapAdd)
	assert.Nil(t, cfg.Nodes[0].CapDrop)
	assert.Nil(t, cfg.Nodes[0].Devices)
	assert.Nil(t, cfg.Nodes[0].SecurityOpt)
}

func TestValidate_SecurityFields(t *testing.T) {
	base := func() *Cluster {
		return &Cluster{
			Kind: "Cluster",
			Name: "default",
			Nodes: []Node{
				{Role: "controller"},
				{Role: "worker"},
			},
		}
	}

	t.Run("valid capabilities", func(t *testing.T) {
		cfg := base()
		cfg.Nodes[1].CapAdd = []string{"SYS_ADMIN", "NET_ADMIN"}
		cfg.Nodes[1].CapDrop = []string{"MKNOD"}
		require.NoError(t, cfg.Validate())
	})

	t.Run("ALL capability", func(t *testing.T) {
		cfg := base()
		cfg.Nodes[1].CapAdd = []string{"ALL"}
		require.NoError(t, cfg.Validate())
	})

	t.Run("invalid capAdd", func(t *testing.T) {
		cfg := base()
		cfg.Nodes[1].CapAdd = []string{"INVALID_CAP"}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown capability "INVALID_CAP" in capAdd`)
	})

	t.Run("invalid capDrop", func(t *testing.T) {
		cfg := base()
		cfg.Nodes[1].CapDrop = []string{"NOT_A_CAP"}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown capability "NOT_A_CAP" in capDrop`)
	})

	t.Run("valid devices", func(t *testing.T) {
		cfg := base()
		cfg.Nodes[1].Devices = []string{"/dev/fuse", "/dev/sda:/dev/xvda:rwm"}
		require.NoError(t, cfg.Validate())
	})

	t.Run("device path not absolute", func(t *testing.T) {
		cfg := base()
		cfg.Nodes[1].Devices = []string{"dev/fuse"}
		err := cfg.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "device path must be absolute")
	})

	t.Run("valid security opts", func(t *testing.T) {
		cfg := base()
		cfg.Nodes[1].SecurityOpt = []string{"apparmor=unconfined"}
		require.NoError(t, cfg.Validate())
	})
}
