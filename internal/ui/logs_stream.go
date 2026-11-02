// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
)

// The log stream, GET /logs/{realm}/{cluster}?node=&source=&unit=&lines=,
// runs one child per open stream: `docker logs --follow --tail N` for
// source docker, and `docker exec NODE journalctl --follow -n N` (with -u
// UNIT for one unit) for source journal, the commands of `sind logs -f -n
// N`. N is one of logsLineCounts, 200 by default.
//
// Events: "line" carries one log line as plain text (the page adds it with
// textContent), "notice" a line sind ui adds (lines skipped, the end of
// the stream), "end" closes the stream for good.
const (
	// logsMaxLine is the longest line sent, in bytes; the rest is cut.
	logsMaxLine = 4096
	// logsMaxRate is the most lines per second sent after the first N; the
	// rest is skipped. The first N wait for the client instead.
	logsMaxRate = 1000
	// logsTailPause is how long the input may pause within the first N
	// lines. A longer pause means the log had fewer lines than N: what
	// follows is live, and keeps the limit. logsTailStart is the same for
	// the first line, which waits for the child to start.
	logsTailPause = 500 * time.Millisecond
	logsTailStart = 5 * time.Second
	// logsBuffer is how many lines wait for a slow client before lines are
	// skipped.
	logsBuffer = 256
	// logsMaxStreams is the most streams, and so children, at one time.
	logsMaxStreams = 16
)

// logsStreams counts the open streams.
var logsStreams atomic.Int32

// logsUnitRE is what a systemd unit name may look like. It starts with a
// letter or digit, so that it is never read as an option of journalctl.
var logsUnitRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.@-]{0,127}$`)

// logsANSIRE matches the terminal escape sequences of a log line.
var logsANSIRE = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|[@-Z\\-_])`)

func init() {
	defaultRegistry.AddRoute(logsRoute)
}

// logsRoute is the stream.
var logsRoute = Route{Pattern: "GET /logs/{realm}/{cluster}", Handle: logsHandle, FetchOnly: true}

// logsSource is the kind of log that a stream follows.
type logsSource string

const (
	logsDocker  logsSource = "docker"
	logsJournal logsSource = "journal"
)

// logsReq is a request of a stream, checked: what to follow and how many
// lines to start with.
type logsReq struct {
	src                        logsSource
	realm, cluster, node, unit string
	tail                       int
}

// logsArgs returns the docker arguments of a stream. Every value in them
// is a name that the store knows, or a unit that passed logsUnitRE.
func logsArgs(q logsReq) []string {
	tail := q.tail
	opts := cluster.LogOptions{Follow: true, Tail: &tail}
	if q.src == logsDocker {
		return cluster.ContainerLogArgs(q.realm, q.node, q.cluster, opts)
	}
	if q.unit != "" {
		return cluster.ServiceLogArgs(q.realm, q.node, q.cluster, q.unit, opts)
	}
	return []string{"exec", string(cluster.ContainerName(q.realm, q.cluster, q.node)),
		"journalctl", "--follow", "-n", fmt.Sprint(tail)}
}

// logsCheck validates the request against the store.
func logsCheck(s *Server, r *http.Request) (logsReq, error) {
	v, err := s.View(r, "node", "source", "unit", "lines")
	if err != nil {
		return logsReq{}, err
	}
	q := logsReq{realm: v.Params.Get("realm"), cluster: v.Params.Get("cluster"),
		node: v.Params.Get("node"), unit: v.Params.Get("unit"), tail: logsLineCounts[0]}
	if q.node == "" || !pgHasNode(v.Snap, q.realm, q.cluster, q.node) {
		return logsReq{}, fmt.Errorf("node %s of %s: %w", q.node, q.cluster, ErrNotFound)
	}
	switch logsSource(v.Params.Get("source")) {
	case logsDocker, "":
		q.src, q.unit = logsDocker, ""
	case logsJournal:
		q.src = logsJournal
	default:
		return logsReq{}, fmt.Errorf("source: want docker or journal: %w", ErrBadRequest)
	}
	if q.unit != "" && !logsUnitRE.MatchString(q.unit) {
		return logsReq{}, fmt.Errorf("unit: not a systemd unit name: %w", ErrBadRequest)
	}
	if l := v.Params.Get("lines"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || !slices.Contains(logsLineCounts, n) {
			return logsReq{}, fmt.Errorf("lines: want one of %v: %w", logsLineCounts, ErrBadRequest)
		}
		q.tail = n
	}
	return q, nil
}

// logsHandle streams the log of one node until the client leaves, the
// server stops or the child ends.
func logsHandle(s *Server, w http.ResponseWriter, r *http.Request) {
	q, err := logsCheck(s, r)
	if err != nil {
		s.Fail(w, r, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if logsStreams.Add(1) > logsMaxStreams {
		logsStreams.Add(-1)
		http.Error(w, "too many log streams are open", http.StatusTooManyRequests)
		return
	}
	defer logsStreams.Add(-1)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// What the container or journalctl writes to its standard error is
	// part of the log.
	proc, err := s.env.Exec.Start(cmdexec.WithStderrMerged(ctx), "docker", logsArgs(q)...)
	if err != nil {
		s.log.Error("starting the log stream", "node", q.node, "err", err)
		http.Error(w, "could not start the log stream", http.StatusBadGateway)
		return
	}
	lines := make(chan string, logsBuffer)
	var dropped atomic.Int64
	readerDone, stop := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(lines)
		logsRead(proc.Stdout, lines, &dropped, time.Now, q.tail, stop)
	}()
	defer func() {
		close(stop)      // a reader that waits for the client ends
		_ = proc.Close() // kills the child; the reader then ends
		<-readerDone
	}()

	// The server's write deadline is for answers; a log stream lasts as
	// long as the client reads it.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": log stream\n\n")
	flusher.Flush()

	skipped := func() error {
		if n := dropped.Swap(0); n > 0 {
			return logsEvent(w, "notice", fmt.Sprintf("%d lines skipped: the stream was too fast", n))
		}
		return nil
	}
	keepAlive := time.NewTicker(s.keepAlive)
	defer keepAlive.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-keepAlive.C:
			if err = skipped(); err == nil {
				_, err = io.WriteString(w, ": keep-alive\n\n")
			}
		case line, open := <-lines:
			if !open {
				_ = skipped()
				_ = logsEvent(w, "notice", "the log ended")
				_ = logsEvent(w, "end", "")
				flusher.Flush()
				return
			}
			if err = skipped(); err == nil {
				err = logsEvent(w, "line", line)
			}
		}
		if err != nil {
			return
		}
		flusher.Flush()
	}
}

// logsRead reads lines of r into out until r ends or stop closes. The
// first lines, as many as first, wait for the reader of out: they are the
// tail that the stream starts with. The tail also ends when the input
// pauses (logsTailPause, logsTailStart for the first line): the log had
// fewer lines. After the tail it never blocks on a slow reader of out, nor
// lets through more than logsMaxRate lines a second: it counts the lines
// it drops in dropped.
func logsRead(r io.Reader, out chan<- string, dropped *atomic.Int64, now func() time.Time, first int, stop <-chan struct{}) {
	br := bufio.NewReaderSize(r, logsMaxLine)
	var windowStart time.Time
	count := 0
	pause := logsTailStart
	for {
		start := now()
		line, err := logsReadLine(br)
		t := now()
		if t.Sub(start) > pause {
			first = 0
		}
		pause = logsTailPause
		switch {
		case line == "" && err != nil:
			// The input ended after a whole line.
		case first > 0:
			first--
			select {
			case out <- line:
			case <-stop:
				return
			}
		default:
			if t.Sub(windowStart) >= time.Second {
				windowStart, count = t, 0
			}
			count++
			if count > logsMaxRate {
				dropped.Add(1)
			} else {
				select {
				case out <- line:
				default:
					dropped.Add(1)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// logsReadLine returns the next line, cleaned and cut to logsMaxLine
// bytes, and the error that ended the input (with the last, unfinished
// line, if any).
func logsReadLine(br *bufio.Reader) (string, error) {
	chunk, err := br.ReadSlice('\n')
	line := string(chunk)
	if errors.Is(err, bufio.ErrBufferFull) {
		// Cut the line, and drop the rest of it.
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = br.ReadSlice('\n')
		}
		line += " [line cut]"
	}
	return logsClean(line), err
}

// logsClean makes a log line safe and plain: no escape sequences, no
// control characters but tab, valid UTF-8, no line ending.
func logsClean(s string) string {
	s = strings.TrimRight(s, "\r\n")
	s = logsANSIRE.ReplaceAllString(s, "")
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if r == '\t' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r >= 0xa0)) {
			return r
		}
		return -1
	}, s)
}

// logsEvent writes one event. data is one line: logsClean removed every
// line break, and an event with no data still has its empty data line.
func logsEvent(w io.Writer, name, data string) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}
