// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// errNoCommands is what every command but a log stream gets.
var errNoCommands = errors.New("the demo runs no commands")

// Exec is the executor of the demo. It runs nothing: the only commands the
// pages start are the log streams, `docker logs --follow` and `docker exec
// NODE journalctl --follow`, and Exec answers those with made-up lines.
type Exec struct {
	// Now is the clock of the lines' times.
	Now func() time.Time
	// Every is the time between two new lines; 0 sends none after the
	// first lines, as a still demo does.
	Every time.Duration
	// Failed tells when a unit of a container failed, and whether it is
	// down: its journal then ends with the lines of its failure. nil for
	// none.
	Failed func(container, unit string) (time.Time, bool)
}

// Run implements cmdexec.Executor.
func (*Exec) Run(context.Context, string, ...string) (string, string, error) {
	return "", "", errNoCommands
}

// RunWithStdin implements cmdexec.Executor.
func (*Exec) RunWithStdin(context.Context, io.Reader, string, ...string) (string, string, error) {
	return "", "", errNoCommands
}

// head is how many lines a stream starts with.
const head = 60

// Start implements cmdexec.Executor: it answers the commands of a log
// stream and no other.
func (e *Exec) Start(ctx context.Context, name string, args ...string) (*cmdexec.Process, error) {
	if name != "docker" || len(args) < 2 || (args[0] != "logs" && args[0] != "exec") {
		return nil, errNoCommands
	}
	journal := args[0] == "exec"
	container, unit := args[len(args)-1], ""
	if journal {
		container = args[1]
		if i := slices.Index(args, "-u"); i >= 0 && i+1 < len(args) {
			unit = args[i+1]
		}
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(e.stream(ctx, pw, journal, container, unit))
	}()
	return &cmdexec.Process{Stdout: pr}, nil
}

// stream writes the lines of the stream until ctx ends or the reader goes.
// The journal of a unit that failed ends with its failure, and gets no new
// lines.
func (e *Exec) stream(ctx context.Context, w io.Writer, journal bool, container, unit string) error {
	short := shortName(container)
	daemon := unit
	if daemon == "" {
		daemon = daemonOf(short)
	}
	now := e.Now()
	failed, down := time.Time{}, false
	if journal && e.Failed != nil {
		failed, down = e.Failed(container, daemon)
	}
	first := head
	if down {
		now, first = failed.Add(-failureSpan), head-len(failureLines(daemon))
	}
	for i := range first {
		at := now.Add(-time.Duration(first-i) * 3 * time.Second)
		if _, err := io.WriteString(w, logLine(journal, short, daemon, i, at)+"\n"); err != nil {
			return err
		}
	}
	if down {
		for _, l := range failureJournal(daemon, failed) {
			if _, err := io.WriteString(w, l.At.UTC().Format("Jan _2 15:04:05")+" "+short+" "+l.Text+"\n"); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return nil
	}
	if e.Every <= 0 {
		<-ctx.Done()
		return nil
	}
	tick := time.NewTicker(e.Every)
	defer tick.Stop()
	for i := head; ; i++ {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if _, err := io.WriteString(w, logLine(journal, short, daemon, i, e.Now())+"\n"); err != nil {
			return err
		}
	}
}

// shortName returns the node of a container name such as sind-dev-worker-1.
func shortName(container string) string {
	if i := strings.Index(container, "-worker-"); i >= 0 {
		return container[i+1:]
	}
	if strings.HasSuffix(container, "-controller-backup") {
		return "controller-backup"
	}
	if i := strings.LastIndex(container, "-"); i >= 0 {
		return container[i+1:]
	}
	return container
}

// daemonOf returns the Slurm daemon of a node.
func daemonOf(short string) string {
	switch {
	case strings.HasPrefix(short, "controller"):
		return "slurmctld"
	case short == "db":
		return "slurmdbd"
	case short == "api":
		return "slurmrestd"
	case short == "submitter":
		return "sackd"
	}
	return "slurmd"
}

// messages are what each daemon says, in turn.
var messages = map[string][]string{
	"slurmctld": {
		"slurmctld: sched/backfill: _start_job: Started JobId=%d in debug on worker-1",
		"slurmctld: _job_complete: JobId=%d WEXITSTATUS 0",
		"slurmctld: _slurm_rpc_node_registration complete for worker-2",
		"slurmctld: Running as primary controller",
		"slurmctld: error: Nodes worker-3 not responding",
	},
	"slurmd": {
		"slurmd: launch task StepId=%d.0 request from UID:1000 GID:1000 HOST:172.20.0.2 PORT:6818",
		"slurmd: Message aggregation disabled",
		"slurmd: CPUs=2 Boards=1 Sockets=1 CoresPerSocket=2 ThreadsPerCore=1 Memory=1024",
		"slurmd: done with job %d",
		"slurmd: warning: Note very large processing time from _rpc_launch_tasks: usec=1204871",
	},
	"slurmdbd": {
		"slurmdbd: slurmdbd version 26.05.4 started",
		"slurmdbd: Accounting storage MYSQL plugin loaded",
		"slurmdbd: DBD_STEP_COMPLETE: JobId=%d",
	},
	"slurmrestd": {
		"slurmrestd: operations: GET /slurm/v0.0.44/ping",
		"slurmrestd: operations: GET /slurm/v0.0.44/jobs",
	},
	"sackd": {
		"sackd: Fetching configuration from slurmctld",
		"sackd: Configuration file written",
	},
}

// logLine returns the i-th line of a stream: a journal line with its time
// and the unit, or the line of the console of the container.
func logLine(journal bool, short, daemon string, i int, at time.Time) string {
	msgs, ok := messages[daemon]
	if !ok {
		msgs = []string{daemon + ": started", daemon + ": reloading configuration"}
	}
	msg := msgs[i%len(msgs)]
	if strings.Contains(msg, "%d") {
		msg = fmt.Sprintf(msg, 4000+i/4)
	}
	if !journal {
		return fmt.Sprintf("[%s] %s", at.UTC().Format("15:04:05"), msg)
	}
	return fmt.Sprintf("%s %s %s[%d]: %s", at.UTC().Format("Jan _2 15:04:05"), short, daemon, 100+i%300, strings.TrimPrefix(msg, daemon+": "))
}

// failureSpan is how long before its failure a unit of the demo started.
const failureSpan = 6 * time.Second

// failures are what each daemon of the demo writes as it fails, before
// systemd says that it did: slurmd cannot reach slurmctld.
var failures = map[string][]string{
	"slurmd": {
		"slurmd version 26.05.4 started",
		"error: Unable to register: Unable to contact slurm controller (connect failure)",
		"fatal: Unable to register with slurm controller, retry limit reached",
	},
}

// failureLines returns what a unit writes as it fails, before systemd's
// lines.
func failureLines(unit string) []string {
	if l, ok := failures[unit]; ok {
		return l
	}
	return []string{unit + " started", "error: " + unit + ": Address already in use", "fatal: " + unit + " cannot listen"}
}

// failureJournal returns the end of the journal of a unit that failed at
// at: its last lines, then systemd's.
func failureJournal(unit string, at time.Time) []store.JournalLine {
	var out []store.JournalLine
	lines := failureLines(unit)
	for i, l := range lines {
		out = append(out, store.JournalLine{At: at.Add(-failureSpan + time.Duration(i+1)*failureSpan/time.Duration(len(lines)+1)), Text: unit + "[212]: " + l})
	}
	return append(out,
		store.JournalLine{At: at, Text: "systemd[1]: " + unit + ".service: Main process exited, code=exited, status=1/FAILURE"},
		store.JournalLine{At: at, Text: "systemd[1]: " + unit + ".service: Failed with result 'exit-code'."},
	)
}
