// SPDX-License-Identifier: LGPL-3.0-or-later

package cmdexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
)

// outputLimitKey is the context key of WithOutputLimit.
type outputLimitKey struct{}

// WithOutputLimit returns ctx for a command whose standard output and
// standard error OSExecutor.Run keeps to about n bytes each, such as one
// whose output a server returns: the first and the last n/2 bytes, with a
// line between them that says how many bytes it left out. Without it, Run
// keeps all of it.
func WithOutputLimit(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, outputLimitKey{}, n)
}

// OutputLimit returns the limit of WithOutputLimit, zero without one.
func OutputLimit(ctx context.Context) int {
	n, _ := ctx.Value(outputLimitKey{}).(int)
	return n
}

// output is where Run collects a stream of a command.
type output interface {
	io.Writer
	String() string
}

// newOutput returns the buffer of a stream of a command run with ctx:
// one that keeps its head and tail under WithOutputLimit, else all of it.
func newOutput(ctx context.Context) output {
	if n := OutputLimit(ctx); n > 0 {
		return &headTail{half: max(n/2, 1)}
	}
	return &bytes.Buffer{}
}

// headTail keeps the first and the last half bytes written to it, and
// counts the bytes between them.
type headTail struct {
	half       int
	head, tail []byte
	total      int
}

// Write implements io.Writer. It never fails.
func (b *headTail) Write(p []byte) (int, error) {
	n := len(p)
	b.total += n
	if room := b.half - len(b.head); room > 0 {
		k := min(room, len(p))
		b.head = append(b.head, p[:k]...)
		p = p[k:]
	}
	b.tail = append(b.tail, p...)
	// Keep the tail to at most twice its size, so that a long stream
	// copies each byte about once.
	if len(b.tail) > 2*b.half {
		b.tail = append(b.tail[:0], b.tail[len(b.tail)-b.half:]...)
	}
	return n, nil
}

// String returns what b kept, with the count of the bytes it left out
// between head and tail.
func (b *headTail) String() string {
	tail := b.tail
	if len(tail) > b.half {
		tail = tail[len(tail)-b.half:]
	}
	if cut := b.total - len(b.head) - len(tail); cut > 0 {
		return fmt.Sprintf("%s\n[%d bytes truncated]\n%s", b.head, cut, tail)
	}
	return string(b.head) + string(tail)
}
