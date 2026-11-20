// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// The tests of this file state what GSI-HPC/sind-action uses of sind: the
// contract that "sind-action" in CLAUDE.md lists. A change that fails one
// of them breaks the action for the next sind release: change sind-action
// first, so that it selects the old or the new form by `sind version
// --json`, and then this file. Not stated here: the redirect of
// releases/latest to the latest release, which GitHub serves.

// actionCommandLines are the command lines that sind-action runs, after
// "sind", and the command each one must reach.
var actionCommandLines = []struct {
	args    []string
	command string
	flags   map[string]string // the flags that the line sets, by name
	nargs   int               // its positional arguments
}{
	// scripts/install.sh and scripts/create.sh read .version of the output.
	{[]string{"version", "--json"}, "sind version", map[string]string{"json": "true"}, 0},
	// action.yml: SIND_VERBOSITY is -v, or -vvv under RUNNER_DEBUG.
	{[]string{"-v", "doctor"}, "sind doctor", map[string]string{"verbose": "1"}, 0},
	{[]string{"-vvv", "doctor"}, "sind doctor", map[string]string{"verbose": "3"}, 0},
	// scripts/create.sh: the name, --config, --wait from the wait input,
	// and --pull or --pull=false from the pull input.
	{[]string{"-v", "create", "cluster", "ci", "--config", "sind.yaml", "--pull"}, "sind create cluster",
		map[string]string{"config": "sind.yaml", "pull": "true"}, 1},
	{[]string{"-v", "create", "cluster", "ci", "--config", "sind.yaml", "--wait", "10m", "--pull=false"}, "sind create cluster",
		map[string]string{"config": "sind.yaml", "wait": "10m0s", "pull": "false"}, 1},
	{[]string{"-v", "get", "cluster", "ci"}, "sind get cluster", nil, 1},
	// cleanup/action.yml
	{[]string{"-v", "delete", "cluster", "--all"}, "sind delete cluster", map[string]string{"all": "true"}, 0},
}

// Every command line of sind-action reaches a command that runs, which
// takes its flags, with the values the line gives them, and its arguments.
func TestActionContract_CommandLines(t *testing.T) {
	for _, tc := range actionCommandLines {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd, rest, err := NewRootCommand().Traverse(tc.args)
			require.NoError(t, err)
			require.Equal(t, tc.command, cmd.CommandPath())
			require.True(t, cmd.Runnable())

			require.NoError(t, cmd.ParseFlags(rest))
			require.NoError(t, cmd.ValidateArgs(cmd.Flags().Args()))
			assert.Len(t, cmd.Flags().Args(), tc.nargs)
			for name, want := range tc.flags {
				flag := cmd.Flags().Lookup(name)
				require.NotNil(t, flag, "flag --%s", name)
				assert.True(t, flag.Changed, "flag --%s", name)
				assert.Equal(t, want, flag.Value.String(), "flag --%s", name)
			}
		})
	}
}

// sind-action reads the version of the installed sind from the field
// "version" of `sind version --json`, without the "v" of the tag, and
// compares it with sort -V.
func TestActionContract_VersionJSON(t *testing.T) {
	setVersion(t, "v1.2.3", "abc1234")

	stdout, _, err := executeCommand("version", "--json")
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &out))
	assert.Equal(t, "1.2.3", out["version"])
}

// sind-action exports SIND_REALM from its realm input, for the cluster
// commands of the job and for its cleanup. The variables that it sets for
// itself, which sind must not read, are in TestReservedBySindAction of
// internal/envvar.
func TestActionContract_Realm(t *testing.T) {
	assert.Equal(t, "SIND_REALM", envvar.Realm)
	assert.Contains(t, envvar.CommandLine, envvar.Realm)
	assert.NotNil(t, NewRootCommand().PersistentFlags().Lookup("realm"), "the flag that SIND_REALM stands in for")
}

// readYAML reads a YAML file of the repository, by its path from the
// root, into out.
func readYAML(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile("../../" + path)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, out))
}

// sind-action downloads the release assets sind-linux-amd64 or
// sind-linux-arm64 and checksums.txt, which has one sha256 line for each
// asset: GoReleaser builds these names.
func TestActionContract_ReleaseAssets(t *testing.T) {
	var cfg struct {
		Builds []struct {
			Binary string   `yaml:"binary"`
			GOOS   []string `yaml:"goos"`
			GOARCH []string `yaml:"goarch"`
		} `yaml:"builds"`
		Archives []struct {
			Formats      []string `yaml:"formats"`
			NameTemplate string   `yaml:"name_template"`
		} `yaml:"archives"`
		Checksum struct {
			NameTemplate string `yaml:"name_template"`
			Algorithm    string `yaml:"algorithm"`
		} `yaml:"checksum"`
	}
	readYAML(t, ".goreleaser.yaml", &cfg)

	require.Len(t, cfg.Builds, 1)
	assert.Equal(t, []string{"linux"}, cfg.Builds[0].GOOS)
	assert.Equal(t, []string{"amd64", "arm64"}, cfg.Builds[0].GOARCH)
	require.Len(t, cfg.Archives, 1)
	assert.Equal(t, []string{"binary"}, cfg.Archives[0].Formats, "the asset is the binary itself, not an archive of it")
	assert.Equal(t, "sind-{{ .Os }}-{{ .Arch }}", cfg.Archives[0].NameTemplate)
	assert.Equal(t, "checksums.txt", cfg.Checksum.NameTemplate)
	assert.Equal(t, "sha256", cfg.Checksum.Algorithm)
}

// With its verify input, sind-action checks the build provenance of the
// binary with gh attestation verify: that the Release workflow of this
// repository attested it, at the release tag, on a GitHub-hosted runner.
// The workflow thus runs for a pushed tag, on such a runner, and its
// attest step, not one of a workflow that it calls, attests every asset of
// checksums.txt.
func TestActionContract_Attestation(t *testing.T) {
	var workflow struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
		Jobs map[string]struct {
			RunsOn      string            `yaml:"runs-on"`
			Uses        string            `yaml:"uses"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	readYAML(t, ".github/workflows/release.yml", &workflow)

	assert.Equal(t, []string{"v*"}, workflow.On.Push.Tags)
	attested := 0
	for name, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/attest@") {
				continue
			}
			attested++
			assert.Equal(t, "dist/checksums.txt", step.With["subject-checksums"], "job %s", name)
			assert.Equal(t, "ubuntu-latest", job.RunsOn, "job %s runs on a GitHub-hosted runner", name)
			assert.Empty(t, job.Uses, "job %s is no call of another workflow", name)
			assert.Equal(t, "write", job.Permissions["attestations"], "job %s", name)
			assert.Equal(t, "write", job.Permissions["id-token"], "job %s", name)
		}
	}
	assert.Equal(t, 1, attested, "one step attests the assets")
}
