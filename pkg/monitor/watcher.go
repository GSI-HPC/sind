// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// closeOnce wraps an io.ReadCloser so that Close can be called multiple
// times safely. Only the first call actually closes the underlying reader.
type closeOnce struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (c *closeOnce) Close() error {
	c.once.Do(func() { c.err = c.ReadCloser.Close() })
	return c.err
}

// Watcher monitors a sind cluster for state changes across all nodes.
// It manages the lifecycle of docker events and per-node systemd monitors,
// and broadcasts events to subscribers.
type Watcher struct {
	executor    cmdexec.Executor
	clusterName string
	prefix      string

	internalCh chan Event

	// ctx is the context Start was given. Every monitor, including those
	// AddNodes starts later, runs until it ends.
	ctx context.Context

	mu          sync.Mutex
	subscribers []subscriber

	wg sync.WaitGroup
}

// NewWatcher creates a Watcher for the given cluster. The executor is used
// to start long-lived streaming processes (docker events, busctl monitor).
func NewWatcher(executor cmdexec.Executor, containerPrefix, clusterName string) *Watcher {
	return &Watcher{
		executor:    executor,
		clusterName: clusterName,
		prefix:      containerPrefix,
		internalCh:  make(chan Event, 64),
	}
}

// subscriber is a channel that receives events, of one container or of
// all (container empty).
type subscriber struct {
	ch        chan Event
	container docker.ContainerName
}

// wants reports whether the subscriber receives ev: an event of its
// container, or a monitor error of the whole watcher, which no container's
// subscription may miss.
func (s subscriber) wants(ev Event) bool {
	return s.container == "" || ev.Container == s.container ||
		ev.Kind == EventMonitorError && ev.Container == ""
}

// Subscribe returns a channel that receives a copy of every event.
// The channel is closed when the Watcher stops. The caller should
// drain the channel to avoid blocking the broadcast loop.
func (w *Watcher) Subscribe() <-chan Event {
	return w.SubscribeTo("")
}

// SubscribeTo is Subscribe for the events of one container, or of every
// container when it is empty. A readiness wait for one node subscribes to
// its container only: while many nodes boot, the other nodes' events would
// otherwise fill its buffer and crowd out its own. A subscription to one
// container also receives the monitor errors of the whole watcher (those
// without a container), as its container's events are missing after one.
func (w *Watcher) SubscribeTo(container docker.ContainerName) <-chan Event {
	ch := make(chan Event, 64)
	w.mu.Lock()
	w.subscribers = append(w.subscribers, subscriber{ch: ch, container: container})
	w.mu.Unlock()
	return ch
}

// Unsubscribe removes a previously subscribed channel and closes it.
func (w *Watcher) Unsubscribe(ch <-chan Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, sub := range w.subscribers {
		if sub.ch == ch {
			w.subscribers = append(w.subscribers[:i], w.subscribers[i+1:]...)
			close(sub.ch)
			return
		}
	}
}

// Start begins monitoring. It starts the docker events stream, spawns
// systemd monitors for each given node, and starts the broadcast loop.
// Cancel the context to stop all monitors, those AddNodes starts later
// included.
func (w *Watcher) Start(ctx context.Context, nodes []NodeTarget) error {
	log := sindlog.From(ctx)

	// Start docker events monitor.
	dockerArgs := DockerEventsArgs(w.clusterName)
	proc, err := w.executor.Start(ctx, "docker", dockerArgs...)
	if err != nil {
		log.Log(ctx, sindlog.LevelTrace, "failed to start docker events monitor", "err", err)
		return err
	}
	w.ctx = ctx

	dm := NewDockerMonitor(w.prefix)

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() { _ = proc.Close() }()
		stdout := &closeOnce{ReadCloser: proc.Stdout}
		runDone := make(chan struct{})
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			select {
			case <-ctx.Done():
			case <-runDone:
			}
			_ = stdout.Close()
		}()
		if detail, err := streamEnd("docker events", dm.Run(ctx, stdout, w.internalCh)); ctx.Err() == nil {
			w.emit(ctx, Event{Kind: EventMonitorError, Err: err, Detail: detail})
		}
		close(runDone)
	}()

	// Start systemd monitors for pre-existing nodes.
	for _, node := range nodes {
		w.startSystemdMonitor(ctx, node)
	}

	// Start broadcast loop.
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.broadcastLoop(ctx)
	}()

	return nil
}

// Wait blocks until all monitor goroutines have exited and closes
// all remaining subscriber channels. Any Unsubscribe calls must complete
// before Wait runs; otherwise a concurrent Unsubscribe would double-close
// the channel Wait is iterating. Callers using the usual
// defer watcher.Wait() / defer watcher.Unsubscribe(ch) pattern get this
// ordering for free (deferred calls run LIFO).
func (w *Watcher) Wait() {
	w.wg.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, sub := range w.subscribers {
		close(sub.ch)
	}
	w.subscribers = nil
}

// AddNodes starts systemd monitors for the given nodes. Call this
// after Start and after the node containers have been created so that
// systemd state changes can accelerate readiness probing. The monitors run
// on the context Start was given, until the watcher stops, so that they
// also serve the waits that follow the caller's own (e.g. for the Slurm
// daemons). Before a successful Start, AddNodes does nothing.
func (w *Watcher) AddNodes(nodes []NodeTarget) {
	if w.ctx == nil {
		return
	}
	for _, node := range nodes {
		w.startSystemdMonitor(w.ctx, node)
	}
}

func (w *Watcher) startSystemdMonitor(ctx context.Context, node NodeTarget) {
	log := sindlog.From(ctx)
	args := SystemdMonitorArgs(node.Container)
	proc, err := w.executor.Start(ctx, "docker", args...)
	if err != nil {
		log.Log(ctx, sindlog.LevelTrace, "failed to start systemd monitor", "node", node.ShortName, "err", err)
		w.emit(ctx, Event{
			Kind:      EventMonitorError,
			Node:      node.ShortName,
			Container: node.Container,
			Err:       err,
		})
		return
	}

	sm := NewSystemdMonitor(node.ShortName, node.Container)

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() { _ = proc.Close() }()
		stdout := &closeOnce{ReadCloser: proc.Stdout}
		runDone := make(chan struct{})
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			select {
			case <-ctx.Done():
			case <-runDone:
			}
			_ = stdout.Close()
		}()
		if detail, err := streamEnd("systemd monitor", sm.Run(ctx, stdout, w.internalCh)); ctx.Err() == nil {
			w.emit(ctx, Event{
				Kind:      EventMonitorError,
				Node:      node.ShortName,
				Container: node.Container,
				Err:       err,
				Detail:    detail,
			})
		}
		close(runDone)
	}()
}

// errStreamEnded is the error of a monitor stream that ended without one,
// such as the busctl monitor of a node that stopped, or of an image without
// busctl.
var errStreamEnded = errors.New("stream ended")

// streamEnd returns the detail and the error of the EventMonitorError for a
// monitor stream that stopped with err, nil when it ended cleanly.
func streamEnd(source string, err error) (string, error) {
	if err != nil {
		return source + " stream failed", err
	}
	return source + " stream ended", errStreamEnded
}

// emit best-effort sends ev to the internal channel. The ctx.Done() branch
// guards against sending into a drained channel after the watcher has been
// torn down — a race that is possible when ctx is cancelled between the
// caller's guard check and this select.
func (w *Watcher) emit(ctx context.Context, ev Event) {
	select {
	case w.internalCh <- ev:
	case <-ctx.Done():
	}
}

// broadcastLoop reads events from the internal channel and sends them
// to the subscribers that want them. It runs until the context is
// cancelled.
func (w *Watcher) broadcastLoop(ctx context.Context) {
	log := sindlog.From(ctx)
	for {
		select {
		case ev := <-w.internalCh:
			log.Log(ctx, sindlog.LevelTrace, "event", "kind", ev.Kind, "node", ev.Node, "unit", ev.Unit, "detail", ev.Detail)
			w.mu.Lock()
			for _, sub := range w.subscribers {
				if !sub.wants(ev) {
					continue
				}
				select {
				case sub.ch <- ev:
				default:
					// Subscriber is full — drop event to avoid blocking.
					log.Log(ctx, sindlog.LevelTrace, "dropped event for full subscriber", "kind", ev.Kind, "node", ev.Node)
				}
			}
			w.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}
