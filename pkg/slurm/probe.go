// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// Daemons ProbeImage checks an image through.
const (
	Slurmctld = "slurmctld"
	Slurmd    = "slurmd"
)

// MonitoringMinVersion is the oldest Slurm release line whose slurmctld and
// slurmd serve the HTTP endpoints the monitoring stack uses
// (/metrics/*, /livez, /readyz), in the form major.minor.
const MonitoringMinVersion = "26.05"

// probeScript lists, for each daemon given after the plugin directory and
// the MariaDB switch, the version it reports, then the Slurm plugin
// directories, the plugins in them that monitoring needs and whether their
// libraries resolve, and with the switch set MariaDB's last error log
// option. The plugin directory is PluginDir when slurm.main sets it, else
// the directory of the libslurmfull the first daemon links, which is
// Slurm's $(pkglibdir) whatever --libdir an image used, else the usual
// plugin directories that exist.
const probeScript = `pd=$1; db=$2; shift 2
for d in "$@"; do
  if command -v "$d" >/dev/null 2>&1; then echo "daemon $d $("$d" -V 2>/dev/null | head -n 1)"
  else echo "missing $d"; fi
done
lib=$(ldd "$(command -v "$1")" 2>/dev/null | awk '$1 ~ /^libslurmfull/ { print $3 }')
dirs=$pd
[ -n "$dirs" ] || dirs=${lib%/*}
if [ -z "$dirs" ]; then
  for c in /usr/lib64/slurm /usr/lib/slurm /usr/local/lib64/slurm /usr/local/lib/slurm /usr/lib/*-linux-gnu/slurm; do
    [ -d "$c" ] && dirs=${dirs:+$dirs:}$c
  done
fi
IFS=:
for dir in $dirs; do
  [ -n "$dir" ] || continue
  echo "plugindir $dir"
  for p in "$dir"/http_parser_*.so "$dir"/url_parser_*.so "$dir"/tls_*.so \
           "$dir"/http_auth_*.so "$dir"/data_parser_*.so "$dir"/metrics_openmetrics.so; do
    [ -e "$p" ] || continue
    if ldd "$p" 2>/dev/null | grep -q 'not found'; then echo "unloadable ${p##*/}"
    else echo "plugin ${p##*/}"; fi
  done
done
unset IFS
if [ "$db" = 1 ]; then
  echo "mariadb $(my_print_defaults --mysqld 2>/dev/null | grep -E -- '^--(skip-)?log[-_]error' | tail -n 1)"
fi`

// ProbeRequest says what ProbeImage checks an image for.
type ProbeRequest struct {
	// Daemons are the Slurm daemons the image runs that the monitoring
	// stack talks to over HTTP: Slurmctld for the controllers' image,
	// Slurmd for a managed worker group's. The first one's version is the
	// image's.
	Daemons []string
	// PluginDir is the PluginDir slurm.main sets, a colon-separated list,
	// empty when it sets none.
	PluginDir string
	// MariaDB also reads MariaDB's error log setting, for a managed db
	// node's image.
	MariaDB bool
}

// ImageInfo is what ProbeImage learned about an image.
type ImageInfo struct {
	// Version is the Slurm version the first daemon reports, empty when the
	// request named none.
	Version string
	// Missing are the requested daemons the image lacks.
	Missing []string
	// PluginDirs are the Slurm plugin directories the probe looked in.
	PluginDirs []string
	// Plugins are the plugin files found there whose libraries resolve,
	// Unloadable those whose libraries do not.
	Plugins, Unloadable []string
	// MariaDBLogError is MariaDB's last log-error option, such as
	// "--skip-log-error", empty when it has none; set only when the request
	// asked for MariaDB.
	MariaDBLogError string
}

// ProbeImage runs one throwaway container of image, without a network and
// with /bin/sh as its entrypoint, and reports the Slurm version, the
// plugins monitoring needs and, with req.MariaDB, MariaDB's error log
// setting. The image is untrusted: every line it prints must have one of the
// expected forms, or ProbeImage fails.
func ProbeImage(ctx context.Context, dc *docker.Client, image string, req ProbeRequest) (*ImageInfo, error) {
	db := "0"
	if req.MariaDB {
		db = "1"
	}
	args := append([]string{"-c", probeScript, "sh", req.PluginDir, db}, req.Daemons...)
	stdout, err := dc.RunEphemeralWith(ctx, []string{"--network", "none", "--entrypoint", "/bin/sh"}, image, args...)
	if err != nil {
		return nil, fmt.Errorf("probing image %s: %w", image, err)
	}
	info, err := parseProbe(stdout, req)
	if err != nil {
		return nil, fmt.Errorf("probing image %s: %w", image, err)
	}
	return info, nil
}

// pluginFilePattern is the form of a plugin file name the probe reports.
var pluginFilePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.so$`)

// probePathPattern is the form of a plugin directory: an absolute path of
// printable characters without blanks or colons.
var probePathPattern = regexp.MustCompile(`^/[A-Za-z0-9_./+@-]*$`)

// mariadbOptionPattern is the form of a MariaDB log-error option.
var mariadbOptionPattern = regexp.MustCompile(`^--(skip-)?log[-_]error(=\S+)?$`)

// parseProbe reads the output of probeScript. Every line must have one of
// the forms the script prints, with words and paths of the expected shapes,
// so that nothing an image prints can reach sind's output unchecked.
func parseProbe(stdout string, req ProbeRequest) (*ImageInfo, error) {
	info := &ImageInfo{}
	daemons := map[string]bool{}
	for line := range strings.Lines(stdout) {
		line = strings.TrimSuffix(line, "\n")
		kind, rest, _ := strings.Cut(line, " ")
		switch kind {
		case "daemon":
			name, version, _ := strings.Cut(rest, " ")
			if !slices.Contains(req.Daemons, name) || daemons[name] {
				return nil, unexpectedProbeLine(line)
			}
			v, err := parseVersion(version)
			if err != nil {
				return nil, fmt.Errorf("%s -V: %w", name, err)
			}
			daemons[name] = true
			if name == req.Daemons[0] {
				info.Version = v
			}
		case "missing":
			if !slices.Contains(req.Daemons, rest) || daemons[rest] {
				return nil, unexpectedProbeLine(line)
			}
			daemons[rest] = true
			info.Missing = append(info.Missing, rest)
		case "plugindir":
			if !probePathPattern.MatchString(rest) {
				return nil, unexpectedProbeLine(line)
			}
			info.PluginDirs = append(info.PluginDirs, rest)
		case "plugin", "unloadable":
			if !pluginFilePattern.MatchString(rest) || len(info.PluginDirs) == 0 {
				return nil, unexpectedProbeLine(line)
			}
			if kind == "plugin" {
				info.Plugins = append(info.Plugins, rest)
			} else {
				info.Unloadable = append(info.Unloadable, rest)
			}
		case "mariadb":
			if !req.MariaDB {
				return nil, unexpectedProbeLine(line)
			}
			info.MariaDBLogError = rest
		default:
			return nil, unexpectedProbeLine(line)
		}
	}
	for _, d := range req.Daemons {
		if !daemons[d] {
			return nil, fmt.Errorf("no result for %s", d)
		}
	}
	return info, nil
}

// unexpectedProbeLine is the error for a line the probe script does not
// print.
func unexpectedProbeLine(line string) error {
	return fmt.Errorf("unexpected probe output: %q", line)
}

// monitoringPlugin is a plugin type slurmctld or slurmd needs to serve
// HTTP: the prefix of its plugin files, its name in problems, and a hint
// on how to build it.
type monitoringPlugin struct{ prefix, what, hint string }

// monitoringPlugins are the plugin types slurmctld and slurmd need to
// serve HTTP: http_switch_init loads a parser of each kind, and http_init
// the HTTP authentication and data parser plugins, without which the
// daemons abort once HTTP is on.
var monitoringPlugins = []monitoringPlugin{
	{"http_parser_", "HTTP parser plugin", "; build it with --with-llhttp-parser"},
	{"url_parser_", "URL parser plugin", ""},
	{"tls_", "TLS plugin", ""},
	{"http_auth_", "HTTP authentication plugin", ""},
	{"data_parser_", "data parser plugin", ""},
}

// openMetricsPlugin is the plugin MetricsType=metrics/openmetrics loads,
// which only slurmctld needs.
var openMetricsPlugin = monitoringPlugin{"metrics_openmetrics.so", "OpenMetrics plugin (metrics/openmetrics)", ""}

// MonitoringProblems returns what keeps an image from serving the HTTP
// endpoints the monitoring stack uses, one problem per entry, empty when
// nothing does: a missing daemon, a Slurm older than MonitoringMinVersion,
// no plugin directory, and each plugin type of which no loadable plugin
// was found; slurmctld also needs the OpenMetrics plugin.
func (i *ImageInfo) MonitoringProblems(req ProbeRequest) []string {
	var problems []string
	for _, d := range i.Missing {
		problems = append(problems, "has no "+d)
	}
	if len(problems) > 0 {
		return problems
	}
	if len(req.Daemons) > 0 && !VersionAtLeast(i.Version, MonitoringMinVersion) {
		problems = append(problems, fmt.Sprintf("Slurm %s is older than %s, which monitoring needs", i.Version, MonitoringMinVersion))
		return problems
	}
	if len(i.PluginDirs) == 0 {
		return append(problems, "cannot find Slurm's plugin directory; set PluginDir in slurm.main")
	}
	dirs := strings.Join(i.PluginDirs, ":")
	need := monitoringPlugins
	if slices.Contains(req.Daemons, Slurmctld) {
		need = append(slices.Clip(need), openMetricsPlugin)
	}
	for _, p := range need {
		match := func(name string) bool { return strings.HasPrefix(name, p.prefix) }
		if slices.ContainsFunc(i.Plugins, match) {
			continue
		}
		problem := fmt.Sprintf("Slurm has no %s in %s%s", p.what, dirs, p.hint)
		if bad := slices.IndexFunc(i.Unloadable, match); bad >= 0 {
			problem = fmt.Sprintf("Slurm's %s in %s cannot load: a library it needs is missing", i.Unloadable[bad], dirs)
		}
		problems = append(problems, problem)
	}
	return problems
}

// MariaDBWarning returns why MariaDB's messages do not reach the journal,
// where the monitoring stack collects them, empty when they do: its last
// log-error option, read with req.MariaDB, must be --skip-log-error, or
// there must be none.
func (i *ImageInfo) MariaDBWarning() string {
	opt := i.MariaDBLogError
	if opt == "" || opt == "--skip-log-error" {
		return ""
	}
	if !mariadbOptionPattern.MatchString(opt) {
		return "cannot read MariaDB's error log setting, so monitoring may not collect MariaDB's log"
	}
	return "MariaDB writes its error log to a file, which monitoring does not collect; add skip-log-error to a my.cnf.d file"
}

// VersionAtLeast reports whether a Slurm version (25.11.8, 26.05.0-0rc1) is
// of the release line oldest (major.minor) or a later one. Pre-releases
// of a line count as the line.
func VersionAtLeast(version, oldest string) bool {
	var v, o [2]int
	if _, err := fmt.Sscanf(version, "%d.%d", &v[0], &v[1]); err != nil {
		return false
	}
	_, _ = fmt.Sscanf(oldest, "%d.%d", &o[0], &o[1])
	return slices.Compare(v[:], o[:]) >= 0
}
