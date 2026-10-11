// SPDX-License-Identifier: LGPL-3.0-or-later

package probe

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/GSI-HPC/sind/pkg/docker"
)

// SlurmctldPort is slurmctld's RPC port, on which Slurm 26.05 and later
// also serve HTTP: the daemon tells the two apart by a connection's first
// bytes.
const SlurmctldPort = "6817"

// httpStatusScript sends a GET request for the path $2 to port $1 of the
// node and prints the status line of the answer, waiting at most two
// seconds for it. It uses bash's /dev/tcp, as SSHDReady does, so that the
// image needs no HTTP client.
const httpStatusScript = `exec 3<>/dev/tcp/localhost/$1 || exit 1
printf 'GET %s HTTP/1.0\r\n\r\n' "$2" >&3
read -t 2 status <&3 || exit 1
printf '%s\n' "$status"`

// statusLinePattern matches an HTTP status line and captures its code.
var statusLinePattern = regexp.MustCompile(`^HTTP/1\.[01] ([0-9]{3})\b`)

// HTTPStatus returns the status line a daemon on the node answers a GET
// request for path on port with, and whether its code is 2xx: /livez and
// /readyz answer 204, the metrics endpoints 200.
func HTTPStatus(ctx context.Context, client *docker.Client, name docker.ContainerName, port, path string) (line string, ok bool, err error) {
	stdout, err := client.Exec(ctx, name, "bash", "-c", httpStatusScript, "bash", port, path)
	if err != nil {
		return "", false, err
	}
	line = strings.TrimRight(strings.TrimSpace(stdout), "\r")
	m := statusLinePattern.FindStringSubmatch(line)
	return line, m != nil && m[1][0] == '2', nil
}

// SlurmctldHTTP returns a check that slurmctld on the node answers path on
// SlurmctldPort with a 2xx status: the monitoring stack scrapes it there.
// An image whose Slurm has no HTTP parser, or a CommunicationParameters
// with disable_http, keep slurmctld from serving HTTP, which it then only
// logs at debug level.
func SlurmctldHTTP(path string) Func {
	return func(ctx context.Context, client *docker.Client, name docker.ContainerName) error {
		line, ok, err := HTTPStatus(ctx, client, name, SlurmctldPort, path)
		if ok {
			return nil
		}
		got := fmt.Sprintf("got %q", line)
		if err != nil {
			got = err.Error()
		}
		return fmt.Errorf("slurmctld serves no HTTP on port %s (%s for GET %s); check CommunicationParameters, or raise SlurmctldDebug to see why the http_parser plugin did not load",
			SlurmctldPort, got, path)
	}
}
