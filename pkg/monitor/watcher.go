// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"io"
	"sync"

	"github.com/GSI-HPC/sind/pkg/cmdexec"
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

	mu          sync.Mutex
	subscribers []chan Event

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

// Subscribe returns a channel that receives a copy of every event.
// The channel is closed when the Watcher stops. The caller should
// drain the channel to avoid blocking the broadcast loop.
func (w *Watcher) Subscribe() <-chan Event {
	ch := make(chan Event, 64)
	w.mu.Lock()
	w.subscribers = append(w.subscribers, ch)
	w.mu.Unlock()
	return ch
}

// Unsubscribe removes a previously subscribed channel and closes it.
func (w *Watcher) Unsubscribe(ch <-chan Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, sub := range w.subscribers {
		if sub == ch {
			w.subscribers = append(w.subscribers[:i], w.subscribers[i+1:]...)
			close(sub)
			return
		}
	}
}

// Start begins monitoring. It starts the docker events stream, spawns
// systemd monitors for each given node, and starts the broadcast loop.
// Cancel the context to stop all monitors.
func (w *Watcher) Start(ctx context.Context, nodes []NodeTarget) error {
	log := sindlog.From(ctx)

	// Start docker events monitor.
	dockerArgs := DockerEventsArgs(w.clusterName)
	proc, err := w.executor.Start(ctx, "docker", dockerArgs...)
	if err != nil {
		log.Log(ctx, sindlog.LevelTrace, "failed to start docker events monitor", "err", err)
		return err
	}

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
		if err := dm.Run(ctx, stdout, w.internalCh); err != nil && ctx.Err() == nil {
			select {
			case w.internalCh <- Event{Kind: EventMonitorError, Err: err, Detail: "docker events stream failed"}:
			case <-ctx.Done():
			}
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
	for _, ch := range w.subscribers {
		close(ch)
	}
	w.subscribers = nil
}

// AddNodes starts systemd monitors for the given nodes. Call this
// after the node containers have been created so that systemd state
// changes can accelerate readiness probing.
func (w *Watcher) AddNodes(ctx context.Context, nodes []NodeTarget) {
	for _, node := range nodes {
		w.startSystemdMonitor(ctx, node)
	}
}

func (w *Watcher) startSystemdMonitor(ctx context.Context, node NodeTarget) {
	log := sindlog.From(ctx)
	args := SystemdMonitorArgs(node.Container)
	proc, err := w.executor.Start(ctx, "docker", args...)
	if err != nil {
		log.Log(ctx, sindlog.LevelTrace, "failed to start systemd monitor", "node", node.ShortName, "err", err)
		select {
		case w.internalCh <- Event{
			Kind:      EventMonitorError,
			Node:      node.ShortName,
			Container: node.Container,
			Err:       err,
		}:
		case <-ctx.Done():
		}
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
		if err := sm.Run(ctx, stdout, w.internalCh); err != nil && ctx.Err() == nil {
			select {
			case w.internalCh <- Event{
				Kind:      EventMonitorError,
				Node:      node.ShortName,
				Container: node.Container,
				Err:       err,
				Detail:    "systemd monitor stream failed",
			}:
			case <-ctx.Done():
			}
		}
		close(runDone)
	}()
}

// broadcastLoop reads events from the internal channel and sends them
// to all subscribers. It runs until the context is cancelled.
func (w *Watcher) broadcastLoop(ctx context.Context) {
	log := sindlog.From(ctx)
	for {
		select {
		case ev := <-w.internalCh:
			log.Log(ctx, sindlog.LevelTrace, "event", "kind", ev.Kind, "node", ev.Node, "unit", ev.Unit, "detail", ev.Detail)
			w.mu.Lock()
			for _, ch := range w.subscribers {
				select {
				case ch <- ev:
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
