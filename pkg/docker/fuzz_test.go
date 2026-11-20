// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"strings"
	"testing"

	"github.com/GSI-HPC/sind/internal/mock"
	"github.com/stretchr/testify/assert"
)

// FuzzParseLabels checks the reader of the label string of docker ps: no
// string makes it panic, and each label it returns is a part of the
// string.
func FuzzParseLabels(f *testing.F) {
	for _, seed := range []string{
		"",
		"sind.realm=sind,sind.cluster=dev,sind.role=worker",
		"a=b=c,d",
		",,=",
		"sind.data.hostpath=/srv/a,b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		labels := parseLabels(s)
		assert.LessOrEqual(t, len(labels), strings.Count(s, ",")+1)
		for k, v := range labels {
			assert.Contains(t, s, k)
			assert.Contains(t, s, v)
			assert.NotContains(t, k, "=")
			assert.NotContains(t, k+v, ",")
		}
	})
}

// FuzzClientOutput checks the readers of what the docker CLI prints: no
// output makes a method of the client panic. A method fails with an error
// for output it cannot read.
func FuzzClientOutput(f *testing.F) {
	for _, seed := range []string{
		"",
		psJSON,
		`{"ID":"a","Names":"sind-dev-controller","State":"exited","Status":"Exited (0) 3 days ago","Image":"img","Networks":"sind-dev-net,sind-mesh"}`,
		`[{"Name":"/sind-dev-controller","State":{"Status":"running"},"Config":{"Labels":{"a":"b"}},"NetworkSettings":{"Networks":{"sind-mesh":{"IPAddress":"172.18.0.2"}}}}]`,
		`[{"Name":"sind-mesh","Id":"abc","IPAM":{"Config":[{"Subnet":"172.18.0.0/16","Gateway":"172.18.0.1"}]},"Labels":{"sind.realm":"sind"},"Containers":{"x":{"Name":"sind-dns"}}}]`,
		`{"ServerVersion":"29.0.0","CgroupVersion":"2","OperatingSystem":"Docker Desktop"}`,
		`{"Name":"sind-mesh","Labels":"sind.realm=sind"}` + "\n" + `{"Name":"x","Labels":""}`,
		`[]`, `null`, `[null]`, `{}`, `[{}]`, `{"Labels":null}`, "\x00", "[{",
		"2026-01-01T00:00:00.000000000Z line one\n2026-01-01T00:00:01Z line two\nno time\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, out string) {
		ctx := t.Context()
		var m mock.Executor
		m.OnCall = func(_ []string, _ string) mock.Result { return mock.Result{Stdout: out} }
		c := NewClient(&m)

		_, _ = c.ListContainers(ctx)
		_, _ = c.InspectContainers(ctx, "a", "b")
		_, _ = c.InspectContainer(ctx, "a")
		_, _ = c.ListNetworks(ctx)
		_, _ = c.InspectNetworks(ctx, "n", "m")
		_, _ = c.InspectNetwork(ctx, "n")
		_, _ = c.InspectNetworkMeta(ctx, "n")
		_, _ = c.NetworkContainers(ctx, "n")
		_, _, _ = c.NetworkLabels(ctx, "n")
		_, _ = c.ListVolumes(ctx)
		_, _, _ = c.VolumeLabels(ctx, "v")
		_, _ = c.VolumePlugins(ctx)
		_, _ = c.Info(ctx)
		_, _ = c.Endpoint(ctx)
		_, _, _ = c.InspectImage(ctx, "img")
		_, _, _ = c.ImageLabels(ctx, "img")
		_, _ = c.Logs(ctx, "a", 10)
		_ = logLines(out)
	})
}
