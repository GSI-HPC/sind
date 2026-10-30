// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/GSI-HPC/go-clikit/cobratree"
	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/cliprogress"
	"github.com/GSI-HPC/go-clikit/progress/display"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/pkg/cluster"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/state"
)

// progressModes returns the words --progress takes, for its help.
func progressModes() string {
	return strings.Join(progressModeNames(), ", ")
}

// progressModeNames returns the values --progress takes.
func progressModeNames() []string {
	words := make([]string, 0, len(cliprogress.Modes()))
	for _, m := range cliprogress.Modes() {
		words = append(words, m.String())
	}
	return words
}

// logAdvanceEvery is the least time between two advances of a step that
// the event log writes, so that sind ui moves a bar four times a second;
// go-clikit writes one a second by default. logFlushEvery is how long a
// line of the event log waits at most before it is written, so that sind
// ui, which follows the log, shows an event within a few frames; go-clikit
// waits a second by default.
const (
	logAdvanceEvery = 250 * time.Millisecond
	logFlushEvery   = 25 * time.Millisecond
)

// The seams of the progress display. Tests replace them to draw on a
// terminal of their own, such as a progresstest.Screen, frame by frame;
// each is read when a command starts, never while it runs, except
// terminalSize and inForeground, which every frame asks.
//
//   - onTerminal reports whether a stream, standard error or standard
//     output, is a terminal. A tree or a counter is drawn only on a
//     standard error that is one, and standard output goes through the
//     display's writer when it is one too.
//   - intoPipe reports whether standard output goes into a pipe or a
//     socket, whose reader writes to the terminal the tree would be drawn
//     on: auto draws nothing then.
//   - terminalSize tells how many columns and rows the terminal of
//     standard error has. The tree needs 40 columns and 8 rows, and draws
//     the counter's line on a smaller terminal, or one that cannot say.
//   - inForeground reports whether sind is the job in the foreground of
//     that terminal; a job in the background draws nothing.
//   - colourProfile tells how many colours the terminal of standard
//     error shows, as the logger's colours take it: none under NO_COLOR.
//   - displayClock is the clock of the Bus and of the display.
//   - startRun makes the Bus of a command and what shows it. A test has
//     the display made without being started, keeps the Run, and draws
//     each frame with Run.Draw, once it has moved displayClock on.
var (
	onTerminal    = func(w io.Writer) bool { return isTerminal(w) }
	intoPipe      = func(w io.Writer) bool { return cliprogress.IsPipe(w) }
	terminalSize  = func(w io.Writer) (int, int, error) { return cliprogress.TerminalSize(w) }
	inForeground  = func(w io.Writer) bool { return cliprogress.InForeground(w) }
	colourProfile = func(w io.Writer) termenv.Profile { return lipgloss.NewRenderer(w).ColorProfile() }
	displayClock  = time.Now
	startRun      = cliprogress.Start
)

// withProgress returns run, the function of a command that changes
// clusters and asks nothing, decorated to report the command's progress:
// create cluster, create worker, delete cluster, delete worker, connect
// container, disconnect container and power.
// The command runs in a progress span of its own, the root of what it
// reports, named by its path below the root ("create cluster").
//
// The span is started under the Bus the command's context brought, as a
// test's; that Bus is the test's to close. Without one, cliprogress gives
// the command a Bus of its own when --progress has a display drawn or
// --progress-log an event log written, and none otherwise, so that
// standard error is then what it would be without progress, byte for
// byte. The Bus is closed, the display taken off the terminal, the streams put back, the
// event log written out and, for a command that failed or was
// interrupted, the summary written, before the decorated function
// returns, and so before run prints the command's error: cobra does not
// run PersistentPostRunE for a command that failed.
//
// A command that failed once it had been interrupted ends canceled,
// whatever its error says, as run tells it.
func withProgress(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return withProgressOn(nil, run)
}

// commandTarget returns what a command works on, as its arguments name it:
// a cluster, the clusters of its nodes or its containers; "" for what they
// do not name.
type commandTarget func(cmd *cobra.Command, args []string) string

// withProgressOn is withProgress for a command that works on what target
// names: the command span's Node, which the event log carries, so that a
// reader such as sind ui can tell "create cluster dev" from another create
// cluster. A command that learns its target only as it runs names it with
// nameTarget, and the span ends with that Node. The display draws no Node
// of a command span.
func withProgressOn(target commandTarget, run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) (err error) {
		interrupt := cmd.Context()
		returned := false
		p, startErr := startRun(interrupt, withActivityLog(interrupt, progressOptions(interrupt, cmd)))
		if startErr != nil {
			return cobratree.Usage(startErr)
		}
		ctx, restore := throughDisplay(cmd, p)
		defer func() {
			p.Finish(!returned || err != nil || interrupt.Err() != nil)
			restore()
		}()
		named := &targetName{}
		if target != nil {
			named.name = target(cmd, args)
		}
		ctx, span := progress.Start(ctx, progress.KindCommand, commandPath(cmd), named.options()...)
		cmd.SetContext(context.WithValue(ctx, targetKey{}, named))
		defer func() {
			switch {
			case !returned:
				span.End(errPanicked, named.options()...)
			case err != nil && interrupt.Err() != nil:
				span.End(interrupted{err}, named.options()...)
			default:
				span.End(err, named.options()...)
			}
			cmd.SetContext(interrupt)
		}()
		err = run(cmd, args)
		returned = true
		return err
	}
}

// targetName is what the command span of a command works on, "" for
// nothing named.
type targetName struct{ name string }

// targetKey is the context key of a command's *targetName.
type targetKey struct{}

// options returns the options that give the command span its target.
func (n *targetName) options() []progress.Option {
	if n.name == "" {
		return nil
	}
	return []progress.Option{progress.Node(n.name)}
}

// nameTarget names what the command of ctx works on once it knows, such as
// the cluster of a configuration read from stdin: its span ends with it.
// Without a command span it does nothing.
func nameTarget(ctx context.Context, name string) {
	if n, ok := ctx.Value(targetKey{}).(*targetName); ok {
		n.name = name
	}
}

// clusterTarget is the target of a command whose argument is a cluster,
// the one SIND_CLUSTER names, else the default one, when there is none, and
// "" for a SIND_CLUSTER that does not name one, as the command fails on it.
func clusterTarget(_ *cobra.Command, args []string) string {
	name, _ := clusterArg(args)
	return name
}

// nodesTarget is the target of a command whose arguments are nodes: their
// clusters, each once, in the order the command works on them, "" for nodes
// that do not parse, as the command fails on them.
func nodesTarget(_ *cobra.Command, args []string) string {
	targets, err := parseNodeArgs(strings.Join(args, " "))
	if err != nil {
		return ""
	}
	var clusters []string
	for _, g := range groupByCluster(targets) {
		clusters = append(clusters, g.Cluster)
	}
	return strings.Join(clusters, ",")
}

// containersTarget is the target of a command whose arguments are
// containers.
func containersTarget(_ *cobra.Command, args []string) string { return strings.Join(args, ",") }

// errPanicked ends the span of a command whose function panicked.
var errPanicked = errors.New("the command panicked")

// interrupted is the error of a command that failed once it had been
// interrupted, which is what ended it, as run tells: its class is
// canceled, and its text the command's error.
type interrupted struct{ error }

func (e interrupted) Unwrap() error { return e.error }

// ProgressClass says that the command was interrupted.
func (interrupted) ProgressClass() progress.Class { return progress.ClassCanceled }

// rollbackFailed is the error of create cluster's step "rollback" when it
// could not remove the mesh it made. It is classed by its own error alone,
// never by progressClass, which calls every failure once the command was
// interrupted canceled: the rollback runs on after an interrupt, so what
// it left behind is a failure, not work the interrupt stopped. pkg/cluster
// ends its rollbacks alike.
type rollbackFailed struct{ error }

func (e rollbackFailed) Unwrap() error { return e.error }

// ProgressClass is the class of the rollback's error without a fallback.
func (e rollbackFailed) ProgressClass() progress.Class { return progress.Classify(e.error, nil) }

// progressClass returns the fallback rule of the classes the spans of a
// command interrupt is the context of end with: once interrupt has ended,
// every failure is canceled, since a docker call the interrupt killed
// fails with "signal: killed" alone; a usage error is the command line's;
// nodes or Slurm that did not get ready within --wait timed out; anything
// else is the target's.
func progressClass(interrupt context.Context) func(error) progress.Class {
	return func(err error) progress.Class {
		switch {
		case interrupt.Err() != nil:
			return progress.ClassCanceled
		case cobratree.IsUsage(err):
			return progress.ClassUsage
		case errors.Is(err, cluster.ErrNotReady):
			return progress.ClassTimeout
		}
		return progress.ClassNone
	}
}

// progressOptions returns how cmd, whose context is interrupt, shows its
// progress and writes its event log: as --progress and --progress-log say,
// else SIND_PROGRESS and SIND_PROGRESS_LOG, on cobra's streams, which the
// seams tell about. The kit decides from them (cliprogress.Choose): auto
// draws the live tree on a stderr that is a terminal and not a dumb one,
// while stdout goes into no pipe, and nothing elsewhere; what the flag
// asks for where it cannot be drawn is a usage error, and what the
// variable asks for fails no command.
func progressOptions(interrupt context.Context, cmd *cobra.Command) cliprogress.Options {
	top := cmd.Root()
	stdout, stderr := top.OutOrStdout(), top.ErrOrStderr()
	flags := top.Flags()
	mode, _ := flags.GetString("progress")
	logPath, _ := flags.GetString("progress-log")
	return cliprogress.Options{
		Program: "sind",
		Mode: cliprogress.Setting{Flag: "--progress", Given: flags.Changed("progress"), Value: mode,
			Variable: envvar.Progress, Env: os.Getenv(envvar.Progress)},
		Log: cliprogress.Setting{Flag: "--progress-log", Given: flags.Changed("progress-log"), Value: logPath,
			Variable: envvar.ProgressLog, Env: os.Getenv(envvar.ProgressLog)},
		LogOptions: progress.LogOptions{Version: strings.TrimPrefix(resolveVersion(), "v"), AdvanceEvery: logAdvanceEvery, FlushEvery: logFlushEvery},
		Stderr:     stderr,
		OnTerminal: onTerminal(stderr),
		Dumb:       os.Getenv("TERM") == "dumb",
		IntoPipe:   intoPipe(stdout),
		Size:       func() (int, int, error) { return terminalSize(stderr) },
		Foreground: func() bool { return inForeground(stderr) },
		ASCII:      !cliprogress.UTF8Locale(os.Getenv),
		Theme:      progressTheme(stderr),
		Now:        displayClock,
		Trace: func() progress.TraceContext {
			tc, _ := progress.ParseTraceContext(takeTraceContext())
			return tc
		},
		Classify: progressClass(interrupt),
	}
}

// progressTheme returns the Theme the displays and the summary draw on
// stderr: Classic in the colours the terminal shows, as cliprogress.ThemeFor
// reads them from NO_COLOR, TERM and COLORTERM, and the zero Theme, plain
// text, where stderr is not a terminal or TERM is dumb, as plain lines go
// to a log there.
func progressTheme(stderr io.Writer) display.Theme {
	return cliprogress.ThemeFor(display.Classic, onTerminal(stderr), os.Getenv)
}

// throughDisplay returns the context of cmd under the Run p, and what puts
// cobra's writers back. Under a display, everything the command writes to
// its streams goes through the writers of the display's terminal, which
// take the tree or the counter off first: cobra's stderr, which Warning
// lines and help are written to, and stdout when it is the terminal too;
// the stderr of the context (withStderr), which the warnings of pkg/ go
// to, the realm lock's and the mesh's among them; and the logger, rebuilt
// on the terminal's Lines, which writes its records above the display
// whatever goroutine logs them, in the colours the terminal takes.
func throughDisplay(cmd *cobra.Command, p *cliprogress.Session) (context.Context, func()) {
	ctx := p.Context()
	if p.Mode() == cliprogress.ModeNone {
		return ctx, func() {}
	}
	top := cmd.Root()
	stdout, stderr := top.OutOrStdout(), top.ErrOrStderr()
	wrapOut := onTerminal(stdout)
	if wrapOut {
		top.SetOut(p.Writer(stdout))
	}
	top.SetErr(p.Writer(stderr))
	ctx = withStderr(ctx, top.ErrOrStderr())
	v, _ := top.Flags().GetCount("verbose")
	logger := newLogHandler(p.Lines(stderr), v)
	logger.SetColorProfile(lipgloss.NewRenderer(stderr).ColorProfile())
	return sindlog.With(ctx, slog.New(logger)), func() {
		if wrapOut {
			top.SetOut(stdout)
		}
		top.SetErr(stderr)
	}
}

// activityLimit is the size above which the default activity log is moved
// aside to activity.jsonl.1, so that it stays under about twice this size.
const activityLimit = 8 << 20

// withActivityLog returns o with the default activity log as its default
// event log when neither --progress-log nor SIND_PROGRESS_LOG names one:
// the file $XDG_STATE_HOME/sind/activity.jsonl, which a display of what
// sind did, such as sind ui, reads. A flag or a variable that is given,
// even empty, wins, and an empty one writes no log.
//
// The log is a convenience and changes nothing a command writes: cliprogress
// leaves out a log that cannot be opened, as in a read-only home, and says
// nothing of one that cannot be written, where a log named by the flag or
// the variable would.
func withActivityLog(ctx context.Context, o cliprogress.Options) cliprogress.Options {
	if _, set := os.LookupEnv(envvar.ProgressLog); o.Log.Given || set {
		return o
	}
	path, err := state.PrepareActivityDir()
	if err != nil {
		sindlog.From(ctx).DebugContext(ctx, "not writing the activity log", "error", err)
		return o
	}
	o.DefaultLog, o.RotateAt = path, activityLimit
	return o
}

// checkProgressFlag rejects a --progress that cliprogress.Choose refuses
// before any command runs, as a usage error: a value that names no way to
// show progress, or a tree or a counter where neither can be drawn. A
// command that shows no progress (version, get, ssh, mcp) would otherwise
// accept a mistyped one silently, as checkRealmFlag says of --realm.
// SIND_PROGRESS fails no command, and only a command that shows progress
// says what is wrong with it; --progress-log is opened only by such a
// command (withProgress), since opening it creates it.
func checkProgressFlag(cmd *cobra.Command) error {
	flags := cmd.Root().Flags()
	if !flags.Changed("progress") {
		return nil
	}
	if mode, _ := flags.GetString("progress"); !slices.Contains(progressModeNames(), mode) {
		return invalidFlagValue("progress", mode, progressModeNames()...)
	}
	_, _, err := cliprogress.Choose(progressOptions(cmd.Context(), cmd))
	return cobratree.Usage(err)
}

// takeTraceContext returns the W3C trace context sind was started with,
// TRACEPARENT and TRACESTATE, and takes both out of its environment. The
// event log continues the trace; the programs sind runs, docker among
// them, are handed none: sind sends its spans nowhere, so a child told to
// run under the caller's span, or one of sind's, would hang what it traces
// under a parent no tracing system holds. Only a command that makes a Bus
// calls it (cliprogress.Start calls Options.Trace): without one sind has no spans, and a child's
// spans belong under the caller's.
func takeTraceContext() (traceparent, tracestate string) {
	traceparent, tracestate = os.Getenv("TRACEPARENT"), os.Getenv("TRACESTATE")
	// Unsetenv fails only for a name the system cannot hold.
	_ = os.Unsetenv("TRACEPARENT")
	_ = os.Unsetenv("TRACESTATE")
	return traceparent, tracestate
}
