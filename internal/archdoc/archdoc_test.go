// SPDX-License-Identifier: LGPL-3.0-or-later

package archdoc

import (
	"errors"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// root is the repository root, from the directory of this package.
	root = "../.."
	// module is sind's module path.
	module = "github.com/GSI-HPC/sind/"
	// guide is the architecture guide, from root.
	guide = "docs/content/contributing/architecture-guide.md"
	// heading starts the section whose first code block is the
	// dependency flow.
	heading = "## Dependency flow\n"
	// width is where an import line of the flow wraps.
	width = 88
)

// imports returns, for each package of sind's module under cmd, tools, pkg
// and internal, the packages of the module that its non-test files import
// in the default build. A directory with a go.mod of its own is another
// module and left out.
func imports(t *testing.T) map[string][]string {
	t.Helper()
	deps := map[string][]string{}
	for _, top := range []string{"cmd", "tools", "pkg", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return err
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			pkg, err := build.Default.ImportDir(path, 0)
			if _, none := errors.AsType[*build.NoGoError](err); none {
				return nil
			}
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			var own []string
			for _, imp := range pkg.Imports {
				if after, ok := strings.CutPrefix(imp, module); ok {
					own = append(own, after)
				}
			}
			deps[filepath.ToSlash(rel)] = own
			return nil
		})
		require.NoError(t, err)
	}
	return deps
}

// rank orders the packages of the flow: the commands, then the public
// packages, then the internal ones.
func rank(pkg string) int {
	switch {
	case strings.HasPrefix(pkg, "cmd/"), strings.HasPrefix(pkg, "tools/"):
		return 0
	case strings.HasPrefix(pkg, "pkg/"):
		return 1
	default:
		return 2
	}
}

// flow renders the dependency flow: each package, and below it the
// packages it imports, the public ones first, wrapped at width.
func flow(deps map[string][]string) string {
	pkgs := make([]string, 0, len(deps))
	for p := range deps {
		pkgs = append(pkgs, p)
	}
	byRank := func(a, b string) int {
		if r := rank(a) - rank(b); r != 0 {
			return r
		}
		return strings.Compare(a, b)
	}
	slices.SortFunc(pkgs, byRank)
	var b strings.Builder
	for _, p := range pkgs {
		b.WriteString(p + "\n")
		imps := slices.Clone(deps[p])
		slices.SortFunc(imps, byRank)
		line := ""
		for _, imp := range imps {
			if line != "" && len(line)+1+len(imp) > width {
				b.WriteString(line + "\n")
				line = ""
			}
			if line == "" {
				line = "  → " + imp
			} else {
				line += " " + imp
			}
		}
		if line != "" {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// documented returns the first code block of the guide's dependency flow
// section.
func documented(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, guide))
	require.NoError(t, err)
	_, section, ok := strings.Cut(string(data), heading)
	require.True(t, ok, "no %q in %s", strings.TrimSpace(heading), guide)
	_, block, ok := strings.Cut(section, "```\n")
	require.True(t, ok, "no code block below %q", strings.TrimSpace(heading))
	block, _, ok = strings.Cut(block, "```")
	require.True(t, ok, "unterminated code block below %q", strings.TrimSpace(heading))
	return block
}

// TestDependencyFlow fails when the guide's dependency flow is not the
// imports of the packages; its message is the block to put there.
func TestDependencyFlow(t *testing.T) {
	want := flow(imports(t))
	assert.Equal(t, want, documented(t), "the dependency flow of %s; replace its code block with:\n%s", guide, want)
}

func TestFlow(t *testing.T) {
	deps := map[string][]string{
		"pkg/b":       {"internal/x", "pkg/a"},
		"cmd/c":       {"pkg/b"},
		"internal/x":  nil,
		"pkg/a":       {"internal/" + strings.Repeat("y", 50), "internal/" + strings.Repeat("z", 50)},
		"tools/t":     {"pkg/a"},
		"internal/ab": {"pkg/a"},
	}
	want := "cmd/c\n  → pkg/b\ntools/t\n  → pkg/a\npkg/a\n  → internal/" + strings.Repeat("y", 50) +
		"\n  → internal/" + strings.Repeat("z", 50) + "\npkg/b\n  → pkg/a internal/x\ninternal/ab\n  → pkg/a\ninternal/x\n"
	assert.Equal(t, want, flow(deps))
}
