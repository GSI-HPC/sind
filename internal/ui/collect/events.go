// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/state"
)

// The defaults of Events.
const (
	// eventsDebounce is how long Events gathers events before it
	// publishes them: a command that starts eight nodes sends dozens.
	eventsDebounce = 200 * time.Millisecond
	// eventsBackoffFirst and eventsBackoffMax bound the wait before the
	// stream is started again; the wait doubles from the first to the
	// second with each stream that fails.
	eventsBackoffFirst = time.Second
	eventsBackoffMax   = 30 * time.Second
	// eventsStable is how long a stream must run to count as working,
	// which resets the wait to the minimum.
	eventsStable = 30 * time.Second
	// maxEventLine is the longest line of the stream that is read: an
	// event carries every label of its container.
	maxEventLine = 16 << 20
)

// EventsArgs are the arguments of the docker command that streams the
// events of sind's containers and networks.
func EventsArgs() []string {
	return []string{
		"events",
		"--filter", "label=" + cluster.LabelRealm,
		"--filter", "type=container",
		"--filter", "type=network",
		"--format", "{{json .}}",
	}
}

// Events streams `docker events` for sind's containers and networks and
// publishes what happens on Bus, debounced. When the stream ends, such as
// with the Docker daemon, it starts it again after a wait that doubles up to
// BackoffMax, and tells the collectors to read everything again, as events
// were missed meanwhile.
type Events struct {
	Exec cmdexec.Executor
	Bus  *Bus
	Log  *slog.Logger

	// Debounce, BackoffMin, BackoffMax and Stable replace the defaults
	// (200 ms, 1 s, 30 s, 30 s) when set.
	Debounce, BackoffMin, BackoffMax, Stable time.Duration
}

// Run streams until ctx ends.
func (e *Events) Run(ctx context.Context) {
	minWait := cmp.Or(e.BackoffMin, eventsBackoffFirst)
	maxWait := cmp.Or(e.BackoffMax, eventsBackoffMax)
	stable := cmp.Or(e.Stable, eventsStable)
	wait := minWait
	for restart := false; ; restart = true {
		if restart {
			e.Bus.Publish(Event{})
		}
		began := time.Now()
		err := e.stream(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) >= stable {
			wait = minWait
		}
		logOr(e.Log).Debug("docker events ended", "err", err, "restart_in", wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		wait = min(2*wait, maxWait)
	}
}

// errStreamEnded is what stream returns for a stream that ended without an
// error.
var errStreamEnded = errors.New("stream ended")

// stream runs one docker events process and publishes its events until it
// ends, which it reports as an error, or ctx does.
func (e *Events) stream(ctx context.Context) error {
	proc, err := e.Exec.Start(ctx, "docker", EventsArgs()...)
	if err != nil {
		return err
	}
	// Cancelling ctx closes the stream, which unblocks the reader.
	stop := context.AfterFunc(ctx, func() { _ = proc.Stdout.Close() })
	defer stop()

	in := make(chan Event, 64)
	done := make(chan struct{})
	var readErr error
	go func() {
		defer close(done)
		defer close(in)
		readErr = readEvents(ctx, proc.Stdout, in)
	}()
	defer func() {
		_ = proc.Close()
		<-done
	}()

	pending := map[Event]struct{}{}
	flush := func() {
		for _, ev := range slices.SortedFunc(maps.Keys(pending), compareEvents) {
			e.Bus.Publish(ev)
		}
		clear(pending)
	}
	var timer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-in:
			if !ok {
				flush()
				<-done
				if readErr != nil {
					return readErr
				}
				return errStreamEnded
			}
			pending[ev] = struct{}{}
			if timer == nil {
				timer = time.After(cmp.Or(e.Debounce, eventsDebounce))
			}
		case <-timer:
			timer = nil
			flush()
		}
	}
}

func compareEvents(a, b Event) int {
	return cmp.Or(cmp.Compare(a.Realm, b.Realm), cmp.Compare(a.Cluster, b.Cluster), cmp.Compare(a.Kind, b.Kind))
}

// dockerEvent is a line of `docker events --format '{{json .}}'`.
type dockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// stateActions are the actions of containers and networks that change what
// sind ui shows. The others, such as the exec_create of each probe, do
// not.
var stateActions = map[string]bool{
	"create": true, "start": true, "die": true, "stop": true, "kill": true,
	"pause": true, "unpause": true, "destroy": true, "remove": true,
	"rename": true, "oom": true, "restart": true, "update": true,
	"connect": true, "disconnect": true,
}

// parseEvent turns a line of the stream into an Event; false for a line
// that is not one of a sind realm, or of an action that changes nothing.
func parseEvent(line []byte) (Event, bool) {
	var de dockerEvent
	if json.Unmarshal(line, &de) != nil {
		return Event{}, false
	}
	attrs := de.Actor.Attributes
	realm := attrs[cluster.LabelRealm]
	if realm == "" || !stateActions[de.Action] {
		return Event{}, false
	}
	ev := Event{Realm: realm, Cluster: attrs[cluster.LabelCluster]}
	switch de.Type {
	case "container":
		ev.Kind = KindContainer
	case "network":
		ev.Kind = KindNetwork
		if attrs["name"] == string(state.LockNetworkName(realm)) {
			ev.Kind, ev.Cluster = KindLock, ""
		}
	default:
		return Event{}, false
	}
	return ev, true
}

// readEvents parses the lines of r into out until r ends or ctx does.
func readEvents(ctx context.Context, r io.Reader, out chan<- Event) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	for sc.Scan() {
		ev, ok := parseEvent(sc.Bytes())
		if !ok {
			continue
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return sc.Err()
}
