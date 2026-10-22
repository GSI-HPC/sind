// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GSI-HPC/go-clikit/localweb/localwebtest"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseEvent is one event of a stream, or a comment line.
type sseEvent struct {
	name, data string
}

// sseClient reads a stream of /events from a real server.
type sseClient struct {
	t *testing.T
	// opening is what the stream opened with: an event for each fragment.
	opening []sseEvent
	events  chan sseEvent
	cancel  context.CancelFunc
	// served closes when the server's handler has returned.
	served chan struct{}
}

// connect opens /events?query in the session of ts, on a real HTTP server,
// and reads what the stream opens with: the retry time and an event for
// each fragment.
func (ts *testServer) connect(query string) *sseClient {
	ts.t.Helper()
	served := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.h.ServeHTTP(w, r)
		close(served)
	}))
	ts.t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	ts.t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/events?"+query, nil)
	require.NoError(ts.t, err)
	req.Host = "127.0.0.1:7878"
	req.AddCookie(ts.cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(ts.t, err)
	require.Equal(ts.t, http.StatusOK, resp.StatusCode)
	require.Equal(ts.t, "text/event-stream", resp.Header.Get("Content-Type"))
	c := &sseClient{t: ts.t, events: make(chan sseEvent, 64), cancel: cancel, served: served}
	go func() {
		defer func() { _ = resp.Body.Close() }()
		defer close(c.events)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		var name string
		var data []string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, ": "):
				c.events <- sseEvent{name: "comment", data: strings.TrimPrefix(line, ": ")}
			case strings.HasPrefix(line, "retry: "):
				c.events <- sseEvent{name: "retry", data: strings.TrimPrefix(line, "retry: ")}
			case line == "" && name != "":
				c.events <- sseEvent{name, strings.Join(data, "\n")}
				name, data = "", nil
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	q, err := url.ParseQuery(query)
	require.NoError(ts.t, err)
	require.Equal(ts.t, sseEvent{"retry", "2000"}, c.next())
	var names []string
	for range q["f"] {
		e := c.next()
		names = append(names, e.name)
		c.opening = append(c.opening, e)
	}
	require.ElementsMatch(ts.t, q["f"], names, "the stream opens with every fragment")
	return c
}

// next returns the next event, or fails after a second.
func (c *sseClient) next() sseEvent {
	c.t.Helper()
	select {
	case e, ok := <-c.events:
		require.True(c.t, ok, "the stream ended")
		return e
	case <-time.After(2 * time.Second):
		require.FailNow(c.t, "no event")
		return sseEvent{}
	}
}

// quiet fails when an event arrives within d.
func (c *sseClient) quiet(d time.Duration) {
	c.t.Helper()
	select {
	case e := <-c.events:
		assert.Fail(c.t, "unexpected event", "%s: %s", e.name, e.data)
	case <-time.After(d):
	}
}

// waitServed waits for the handler to return.
func (c *sseClient) waitServed() {
	c.t.Helper()
	select {
	case <-c.served:
	case <-time.After(2 * time.Second):
		require.FailNow(c.t, "the handler did not return")
	}
}

func TestEventsRefusesBadQueries(t *testing.T) {
	ts := newTestServer(t).login()
	var many []string
	for i := range maxFragments + 1 {
		many = append(many, fmt.Sprintf("f=frag%d", i))
	}
	for query, want := range map[string]int{
		"":                      http.StatusBadRequest,
		"f=nope":                http.StatusBadRequest,
		"f=count&f=nope":        http.StatusBadRequest,
		strings.Join(many, "&"): http.StatusBadRequest,
		"f=one-run&id=" + strings.Repeat("a", maxParamLen+1): http.StatusBadRequest,
		"f=count&nav=" + strings.Repeat("a", maxParamLen+1):  http.StatusBadRequest,
		"f=one-run&id=bad":  http.StatusBadRequest,
		"f=one-run&id=nope": http.StatusNotFound,
		"f=one-run&id=boom": http.StatusInternalServerError,
	} {
		assert.Equal(t, want, ts.get("/events?"+query).Code, query)
	}
}

func TestEventsRequestKeepsDeclaredParamsOnly(t *testing.T) {
	ts := newTestServer(t).login()
	r := request("GET", "/events?f=one-run&f=count&f=one-run&id=x&id=y&other=1&q=2&nav=clusters")
	frags, v, err := ts.srv.eventsRequest(r)
	require.NoError(t, err)
	require.Len(t, frags, 2, "a fragment named twice is rendered once")
	assert.Equal(t, "count", frags[0].Name)
	assert.Equal(t, "one-run", frags[1].Name)
	assert.Equal(t, Params{"id": {"x", "y"}}, v.Params)
	assert.Equal(t, "clusters", v.Nav)
}

// noFlush is a response writer that cannot flush.
type noFlush struct{ http.ResponseWriter }

func TestEventsNeedsAFlusher(t *testing.T) {
	ts := newTestServer(t).login()
	r := request("GET", "/events?f=count")
	r.AddCookie(ts.cookie)
	w := httptest.NewRecorder()
	ts.h.ServeHTTP(noFlush{w}, r)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestEventsSendsAChangedFragmentOnly(t *testing.T) {
	ts := newTestServer(t).login()
	c := ts.connect("f=count&f=same&f=runs&f=one-run&id=x")
	assert.Contains(t, c.opening, sseEvent{"count", "tasks=0"})
	c.quiet(80 * time.Millisecond) // then nothing until a change

	// A task changes the count. "same" depends on the topic too, renders
	// the same HTML, and is not sent.
	ts.st.AddTask(store.Task{ID: "t1"})
	assert.Equal(t, sseEvent{"count", "tasks=1"}, c.next())
	c.quiet(80 * time.Millisecond)

	// A run changes: "runs" depends on the prefix, "one-run" on its run.
	ts.st.SetRuns([]store.Run{{ID: "x", Title: "delete cluster dev"}, {ID: "y"}}, "x")
	got := map[string]string{}
	for range 2 {
		e := c.next()
		got[e.name] = e.data
	}
	assert.Equal(t, map[string]string{"runs": "runs=2", "one-run": "run=delete cluster dev"}, got)
	c.quiet(80 * time.Millisecond)

	// Another run changes: "one-run" is not about it, and "runs" is the
	// same HTML as before.
	ts.st.SetRuns([]store.Run{{ID: "x", Title: "delete cluster dev"}, {ID: "z"}}, "z")
	c.quiet(120 * time.Millisecond)
	// A change back to what was sent before sends again, as the browser
	// holds the newer HTML.
	ts.st.SetRuns([]store.Run{{ID: "x", Title: "delete cluster dev"}}, "z")
	assert.Equal(t, sseEvent{"runs", "runs=1"}, c.next())
}

func TestEventsCoalescesChanges(t *testing.T) {
	ts := newTestServer(t).login()
	ts.srv.coalesce = 150 * time.Millisecond
	c := ts.connect("f=count")
	for i := range 3 {
		ts.st.AddTask(store.Task{ID: fmt.Sprint(i)})
	}
	assert.Equal(t, sseEvent{"count", "tasks=3"}, c.next(), "one render for the burst")
	c.quiet(250 * time.Millisecond)
}

func TestEventsOpenWithAChangeBetweenPageAndStream(t *testing.T) {
	// A change that lands after the page was rendered, before the stream
	// opens, is in what the stream opens with, and is not sent again.
	ts := newTestServer(t).login()
	ts.st.AddTask(store.Task{ID: "early"})
	c := ts.connect("f=count")
	assert.Equal(t, []sseEvent{{"count", "tasks=1"}}, c.opening)
	c.quiet(80 * time.Millisecond)
	ts.st.AddTask(store.Task{ID: "late"})
	assert.Equal(t, sseEvent{"count", "tasks=2"}, c.next())
}

func TestEventsRailFollowsAnyTopicAndNav(t *testing.T) {
	ts := newTestServer(t).login()
	ts.srv.reg.SetNavCount("nodes", func(sn store.Snapshot) NavCount {
		return NavCount{Text: fmt.Sprint(len(sn.Runs))}
	})
	c := ts.connect("f=rail&nav=nodes")
	ts.st.SetRuns([]store.Run{{ID: "a"}, {ID: "b"}})
	e := c.next()
	assert.Equal(t, "rail", e.name)
	assert.Contains(t, e.data, `<a class="nav on" href="/nodes" aria-current="page"><span class="g">▪</span>Nodes<span class="n">2</span></a>`)
}

func TestEventsKeepAliveAndDisconnect(t *testing.T) {
	ts := newTestServer(t).login()
	ts.srv.keepAlive = 30 * time.Millisecond
	c := ts.connect("f=count")
	assert.Equal(t, sseEvent{"comment", "keep-alive"}, c.next())
	assert.Equal(t, sseEvent{"comment", "keep-alive"}, c.next())
	c.cancel()
	c.waitServed()
}

func TestEventsEndWithTheServer(t *testing.T) {
	ts := newTestServer(t).login()
	c := ts.connect("f=count")
	ts.cancel()
	c.waitServed()
}

func TestEventsSkipAFragmentThatFails(t *testing.T) {
	ts := newTestServer(t).login()
	var logs bytes.Buffer
	ts.srv.log = slog.New(slog.NewTextHandler(&logs, nil))
	c := ts.connect("f=one-run&f=count&id=x")
	// The run goes away: "one-run" cannot render now, "count" goes on.
	ts.st.SetRuns(nil, "x")
	ts.st.AddTask(store.Task{ID: "t"})
	assert.Equal(t, sseEvent{"count", "tasks=1"}, c.next())
	c.quiet(50 * time.Millisecond)
	assert.Contains(t, logs.String(), `fragment=one-run`)
	assert.Contains(t, logs.String(), "not found")
}

// failingWriter is a streaming response writer whose writes fail from the
// failAfter'th on, and whose flush then reports it, as net/http's does.
type failingWriter struct {
	mu        sync.Mutex
	header    http.Header
	writes    int
	failAfter int
}

func (w *failingWriter) Header() http.Header { return w.header }
func (w *failingWriter) WriteHeader(int)     {}
func (w *failingWriter) Flush()              {}
func (w *failingWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writes >= w.failAfter {
		return errors.New("broken pipe")
	}
	return nil
}
func (w *failingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.writes >= w.failAfter {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
}

func TestEventsEndWhenAWriteFails(t *testing.T) {
	ts := newTestServer(t).login()
	for name, tc := range map[string]struct {
		failAfter int
		trigger   func()
	}{
		// Write 1 is "retry", write 2 the event the stream opens with; the
		// next is an event or a keep-alive.
		"opening":    {2, func() {}},
		"event":      {3, func() { ts.st.AddTask(store.Task{ID: "t"}) }},
		"keep-alive": {3, func() {}},
	} {
		t.Run(name, func(t *testing.T) {
			ts.srv.keepAlive = time.Hour
			if name == "keep-alive" {
				ts.srv.keepAlive = 20 * time.Millisecond
			}
			w := &failingWriter{header: http.Header{}, failAfter: tc.failAfter}
			r := request("GET", "/events?f=count")
			r.AddCookie(ts.cookie)
			done := make(chan struct{})
			go func() { ts.h.ServeHTTP(w, r); close(done) }()
			time.Sleep(50 * time.Millisecond)
			tc.trigger()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("the handler did not return after a failed write")
			}
		})
	}
}

func TestLiveEvents(t *testing.T) {
	ts, base := liveServer(t)
	c := localwebtest.Login(t, base, "the-token")
	r := c.Events(t, "/events?f=count&f=same")
	assert.Equal(t, sseEvent{"count", "tasks=0"}, readEvent(t, r))
	assert.Equal(t, sseEvent{"same", "always the same"}, readEvent(t, r))
	assert.Equal(t, "2000", r.Retry)

	ts.st.AddTask(store.Task{ID: "t1"})
	assert.Equal(t, sseEvent{"count", "tasks=1"}, readEvent(t, r))
}

// readEvent returns the next event of r.
func readEvent(t *testing.T, r *localwebtest.Reader) sseEvent {
	t.Helper()
	e, err := r.Next()
	require.NoError(t, err)
	return sseEvent{e.Name, e.Data}
}
