// SPDX-License-Identifier: LGPL-3.0-or-later

package activity

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/store"
)

// readFixture returns the lines of a file under testdata.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

// placeholder is how the golden log writes a span id: "#n" stands for the
// n-th span, as the 16 hexadecimal digits of n.
var placeholder = regexp.MustCompile(`"#(\d+)"`)

// spanOf returns the span id that "#n" of the golden log stands for.
func spanOf(n int) string { return fmt.Sprintf("%016x", n) }

// feed gives the reducer each line of data.
func feed(r *Reducer, data []byte) {
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = placeholder.ReplaceAllFunc(line, func(m []byte) []byte {
			n, _ := strconv.Atoi(string(m[2 : len(m)-1]))
			return []byte(`"` + spanOf(n) + `"`)
		})
		r.Line(line)
	}
}

// reduce returns the runs of the lines of the files under testdata.
func reduce(t *testing.T, names ...string) []store.Run {
	t.Helper()
	r := NewReducer(50)
	for _, n := range names {
		feed(r, readFixture(t, n))
	}
	return r.Runs()
}

// only returns the one run of the log.
func only(t *testing.T, name string) store.Run {
	t.Helper()
	runs := reduce(t, name)
	require.Len(t, runs, 1)
	return runs[0]
}

// find returns the first span below s named name, depth first.
func find(t *testing.T, s store.Span, name string) store.Span {
	t.Helper()
	var out store.Span
	found := false
	walk(s, func(c store.Span) {
		if c.Name == name && !found {
			out, found = c, true
		}
	})
	require.True(t, found, "no span %q", name)
	return out
}

// names returns the names of the spans.
func names(spans []store.Span) []string {
	var out []string
	for _, s := range spans {
		out = append(out, s.Name)
	}
	return out
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

// sid returns the span id, 16 hexadecimal digits, that the short name of a
// span stands for in a test.
func sid(name string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%016x", h.Sum64()|1<<63)
}

var shortSpan = regexp.MustCompile(`"(span|parent)":"([^"0-9a-f][^"]*|[^"]{1,15}|[^"]{17,})"`)

// line makes a line of the log from its keys, as the log writes them. The
// short names a test gives its spans become span ids.
func line(typ, run string, rest string) []byte {
	s := `{"v":1,"type":"` + typ + `","run":"` + run + `"`
	if rest != "" {
		s += "," + shortSpan.ReplaceAllStringFunc(rest, func(m string) string {
			k := shortSpan.FindStringSubmatch(m)
			return `"` + k[1] + `":"` + sid(k[2]) + `"`
		})
	}
	return []byte(s + "}")
}

// ev makes an event line of the run r at the time t.
func ev(typ, r, t, rest string) []byte {
	if rest != "" {
		rest = "," + rest
	}
	return line(typ, r, `"time":"`+t+`"`+rest)
}

func splitLines(b []byte) [][]byte {
	return bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
}

func bytesContains(b []byte, s string) bool { return bytes.Contains(b, []byte(s)) }

// goldenOfModule returns the golden log of the go-clikit module the
// tests build with, or skips the test when the module cache is not there.
func goldenOfModule(t *testing.T) []byte {
	t.Helper()
	const module = "github.com/GSI-HPC/go-clikit"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	version := ""
	for _, d := range info.Deps {
		if d.Path == module {
			version = d.Version
		}
	}
	cache := os.Getenv("GOMODCACHE")
	if cache == "" {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			home, _ := os.UserHomeDir()
			gopath = filepath.Join(home, "go")
		}
		cache = filepath.Join(gopath, "pkg", "mod")
	}
	var escaped strings.Builder
	for _, r := range module {
		if unicode.IsUpper(r) {
			escaped.WriteRune('!')
			r = unicode.ToLower(r)
		}
		escaped.WriteRune(r)
	}
	b, err := os.ReadFile(filepath.Join(cache, escaped.String()+"@"+version, "progress", "testdata", "log-v1.jsonl"))
	if err != nil {
		t.Skipf("the module's golden log is not in the module cache: %v", err)
	}
	return b
}

func appendTo(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.Write(b)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func logLines(run, title string) []byte {
	var b bytes.Buffer
	b.Write(line("trace", run, `"trace":"t","program":"sind"`))
	b.WriteString("\n")
	b.Write(ev("start", run, "2026-01-01T00:00:00Z", `"span":"r","kind":"command","name":"`+title+`"`))
	b.WriteString("\n")
	b.Write(ev("end", run, "2026-01-01T00:00:01Z", `"span":"r","kind":"command","name":"`+title+`","status":"ok"`))
	b.WriteString("\n")
	return b.Bytes()
}

// TestTailByteByByte writes each recorded log one byte at a time, polling
// after each, and holds that no line is lost, cut or read twice: the runs
// are those of the log read at once.
