// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"fmt"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeOK is what the probe prints for a sind-node 26.05 image with a db
// node, through slurmctld and slurmd.
const probeOK = `daemon slurmctld slurm 26.05.5
daemon slurmd slurm 26.05.5
plugindir /usr/lib64/slurm
plugin http_parser_llhttp_parser.so
plugin url_parser_internal.so
plugin tls_none.so
plugin http_auth_local.so
plugin data_parser_v0_0_42.so
plugin data_parser_v0_0_44.so
plugin metrics_openmetrics.so
mariadb --skip-log-error
`

func TestProbeImage(t *testing.T) {
	var m mock.Executor
	m.AddResult(probeOK, "", nil)
	c := docker.NewClient(&m)
	req := ProbeRequest{Daemons: []string{Slurmctld, Slurmd}, PluginDir: "/opt/slurm", MariaDB: true}

	info, err := ProbeImage(t.Context(), c, "sind-node:26.05", req)
	require.NoError(t, err)
	assert.Equal(t, "26.05.5", info.Version)
	assert.Equal(t, []string{"/usr/lib64/slurm"}, info.PluginDirs)
	assert.Len(t, info.Plugins, 7)
	assert.Equal(t, "--skip-log-error", info.MariaDBLogError)
	assert.Empty(t, info.MonitoringProblems(req))
	assert.Empty(t, info.MariaDBWarning())

	require.Len(t, m.Calls, 1)
	assert.Equal(t, []string{
		"run", "--rm", "--network", "none", "--entrypoint", "/bin/sh", "sind-node:26.05",
		"-c", probeScript, "sh", "/opt/slurm", "1", "slurmctld", "slurmd",
	}, m.Calls[0].Args)
}

func TestProbeImage_WithoutMariaDB(t *testing.T) {
	var m mock.Executor
	m.AddResult("daemon slurmd slurm 26.05.5\n", "", nil)
	c := docker.NewClient(&m)

	info, err := ProbeImage(t.Context(), c, "img", ProbeRequest{Daemons: []string{Slurmd}})
	require.NoError(t, err)
	assert.Equal(t, "26.05.5", info.Version)
	assert.Equal(t, []string{"-c", probeScript, "sh", "", "0", "slurmd"}, m.Calls[0].Args[7:])
}

func TestProbeImage_RunFails(t *testing.T) {
	var m mock.Executor
	m.AddResult("", "no such image", fmt.Errorf("exit status 125"))
	c := docker.NewClient(&m)

	_, err := ProbeImage(t.Context(), c, "img", ProbeRequest{Daemons: []string{Slurmctld}})
	require.ErrorContains(t, err, "probing image img: ")
}

func TestProbeImage_BadOutput(t *testing.T) {
	var m mock.Executor
	m.AddResult("hello\n", "", nil)
	c := docker.NewClient(&m)

	_, err := ProbeImage(t.Context(), c, "img", ProbeRequest{Daemons: []string{Slurmctld}})
	require.EqualError(t, err, `probing image img: unexpected probe output: "hello"`)
}

func TestParseProbe_Rejects(t *testing.T) {
	ctld := ProbeRequest{Daemons: []string{Slurmctld}}
	tests := []struct {
		name    string
		out     string
		req     ProbeRequest
		wantErr string
	}{
		{"unknown daemon", "daemon slurmdbd slurm 26.05.5\n", ctld, `unexpected probe output: "daemon slurmdbd slurm 26.05.5"`},
		{"daemon twice", "daemon slurmctld slurm 26.05.5\ndaemon slurmctld slurm 26.05.5\n", ctld, `unexpected probe output: "daemon slurmctld slurm 26.05.5"`},
		{"bad version", "daemon slurmctld slurm 26.05\n", ctld, `slurmctld -V: unexpected slurmctld -V output: "slurm 26.05"`},
		{
			"control sequence in version",
			"daemon slurmctld slurm 26.05.5\x1b]52;c;eA==\x07\n", ctld,
			"slurmctld -V: unexpected slurmctld -V output: \"slurm 26.05.5\\x1b]52;c;eA==\\a\"",
		},
		{"unknown missing", "missing sackd\n", ctld, `unexpected probe output: "missing sackd"`},
		{"missing after daemon", "daemon slurmctld slurm 26.05.5\nmissing slurmctld\n", ctld, `unexpected probe output: "missing slurmctld"`},
		{"relative plugin dir", "daemon slurmctld slurm 26.05.5\nplugindir lib\n", ctld, `unexpected probe output: "plugindir lib"`},
		{"plugin dir with a blank", "daemon slurmctld slurm 26.05.5\nplugindir /a b\n", ctld, `unexpected probe output: "plugindir /a b"`},
		{"plugin before a dir", "daemon slurmctld slurm 26.05.5\nplugin tls_none.so\n", ctld, `unexpected probe output: "plugin tls_none.so"`},
		{"plugin with a slash", "daemon slurmctld slurm 26.05.5\nplugindir /p\nplugin ../x.so\n", ctld, `unexpected probe output: "plugin ../x.so"`},
		{"mariadb not asked for", "daemon slurmctld slurm 26.05.5\nmariadb \n", ctld, `unexpected probe output: "mariadb "`},
		{"extra line", "daemon slurmctld slurm 26.05.5\n\n", ctld, `unexpected probe output: ""`},
		{"daemon without a result", "", ctld, "no result for slurmctld"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseProbe(tt.out, tt.req)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestParseProbe_Unloadable(t *testing.T) {
	info, err := parseProbe("daemon slurmd slurm 26.05.5\nmissing slurmctld\nplugindir /p\nunloadable http_parser_llhttp_parser.so\n",
		ProbeRequest{Daemons: []string{Slurmd, Slurmctld}})
	require.NoError(t, err)
	assert.Equal(t, []string{"slurmctld"}, info.Missing)
	assert.Equal(t, []string{"http_parser_llhttp_parser.so"}, info.Unloadable)
}

func TestMonitoringProblems(t *testing.T) {
	all := []string{"http_parser_llhttp_parser.so", "url_parser_internal.so", "tls_none.so", "http_auth_local.so", "data_parser_v0_0_44.so", "metrics_openmetrics.so"}
	ctld := ProbeRequest{Daemons: []string{Slurmctld}}
	slurmd := ProbeRequest{Daemons: []string{Slurmd}}
	tests := []struct {
		name string
		info ImageInfo
		req  ProbeRequest
		want []string
	}{
		{"complete", ImageInfo{Version: "26.05.5", PluginDirs: []string{"/p"}, Plugins: all}, ctld, nil},
		{"pre-release", ImageInfo{Version: "26.05.0-0rc1", PluginDirs: []string{"/p"}, Plugins: all}, ctld, nil},
		{"later line", ImageInfo{Version: "27.11.0", PluginDirs: []string{"/p"}, Plugins: all}, ctld, nil},
		{"later major", ImageInfo{Version: "27.02.0", PluginDirs: []string{"/p"}, Plugins: all}, ctld, nil},
		{"25.11", ImageInfo{Version: "25.11.8"}, ctld, []string{"Slurm 25.11.8 is older than 26.05, which monitoring needs"}},
		{"unparsable version", ImageInfo{Version: "x"}, ctld, []string{"Slurm x is older than 26.05, which monitoring needs"}},
		{"one number", ImageInfo{Version: "26"}, ctld, []string{"Slurm 26 is older than 26.05, which monitoring needs"}},
		{"minor not a number", ImageInfo{Version: "26.x.1"}, ctld, []string{"Slurm 26.x.1 is older than 26.05, which monitoring needs"}},
		{"missing daemon", ImageInfo{Missing: []string{"slurmd"}}, slurmd, []string{"has no slurmd"}},
		{"no plugin dir", ImageInfo{Version: "26.05.5"}, ctld, []string{"cannot find Slurm's plugin directory; set PluginDir in slurm.main"}},
		{
			"no HTTP parser, no metrics",
			ImageInfo{Version: "26.05.5", PluginDirs: []string{"/a", "/b"}, Plugins: all[1:5]},
			ctld,
			[]string{
				"Slurm has no HTTP parser plugin in /a:/b; build it with --with-llhttp-parser",
				"Slurm has no OpenMetrics plugin (metrics/openmetrics) in /a:/b",
			},
		},
		{"slurmd needs no metrics plugin", ImageInfo{Version: "26.05.5", PluginDirs: []string{"/p"}, Plugins: all[:5]}, slurmd, nil},
		{
			"unloadable",
			ImageInfo{Version: "26.05.5", PluginDirs: []string{"/p"}, Plugins: all[1:5], Unloadable: []string{"http_parser_llhttp_parser.so"}},
			slurmd,
			[]string{"Slurm's http_parser_llhttp_parser.so in /p cannot load: a library it needs is missing"},
		},
		{"MariaDB only", ImageInfo{PluginDirs: []string{"/p"}, Plugins: all}, ProbeRequest{MariaDB: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.info.MonitoringProblems(tt.req))
		})
	}
}

func TestMariaDBWarning(t *testing.T) {
	for opt, want := range map[string]string{
		"":                 "",
		"--skip-log-error": "",
		"--log-error=/var/log/mariadb/mariadb.log": "MariaDB writes its error log to a file, which monitoring does not collect; add skip-log-error to a my.cnf.d file",
		"--log_error": "MariaDB writes its error log to a file, which monitoring does not collect; add skip-log-error to a my.cnf.d file",
		"\x1b[31m":    "cannot read MariaDB's error log setting, so monitoring may not collect MariaDB's log",
	} {
		assert.Equal(t, want, (&ImageInfo{MariaDBLogError: opt}).MariaDBWarning(), opt)
	}
}

func TestVersionAtLeast(t *testing.T) {
	assert.True(t, VersionAtLeast("26.05.4", "26.05"))
	assert.True(t, VersionAtLeast("26.05.0-0rc1", "26.05"), "a pre-release counts as its line")
	assert.True(t, VersionAtLeast("27.11.0", "26.05"))
	assert.False(t, VersionAtLeast("25.11.8", "26.05"))
	assert.False(t, VersionAtLeast("", "26.05"))
}
