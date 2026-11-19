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
	"strings"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
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
// events of containers and networks that change what sind ui shows. It
// filters by the actions of stateActions but not by the label sind.realm:
// the daemon matches a label filter against the attributes of an event,
// and a network event has none of the network's labels, only its name and
// type, so that filter would drop every network event. parseEvent finds
// the realm of an event instead.
func EventsArgs() []string {
	args := []string{"events", "--filter", "type=container", "--filter", "type=network"}
	for _, action := range slices.Sorted(maps.Keys(stateActions)) {
		args = append(args, "--filter", "event="+action)
	}
	return append(args, "--format", "{{json .}}")
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
	// Realms returns the realms that sind ui knows, by which the event of
	// a cluster's network <realm>-<cluster>-net finds its realm; nil knows
	// none, and such an event is dropped.
	Realms func() []string

	// Debounce, BackoffMin, BackoffMax and Stable replace the defaults
	// (200 ms, 1 s, 30 s, 30 s) when set.
	Debounce, BackoffMin, BackoffMax, Stable time.Duration
}

// StoreRealms returns, for the Realms of Events, the names of the realms
// that st lists.
func StoreRealms(st *store.Store) func() []string {
	return func() []string {
		realms := st.Snapshot().Realms
		names := make([]string, len(realms))
		for i, r := range realms {
			names[i] = r.Name
		}
		return names
	}
}

// Run streams until ctx ends.
func (e *Events) Run(ctx context.Context) {
	minWait := cmp.Or(e.BackoffMin, eventsBackoffFirst)
	maxWait := cmp.Or(e.BackoffMax, eventsBackoffMax)
	stable := cmp.Or(e.Stable, eventsStable)
	wait := minWait
	for restart := false; ; restart = true {
		if restart {
			e.Bus.Notify(Event{})
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
// ends, which it reports as an error, or ctx does. The error of a stream
// whose docker command failed says why, in docker's words.
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
		readErr = readEvents(ctx, proc.Stdout, e.Realms, in)
	}()
	defer func() {
		_ = proc.Close()
		<-done
	}()

	pending := map[Event]struct{}{}
	flush := func() {
		for _, ev := range slices.SortedFunc(maps.Keys(pending), compareEvents) {
			e.Bus.Notify(ev)
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
				if err := errors.Join(readErr, docker.ClassifyUnreachable(proc.Close())); err != nil {
					return err
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

// parseEvent turns a line of the stream into the Events it means; none for
// a line that is not one of a sind realm, or of an action that changes
// nothing. A container names its realm by its label sind.realm, or, for the
// DNS, the SSH relay and the monitoring stack, by its Compose project
// <realm>-mesh. A network event carries only the network's name: <realm>-lock
// is the realm lock, <realm>-mesh the mesh, and <realm>-<cluster>-net the
// network of a cluster in one of realms, whose names can hold a hyphen, so a
// name can fit more than one.
func parseEvent(line []byte, realms []string) []Event {
	var de dockerEvent
	if json.Unmarshal(line, &de) != nil || !stateActions[de.Action] {
		return nil
	}
	attrs := de.Actor.Attributes
	switch de.Type {
	case "container":
		realm := attrs[cluster.LabelRealm]
		if realm == "" {
			realm = meshRealm(attrs[docker.ComposeProjectLabel])
		}
		if realm == "" {
			return nil
		}
		return []Event{{Kind: KindChange, Realm: realm, Cluster: attrs[cluster.LabelCluster]}}
	case "network":
		name := attrs["name"]
		if realm, ok := strings.CutSuffix(name, "-lock"); ok && validRealm(realm) {
			return []Event{{Kind: KindLock, Realm: realm}}
		}
		if realm := meshRealm(name); realm != "" {
			return []Event{{Kind: KindChange, Realm: realm}}
		}
		var out []Event
		for _, realm := range realms {
			if cl, ok := strings.CutPrefix(name, realm+"-"); ok {
				if cl, ok = strings.CutSuffix(cl, "-net"); ok && cl != "" {
					out = append(out, Event{Kind: KindChange, Realm: realm, Cluster: cl})
				}
			}
		}
		return out
	}
	return nil
}

// meshRealm returns the realm of the mesh network or Compose project
// <realm>-mesh; empty for another name.
func meshRealm(name string) string {
	realm, _ := mesh.ProjectRealm(name)
	return realm
}

// validRealm reports whether realm may be the name of a realm, which leaves
// out most networks and Compose projects of others that end alike.
func validRealm(realm string) bool { return config.CheckName("realm", realm) == nil }

// readEvents parses the lines of r into out until r ends or ctx does;
// realms, when not nil, returns the realms that sind ui knows.
func readEvents(ctx context.Context, r io.Reader, realms func() []string, out chan<- Event) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	for sc.Scan() {
		var known []string
		if realms != nil {
			known = realms()
		}
		for _, ev := range parseEvent(sc.Bytes(), known) {
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return sc.Err()
}
