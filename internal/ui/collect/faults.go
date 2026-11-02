// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// faultLines is how many lines of the journal of a unit that is not
// active a fault keeps: the ones that say why it stopped.
const faultLines = "5"

// readFaults reads why the nodes of the status st of a cluster need a look,
// by short name: how the container of a node that is stopped stopped
// (docker inspect), and for each service of a running node that is down
// its state, why it stopped and the end of its journal (systemctl show,
// journalctl). The status names few such nodes, so it costs a call or two
// for each. It is best effort: what cannot be read is left out.
func readFaults(ctx context.Context, c *docker.Client, realm, name string, st *cluster.Status) map[string]store.Fault {
	out := map[string]store.Fault{}
	for _, n := range st.Nodes {
		if n == nil || n.Health == nil {
			continue
		}
		short := strings.TrimSuffix(n.Name, "."+name)
		container := cluster.ContainerName(realm, name, short)
		if n.Health.State == cluster.StateStopped {
			if info, err := c.InspectContainer(ctx, container); err == nil {
				out[short] = store.Fault{Exit: &store.Exit{Code: info.ExitCode, OOMKilled: info.OOMKilled, Error: info.Error, At: info.FinishedAt}}
			}
			continue
		}
		if n.Health.State != cluster.StateRunning {
			continue
		}
		var units []store.Unit
		for _, svc := range slices.Sorted(maps.Keys(n.Health.Services)) {
			if !n.Health.Services[svc] {
				units = append(units, readUnit(ctx, c, container, string(svc)))
			}
		}
		if len(units) > 0 {
			out[short] = store.Fault{Units: units}
		}
	}
	return out
}

// readUnit reads the state of a systemd unit of a container and the end of
// its journal.
func readUnit(ctx context.Context, c *docker.Client, container docker.ContainerName, unit string) store.Unit {
	u := store.Unit{Name: unit}
	show, err := c.ExecAllowNonZero(ctx, container, "systemctl", "show", unit,
		"--property=ActiveState,Result,ExecMainStatus,StateChangeTimestamp", "--timestamp=unix")
	if err == nil {
		for line := range strings.Lines(show) {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			switch k {
			case "ActiveState":
				u.State = v
			case "Result":
				u.Result = v
			case "ExecMainStatus":
				u.Status, _ = strconv.Atoi(v)
			case "StateChangeTimestamp":
				u.Since = unixTime(strings.TrimPrefix(v, "@"))
			}
		}
	}
	journal, err := c.ExecAllowNonZero(ctx, container, "journalctl", "-u", unit, "-n", faultLines, "--no-pager", "-o", "short-unix")
	if err == nil {
		u.Journal = journalLines(journal)
	}
	return u
}

// journalLines reads the lines that journalctl -o short-unix writes, as
// "1761555737.123456 worker-4 slurmd[212]: error: ...": the time and the
// text after the host name. Other lines, such as "-- No entries --", are
// left out.
func journalLines(out string) []store.JournalLine {
	var lines []store.JournalLine
	for line := range strings.Lines(out) {
		f := strings.SplitN(strings.TrimRight(line, "\r\n"), " ", 3)
		if len(f) < 3 {
			continue
		}
		at := unixTime(f[0])
		if at.IsZero() {
			continue
		}
		lines = append(lines, store.JournalLine{At: at, Text: f[2]})
	}
	return lines
}

// unixTime reads seconds since 1970, with a fraction, as journalctl and
// systemctl --timestamp=unix write them; the zero time for anything else,
// such as the empty time of a unit that never ran.
func unixTime(s string) time.Time {
	sec, frac, _ := strings.Cut(s, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	ns := 0
	if frac != "" {
		frac = (frac + "000000000")[:9]
		ns, _ = strconv.Atoi(frac)
	}
	return time.Unix(n, int64(ns))
}
