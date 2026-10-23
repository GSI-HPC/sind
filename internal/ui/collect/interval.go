// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"fmt"
	"sync"
	"time"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// The bounds of an Interval that the user sets.
const (
	MinInterval = 5 * time.Second
	MaxInterval = time.Hour
)

// CheckInterval returns an error for an interval out of its bounds.
func CheckInterval(d time.Duration) error {
	if d < MinInterval || d > MaxInterval {
		return fmt.Errorf("the interval must be between 5s and 1h, got %s", d)
	}
	return nil
}

// Interval is the interval of a collector's full read, which the user can
// change while the collector runs. It is safe for concurrent use.
type Interval struct {
	mu sync.Mutex
	d  time.Duration
	// changed holds a notice of a change that the collector has not
	// taken yet.
	changed chan struct{}
}

// NewInterval returns an Interval of d.
func NewInterval(d time.Duration) *Interval {
	return &Interval{d: d, changed: make(chan struct{}, 1)}
}

// Get returns the interval.
func (i *Interval) Get() time.Duration {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.d
}

// Set changes the interval; the collector takes it at once.
func (i *Interval) Set(d time.Duration) {
	i.mu.Lock()
	i.d = d
	i.mu.Unlock()
	select {
	case i.changed <- struct{}{}:
	default: // a notice is pending already
	}
}

// Viewers counts the live update streams of the browser: the pages open.
// A collector whose data only pages show reads it while one is open. It
// is safe for concurrent use.
type Viewers struct {
	mu sync.Mutex
	n  int
	// arrived hold, for each collector that asked (arrivals), a notice
	// that the first page opened, which it has not taken yet.
	arrived []chan struct{}
}

// NewViewers returns Viewers with no page open.
func NewViewers() *Viewers { return &Viewers{} }

// arrivals returns a channel of its own for a collector, which receives
// when the first page opens.
func (v *Viewers) arrivals() <-chan struct{} {
	c := make(chan struct{}, 1)
	v.mu.Lock()
	v.arrived = append(v.arrived, c)
	v.mu.Unlock()
	return c
}

// Watch counts a page in until the function it returns is called. It
// takes the topics of the page as ui.Env.AddWatcher hands them, and counts
// every page, as each shows the rail, whose counts read every collector.
func (v *Viewers) Watch([]store.Topic) (release func()) {
	v.mu.Lock()
	v.n++
	if v.n == 1 {
		for _, c := range v.arrived {
			select {
			case c <- struct{}{}:
			default:
			}
		}
	}
	v.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			v.mu.Lock()
			v.n--
			v.mu.Unlock()
		})
	}
}

// None reports whether no page is open.
func (v *Viewers) None() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.n == 0
}
