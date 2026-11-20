// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FuzzMonitors checks the readers of the docker events and busctl monitor
// streams, which the node's own processes can write to: no stream makes a
// monitor panic or fail, and each event names its node.
func FuzzMonitors(f *testing.F) {
	for _, seed := range []string{
		"",
		`{"Type":"container","Action":"die","Actor":{"Attributes":{"name":"sind-dev-worker-0","exitCode":"137"}},"time":1234567891}`,
		`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"other"}},"time":-1}`,
		`{"type":"signal","member":"PropertiesChanged","path":"/org/freedesktop/systemd1/unit/munge_2eservice","payload":{"data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":"active"}},[]]}}`,
		`{"type":"signal","member":"PropertiesChanged","path":"/org/freedesktop/systemd1/unit/_2","payload":{"data":["org.freedesktop.systemd1.Unit",{"ActiveState":{"type":"s","data":3}}]}}`,
		`{"type":"signal","member":"PropertiesChanged","payload":{"data":[1]}}`,
		`{"type":"signal","member":"PropertiesChanged","payload":{"data":null}}`,
		"not json\n{}\n[]\nnull\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		run := func(run func(context.Context, io.Reader, chan<- Event) error) []Event {
			ch := make(chan Event, strings.Count(input, "\n")+1)
			require.NoError(t, run(t.Context(), strings.NewReader(input), ch))
			close(ch)
			var events []Event
			for ev := range ch {
				events = append(events, ev)
			}
			return events
		}
		for _, ev := range run(NewDockerMonitor("sind-dev-").Run) {
			assert.NotEmpty(t, ev.Node)
			assert.Equal(t, "sind-dev-"+ev.Node, string(ev.Container))
		}
		for _, ev := range run(NewSystemdMonitor("worker-0", "sind-dev-worker-0").Run) {
			assert.Equal(t, "worker-0", ev.Node)
			assert.Contains(t, []EventKind{EventUnitActive, EventUnitFailed}, ev.Kind)
		}
	})
}
