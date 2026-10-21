// SPDX-License-Identifier: LGPL-3.0-or-later

// Command screenshots takes the screenshots of sind ui that the docs show.
//
// It starts sind ui --demo --still, whose pages are the same on every run,
// drives an installed Chromium through the shots of shots.yaml, and writes one
// image per shot, theme and size into the directory of -out. It is a module of
// its own, so that sind does not depend on a browser driver. Its images are
// not committed.
//
//	go build -o sind ../../cmd/sind && go run . -sind ./sind -out /tmp/shots
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "screenshots:", err)
		os.Exit(1)
	}
}

// epoch is the time a still demo stands at (demo.Epoch of internal/ui/demo,
// which this module cannot import): the page's clock is set to it.
const epoch = "2026-10-21T09:15:00Z"

func run(args []string) error {
	var o options
	var at string
	fs := flag.NewFlagSet("screenshots", flag.ContinueOnError)
	fs.StringVar(&o.Sind, "sind", "sind", "the sind executable to take the screenshots of")
	fs.StringVar(&o.Chromium, "chromium", "", "the Chromium or Chrome executable (default: $CHROMIUM, else the first of chromium, chromium-browser, google-chrome on PATH)")
	fs.StringVar(&o.Shots, "shots", "shots.yaml", "the shot list")
	fs.StringVar(&o.Out, "out", "", "the directory for the images (required)")
	fs.IntVar(&o.Port, "port", 0, "the port of sind ui on 127.0.0.1 (default: a free one)")
	fs.StringVar(&at, "epoch", epoch, "the time of the still demo, for the clock of the pages")
	fs.BoolVar(&o.NoSandbox, "no-sandbox", os.Geteuid() == 0, "start Chromium without its sandbox (default: when running as root, as in a container)")
	fs.Var(&o.Only, "only", "take only the shot with this name (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if o.Out == "" {
		return errors.New("name the directory for the images with -out")
	}
	var err error
	if o.Epoch, err = time.Parse(time.RFC3339, at); err != nil {
		return fmt.Errorf("-epoch: %w", err)
	}
	list, err := loadShots(o.Shots)
	if err != nil {
		return err
	}
	if o.Chromium, err = findChromium(o.Chromium); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ui, err := startUI(ctx, o.Sind, o.Port)
	if err != nil {
		return err
	}
	defer ui.stop()
	return takeShots(ctx, o, list, ui.url)
}

// options are the flags.
type options struct {
	Sind, Chromium, Shots, Out string
	Port                       int
	Epoch                      time.Time
	NoSandbox                  bool
	Only                       names
}

// names is a repeatable string flag.
type names []string

func (n *names) String() string     { return strings.Join(*n, ",") }
func (n *names) Set(v string) error { *n = append(*n, v); return nil }

// findChromium returns the browser to drive.
func findChromium(given string) (string, error) {
	if given == "" {
		given = os.Getenv("CHROMIUM")
	}
	if given != "" {
		return given, nil
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", errors.New("found no Chromium; install one, or name it with -chromium or $CHROMIUM")
}

// ui is a running sind ui --demo --still.
type ui struct {
	cmd *exec.Cmd
	url string
}

// uiArgs are the arguments of the sind that serves the pages.
func uiArgs(port int) []string {
	return []string{"ui", "--demo", "--still", "--no-open", "--port", strconv.Itoa(port)}
}

// urlOf returns the address in the line sind ui prints for it.
func urlOf(line string) (string, bool) {
	_, url, ok := strings.Cut(line, "sind ui: ")
	return url, ok
}

// startUI starts the still demo and returns when it prints its URL.
func startUI(ctx context.Context, sind string, port int) (*ui, error) {
	cmd := exec.CommandContext(ctx, sind, uiArgs(port)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	// sind ui prints its URL, and its log, on standard error.
	out, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s ui: %w", sind, err)
	}
	u := &ui{cmd: cmd}
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if url, ok := urlOf(sc.Text()); ok {
				found <- url
				break
			}
		}
		_, _ = io.Copy(os.Stderr, out)
	}()
	select {
	case u.url = <-found:
		return u, nil
	case <-time.After(30 * time.Second):
		u.stop()
		return nil, errors.New("sind ui printed no URL")
	case <-ctx.Done():
		u.stop()
		return nil, ctx.Err()
	}
}

// stop interrupts sind ui and waits for it.
func (u *ui) stop() {
	_ = u.cmd.Process.Signal(os.Interrupt)
	_ = u.cmd.Wait()
}
