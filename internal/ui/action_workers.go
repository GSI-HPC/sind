// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/docker"
)

// actWorkers knows the sizes of worker containers, which the Add workers
// dialog shows for the new workers that take them. It keeps what it saw for
// actImageTTL, as a preview comes with each key.
type actWorkers struct {
	inspect func(ctx context.Context, name docker.ContainerName) (*docker.ContainerInfo, error)
	now     func() time.Time

	mu   sync.Mutex
	seen map[string]actSeen[action.WorkerFacts]
}

// newActWorkers returns the workers that inspect looks at.
func newActWorkers(inspect func(context.Context, docker.ContainerName) (*docker.ContainerInfo, error)) *actWorkers {
	return &actWorkers{inspect: inspect, now: time.Now, seen: map[string]actSeen[action.WorkerFacts]{}}
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
		f.CPUs, f.Memory = int(info.HostConfig.NanoCPUs/1e9), info.HostConfig.Memory
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen[container] = actSeen[action.WorkerFacts]{f, a.now()}
	return f
}
