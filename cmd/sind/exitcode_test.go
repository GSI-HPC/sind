// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noDocker returns a context whose docker client fails every call, so that
// a command that gets as far as docker exits 1, not 2.
func noDocker(ctx context.Context) context.Context {
	return withClient(ctx, docker.NewClient(&mock.Executor{}))
}

// TestRun_UsageErrorExits2 checks that a command line sind rejects exits 2,
// wherever it is rejected: by pflag while cobra traverses the parents
// (TraverseChildren) or parses the command's own flags, by an argument
// check, or by the command itself before it acts. It exited 1, like a
// command that ran and failed.
func TestRun_UsageErrorExits2(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--bogus", "get", "clusters"}, "unknown flag: --bogus"},
		{[]string{"--verbose=x", "version"}, `invalid argument "x" for "-v, --verbose" flag`},
		{[]string{"get", "clusters", "--bogus"}, "unknown flag: --bogus"},
		{[]string{"get", "clusters", "-o"}, "flag needs an argument"},
		{[]string{"get", "clusters", "---o"}, "bad flag syntax: ---o"},
		{[]string{"bogus"}, `unknown command "bogus" for "sind"`},
		{[]string{"get", "bogus"}, `unknown command "bogus" for "sind get"`},
		{[]string{"help", "bogus"}, `unknown help topic "bogus"`},
		{[]string{"completion", "fishy"}, `unknown command "fishy" for "sind completion"`},
		{[]string{"version", "extra"}, `unknown command "extra" for "sind version"`},
		{[]string{"get", "cluster", "a", "b"}, "accepts at most 1 arg(s), received 2"},
		{[]string{"get", "cluster", "Not_A_Name"}, `invalid cluster name "Not_A_Name"`},
		{[]string{"get", "clusters", "-o", "yaml"}, `invalid --output value "yaml"`},
		{[]string{"--realm", "Not_A_Realm", "get", "clusters"}, `--realm: invalid realm name "Not_A_Realm"`},
		{[]string{"get", "node", "worker-0.dev.sind.sind"}, "not the FQDN"},
		{[]string{"get", "node", "worker-0.Not_A_Name"}, `invalid node name "worker-0.Not_A_Name"`},
		{[]string{"power", "on", "worker-["}, "expanding nodes"},
		{[]string{"delete", "worker", ".dev"}, `invalid node name ".dev"`},
		{[]string{"delete", "cluster", "--all", "dev"}, "--all does not accept arguments"},
		{[]string{"logs", "worker-[0-1]"}, "logs requires exactly one node, got 2"},
		{[]string{"ssh"}, "node argument required"},
		{[]string{"ssh", "worker-[0-1]"}, "ssh requires exactly one node, got 2"},
		{[]string{"exec", "--bogus", "--", "true"}, "unknown flag: --bogus"},
		{[]string{"exec", "dev"}, "-- separator and command required"},
		{[]string{"exec", "dev", "--"}, "command required after --"},
		{[]string{"exec", "a", "b", "--", "true"}, "expected at most one argument before --, got 2"},
		{[]string{"exec", "Not_A_Name", "--", "true"}, `invalid cluster name "Not_A_Name"`},
		{[]string{"enter", "a", "b"}, "accepts at most 1 arg(s), received 2"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer

			code := run(noDocker(t.Context()), tc.args, &stderr)

			assert.Equal(t, exitUsage, code, stderr.String())
			assert.Contains(t, stderr.String(), tc.want)
		})
	}
}

// TestRun_InvalidSindRealmExits1 marks where usage errors end: SIND_REALM
// is not part of the command line, so an invalid one exits 1, like a
// config file that does not validate.
func TestRun_InvalidSindRealmExits1(t *testing.T) {
	t.Setenv("SIND_REALM", "Not_A_Realm")
	var stderr bytes.Buffer

	code := run(noDocker(t.Context()), []string{"get", "clusters"}, &stderr)

	assert.Equal(t, exitFailure, code)
	assert.Contains(t, stderr.String(), `SIND_REALM: invalid realm name "Not_A_Realm"`)
}

func TestUsage(t *testing.T) {
	assert.NoError(t, usage(nil))

	err := fmt.Errorf("parsing: %w", usage(assert.AnError))
	assert.True(t, isUsageError(err))
	require.ErrorIs(t, err, assert.AnError)
	assert.EqualError(t, err, "parsing: "+assert.AnError.Error())

	assert.True(t, isUsageError(usagef("bad %s", "arg")))
	assert.False(t, isUsageError(assert.AnError))
	assert.False(t, isUsageError(nil))
}
