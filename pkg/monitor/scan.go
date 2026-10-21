// SPDX-License-Identifier: LGPL-3.0-or-later

package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"io"

	sindlog "github.com/GSI-HPC/sind/pkg/log"
)

// scanEvents reads JSON lines of T from r, of up to maxLine bytes, and
// sends the events parse makes of them to ch, skipping lines that do not
// parse or make none. It returns when r ends or fails, or when ctx is
// cancelled while it sends. A read blocks regardless of ctx: to stop it,
// the caller closes r. The caller closes ch after it returns.
func scanEvents[T any](ctx context.Context, r io.Reader, ch chan<- Event, source string, maxLine int,
	parse func(T) (Event, bool)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	for scanner.Scan() {
		var v T
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			sindlog.From(ctx).Log(ctx, sindlog.LevelTrace, source+": skipping unparseable line", "err", err)
			continue
		}
		ev, ok := parse(v)
		if !ok {
			continue
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return scanner.Err()
}
