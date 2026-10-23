// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/pkg/docker"
)

func TestActWorkers_Facts(t *testing.T) {
	inspects := 0
	var fail error
	a := newActWorkers(func(_ context.Context, name docker.ContainerName) (*docker.ContainerInfo, error) {
		inspects++
		if fail != nil {
			return nil, fail
		}
		info := &docker.ContainerInfo{Name: name}
		info.HostConfig.NanoCPUs, info.HostConfig.Memory = 4e9, 2<<30
		return info, nil
	})
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	ctx := t.Context()

	assert.Equal(t, action.WorkerFacts{CPUs: 4, Memory: 2 << 30}, a.facts(ctx, "sind-dev-worker-1"))
	a.facts(ctx, "sind-dev-worker-1")
	assert.Equal(t, 1, inspects, "a preview soon after asks Docker no more")
	now = now.Add(actImageTTL)
	fail = errors.New("no such container")
	assert.Equal(t, action.WorkerFacts{Err: "no such container"}, a.facts(ctx, "sind-dev-worker-1"))
	assert.Equal(t, 2, inspects)
}
