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
	}, nil)
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

func TestActWorkers_Sizes(t *testing.T) {
	var one, all []docker.ContainerName
	var failAll error
	size := func(name docker.ContainerName) *docker.ContainerInfo {
		info := &docker.ContainerInfo{Name: name}
		info.HostConfig.NanoCPUs, info.HostConfig.Memory = 1e9, int64(len(name))<<20
		return info
	}
	a := newActWorkers(func(_ context.Context, name docker.ContainerName) (*docker.ContainerInfo, error) {
		one = append(one, name)
		if name == "gone" {
			return nil, errors.New("no such container: gone")
		}
		return size(name), nil
	}, func(_ context.Context, names ...docker.ContainerName) ([]*docker.ContainerInfo, error) {
		all = append(all, names...)
		if failAll != nil {
			return nil, failAll
		}
		var out []*docker.ContainerInfo
		for _, n := range names {
			if n != "skipped" {
				out = append(out, size(n))
			}
		}
		return out, nil
	})
	now := time.Unix(1000, 0)
	a.now = func() time.Time { return now }
	ctx := t.Context()

	a.facts(ctx, "seen")
	one = nil
	got := a.sizes(ctx, []string{"seen", "ab", "skipped", "ab"})
	assert.Equal(t, []action.WorkerFacts{{CPUs: 1, Memory: 4 << 20}, {CPUs: 1, Memory: 2 << 20}, {CPUs: 1, Memory: 7 << 20}, {CPUs: 1, Memory: 2 << 20}}, got)
	assert.Equal(t, []docker.ContainerName{"ab", "skipped", "ab"}, all, "one inspect for those not seen")
	assert.Equal(t, []docker.ContainerName{"skipped"}, one, "what it did not return, one by one")

	// A failed inspect of all looks at each.
	now = now.Add(actImageTTL)
	all, one, failAll = nil, nil, errors.New("exit status 1")
	got = a.sizes(ctx, []string{"ab", "gone"})
	assert.Equal(t, []action.WorkerFacts{{CPUs: 1, Memory: 2 << 20}, {Err: "no such container: gone"}}, got)
	assert.Equal(t, []docker.ContainerName{"ab", "gone"}, one)

	all = nil
	a.sizes(ctx, []string{"ab"})
	assert.Empty(t, all, "seen of late")

	a.inspectAll = nil
	one = nil
	a.sizes(ctx, []string{"new"})
	assert.Equal(t, []docker.ContainerName{"new"}, one)
}
