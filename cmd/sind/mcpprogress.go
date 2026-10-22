// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"sync"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-clikit/progress/eventlog"
	"github.com/GSI-HPC/go-clikit/progress/mcpprogress"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolProgressEvery is how often a tool call reads the event log of its
// sind process for new events.
const toolProgressEvery = 100 * time.Millisecond

// withToolProgress prepares a tool call that came with a progress token to
// send the progress of its sind process to the client, as MCP progress
// notifications on the call's session, and returns ctx for the process and
// the function that ends the following, which the call runs once the
// process has ended and before it answers.
//
// The process is a sind of its own with no Bus here, so its work is read
// back from the event log it writes: the default activity log, as for
// every command. The call hands the process a trace of its own
// (TRACEPARENT), and only the lines of that trace go on to an
// mcpprogress.Notifier. A call without a token, or a host with no state
// directory, runs as before.
func withToolProgress(ctx context.Context, req *mcp.CallToolRequest) (context.Context, func()) {
	token := req.Params.GetProgressToken()
	if token == nil || req.Session == nil {
		return ctx, func() {}
	}
	path, err := state.ActivityLogPath()
	if err != nil {
		return ctx, func() {}
	}
	tc, err := progress.NewTraceContext(rand.Reader)
	if err != nil {
		return ctx, func() {}
	}
	send := func(done, total float64, message string) error {
		return req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token, Progress: done, Total: total, Message: message,
		})
	}
	env := append(os.Environ(), "TRACEPARENT="+tc.Traceparent())
	return cmdexec.WithEnv(ctx, env), followToolProgress(path, tc.Trace, send)
}

// followToolProgress reads the event log at path as it grows, until the
// returned function is called, and sends the progress of the lines of the
// trace through send, as an mcpprogress.Notifier does. What the log held
// before is read and left out first, so that a long log is not read again
// on the call's time. The returned function reads what is left, sends it,
// and returns once nothing more will be.
func followToolProgress(path string, trace progress.TraceID, send func(done, total float64, message string) error) func() {
	n := mcpprogress.New(mcpprogress.Options{Program: "sind", Send: send})
	feed := &traceFeed{trace: []byte(trace.String()), next: eventlog.Forward(n)}
	tail := eventlog.NewTail(path, feed)
	// An unreadable log is an empty one: the call has no progress to send.
	_, _ = tail.Poll()
	feed.live = true

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(toolProgressEvery)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				_, _ = tail.Poll()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
			_, _ = tail.Poll()
			n.Close()
		})
	}
}

// traceFeed gives next the lines of one trace, from the moment it is live.
type traceFeed struct {
	trace []byte
	next  eventlog.Feed
	live  bool
}

// Line implements eventlog.Feed.
func (f *traceFeed) Line(b []byte) {
	if f.live && bytes.Contains(b, f.trace) {
		f.next.Line(b)
	}
}

// Reset implements eventlog.Feed.
func (f *traceFeed) Reset() { f.next.Reset() }

// toolProgressRelay carries the progress notifications of the MCP server
// that `sind mcp stream` forwards its calls to back to the client of the
// stream. The server sends them on the session of its own client with the
// token of the forwarded call, a token the relay made up (add), and the
// client of the stream knows its own.
type toolProgressRelay struct {
	mu    sync.Mutex
	next  int
	calls map[int]relayed
}

// relayed is a call whose progress is carried back: the request of the
// stream's client and the token it sent.
type relayed struct {
	req   *mcp.CallToolRequest
	token any
}

// add registers the call of req, which came with a progress token, and
// returns the token to forward it with and the function that forgets it.
func (r *toolProgressRelay) add(req *mcp.CallToolRequest) (token int, forget func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[int]relayed{}
	}
	r.next++
	token = r.next
	r.calls[token] = relayed{req: req, token: req.Params.GetProgressToken()}
	return token, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.calls, token)
	}
}

// handle sends a notification of the inner server on to the client of the
// call it belongs to. A notification of a call that has ended is dropped.
func (r *toolProgressRelay) handle(ctx context.Context, n *mcp.ProgressNotificationClientRequest) {
	token, ok := n.Params.ProgressToken.(float64)
	if !ok {
		if i, isInt := n.Params.ProgressToken.(int); isInt {
			token, ok = float64(i), true
		}
	}
	if !ok {
		return
	}
	r.mu.Lock()
	call, ok := r.calls[int(token)]
	r.mu.Unlock()
	if !ok || call.req.Session == nil {
		return
	}
	_ = call.req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: call.token,
		Progress:      n.Params.Progress,
		Total:         n.Params.Total,
		Message:       n.Params.Message,
	})
}
