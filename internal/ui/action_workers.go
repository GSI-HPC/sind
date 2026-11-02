// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// actWorkers knows the sizes of node containers: the Add workers dialog
// shows that of the worker that new ones take, and what the nodes that run
// take of the host. It keeps what it saw for actImageTTL, as a preview
// comes with each key.
type actWorkers struct {
	inspect func(ctx context.Context, name docker.ContainerName) (*docker.ContainerInfo, error)
	// inspectAll looks at several containers at once, as docker inspect
	// does; nil looks at them one by one.
	inspectAll func(ctx context.Context, names ...docker.ContainerName) ([]*docker.ContainerInfo, error)
	now        func() time.Time

	mu   sync.Mutex
	seen map[string]actSeen[action.WorkerFacts]
}

// newActWorkers returns the workers that inspect and inspectAll, which may
// be nil, look at.
func newActWorkers(inspect func(context.Context, docker.ContainerName) (*docker.ContainerInfo, error),
	inspectAll func(context.Context, ...docker.ContainerName) ([]*docker.ContainerInfo, error)) *actWorkers {
	return &actWorkers{inspect: inspect, inspectAll: inspectAll, now: time.Now, seen: map[string]actSeen[action.WorkerFacts]{}}
}

// sizes tells the sizes of the containers, in their order: those it has
// not seen of late with one docker inspect, and, should that fail, as a
// container of them has gone, one by one.
func (a *actWorkers) sizes(ctx context.Context, containers []string) []action.WorkerFacts {
	out := make([]action.WorkerFacts, len(containers))
	known := make([]bool, len(containers))
	var want []docker.ContainerName
	a.mu.Lock()
	for i, c := range containers {
		if s, ok := a.seen[c]; ok && a.now().Sub(s.at) < actImageTTL {
			out[i], known[i] = s.facts, true
		} else {
			want = append(want, docker.ContainerName(c))
		}
	}
	a.mu.Unlock()
	if len(want) > 0 && a.inspectAll != nil {
		if infos, err := a.inspectAll(ctx, want...); err == nil {
			got := map[string]action.WorkerFacts{}
			a.mu.Lock()
			for _, info := range infos {
				got[string(info.Name)] = actWorkerFactsOf(info)
				a.seen[string(info.Name)] = actSeen[action.WorkerFacts]{got[string(info.Name)], a.now()}
			}
			a.mu.Unlock()
			for i, c := range containers {
				if f, ok := got[c]; ok && !known[i] {
					out[i], known[i] = f, true
				}
			}
		}
	}
	for i, c := range containers {
		if !known[i] {
			out[i] = a.facts(ctx, c)
		}
	}
	return out
}

// actWorkerFactsOf is the size of the container that info describes.
func actWorkerFactsOf(info *docker.ContainerInfo) action.WorkerFacts {
	return action.WorkerFacts{CPUs: int(info.HostConfig.NanoCPUs / 1e9), Memory: info.HostConfig.Memory}
}

// facts tells the size of the container.
func (a *actWorkers) facts(ctx context.Context, container string) action.WorkerFacts {
	a.mu.Lock()
	s, ok := a.seen[container]
	a.mu.Unlock()
	if ok && a.now().Sub(s.at) < actImageTTL {
		return s.facts
	}
	var f action.WorkerFacts
	info, err := a.inspect(ctx, docker.ContainerName(container))
	if err != nil {
		f.Err = err.Error()
	} else {
		f = actWorkerFactsOf(info)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen[container] = actSeen[action.WorkerFacts]{f, a.now()}
	return f
}
