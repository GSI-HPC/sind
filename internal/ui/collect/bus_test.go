// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBusDeliversDistinctEventsToEverySubscriber(t *testing.T) {
	b := NewBus()
	s1, s2 := b.Subscribe(), b.Subscribe()
	defer s1.Close()
	ev := Event{Kind: KindContainer, Realm: "sind", Cluster: "dev"}

	b.Publish(ev)
	b.Publish(ev)
	b.Publish(Event{Kind: KindNetwork, Realm: "sind"})

	for _, s := range []*Sub{s1, s2} {
		select {
		case <-s.C():
		case <-time.After(time.Second):
			require.FailNow(t, "no wake-up")
		}
		assert.ElementsMatch(t, []Event{ev, {Kind: KindNetwork, Realm: "sind"}}, s.Drain())
		assert.Empty(t, s.Drain())
	}

	s2.Close()
	b.Publish(ev)
	assert.Empty(t, s2.Drain(), "a closed subscription receives nothing")
	assert.Len(t, s1.Drain(), 1)
}

func TestBusPublishNeverBlocks(t *testing.T) {
	b := NewBus()
	s := b.Subscribe()
	defer s.Close()
	for range 1000 {
		b.Publish(Event{Realm: "a", Kind: KindContainer})
	}
	assert.Len(t, s.Drain(), 1)
}
