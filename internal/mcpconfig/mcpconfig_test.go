// SPDX-License-Identifier: LGPL-3.0-or-later

package mcpconfig_test

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/go-clikit/fsutil"

	"github.com/GSI-HPC/sind/internal/mcpconfig"
)

var (
	sindEntry = mcpconfig.Server{Name: "sind", Command: "/usr/bin/sind", Args: []string{"mcp", "start"}}
	now       = time.Date(2026, 10, 19, 16, 30, 5, 0, time.Local)
)

// load writes content (unless nil) to cfg/c.json in a new directory and
// loads it. It returns the path of the file.
func load(t *testing.T, content *string, key string) (string, *mcpconfig.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg", "c.json")
	if content != nil {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(*content), 0o644))
	}
	f, err := mcpconfig.Load(path, key)
	require.NoError(t, err)
	return path, f
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// newServers is the configuration Set makes of an empty one.
const newServers = `{
  "mcpServers": {
    "sind": {
      "command": "/usr/bin/sind",
      "args": [
        "mcp",
        "start"
      ]
    }
  }
}
`

func TestSet(t *testing.T) {
	tests := []struct {
		name string
		in   *string
		key  string
		want string
	}{
		{name: "no file", key: "mcpServers", want: newServers},
		{name: "whitespace only", in: new(" \n"), key: "mcpServers", want: newServers},
		{
			name: "keeps other settings and servers",
			in: new(`{
	"globalShortcut": "Ctrl+Space",
	"mcpServers": {
		"other": {"command": "x", "cwd": "/w", "disabled": true, "autoApprove": ["t"]}
	}
}
`),
			key: "mcpServers",
			want: `{
	"globalShortcut": "Ctrl+Space",
	"mcpServers": {
		"other": {"command": "x", "cwd": "/w", "disabled": true, "autoApprove": ["t"]},
		"sind": {
			"command": "/usr/bin/sind",
			"args": [
				"mcp",
				"start"
			]
		}
	}
}
`,
		},
		{
			name: "jsonc with comments and trailing commas",
			in: new(`{
  // inputs
  "inputs": [],
  "servers": {
    "other": {"command": "x"}, // keep
  },
}
`),
			key: "servers",
			want: `{
  // inputs
  "inputs": [],
  "servers": {
    "other": {"command": "x"}, // keep
    "sind": {
      "command": "/usr/bin/sind",
      "args": [
        "mcp",
        "start"
      ]
    },
  },
}
`,
		},
		{
			name: "comment after the last entry",
			in: new(`{
    "servers": {
        "other": {
            "command": "x"
        } // keep
    }
}`),
			key: "servers",
			want: `{
    "servers": {
        "other": {
            "command": "x"
        }, // keep
        "sind": {
            "command": "/usr/bin/sind",
            "args": [
                "mcp",
                "start"
            ]
        }
    }
}`,
		},
		{
			name: "no servers yet",
			in:   new("{\n  \"preferences\": {\"x\": 1}\n}\n"),
			key:  "mcpServers",
			want: `{
  "preferences": {"x": 1},
  "mcpServers": {
    "sind": {
      "command": "/usr/bin/sind",
      "args": [
        "mcp",
        "start"
      ]
    }
  }
}
`,
		},
		{
			name: "empty servers",
			in:   new("{\n  \"servers\": {}\n}\n"),
			key:  "servers",
			want: `{
  "servers": {
    "sind": {
      "command": "/usr/bin/sind",
      "args": [
        "mcp",
        "start"
      ]
    }
  }
}
`,
		},
		{
			name: "one-line file",
			in:   new(`{"a":1,"mcpServers":{"b":{"command":"y"}}}`),
			key:  "mcpServers",
			want: `{"a":1,"mcpServers":{"b":{"command":"y"},"sind":{"command":"/usr/bin/sind","args":["mcp","start"]}}}`,
		},
		{
			name: "one-line file without servers",
			in:   new(`{"a": 1}`),
			key:  "mcpServers",
			want: `{"a": 1,"mcpServers": {"sind": {"command":"/usr/bin/sind","args":["mcp","start"]}}}`,
		},
		{
			name: "replaces the entry and keeps its comments",
			in: new(`{
  "mcpServers": {
    // ours
    "sind": {"command": "old", "args": ["x"]}, // was old
    "z": {}
  }
}
`),
			key: "mcpServers",
			want: `{
  "mcpServers": {
    // ours
    "sind": {
      "command": "/usr/bin/sind",
      "args": [
        "mcp",
        "start"
      ]
    }, // was old
    "z": {}
  }
}
`,
		},
		{
			name: "replaces the entry and keeps the members it does not set",
			in: new(`{
  "mcpServers": {
    "sind": {"disabled": true, "command": "old", "autoApprove": ["x"], "env": {"A": "1"}, /* where */ "cwd": "/w", "url": "u", "command": "again",}
  }
}
`),
			key: "mcpServers",
			want: `{
  "mcpServers": {
    "sind": {
      "disabled": true,
      "command": "/usr/bin/sind",
      "autoApprove": [
        "x"
      ],
      "cwd": "/w",
      "args": [
        "mcp",
        "start"
      ]
    }
  }
}
`,
		},
		{
			name: "replaces a one-line entry",
			in:   new(`{"mcpServers":{"sind":{"type":"http","disabled":false}}}`),
			key:  "mcpServers",
			want: `{"mcpServers":{"sind":{"disabled":false,"command":"/usr/bin/sind","args":["mcp","start"]}}}`,
		},
		{
			name: "replaces an entry that is no object",
			in:   new(`{"mcpServers":{"sind":"x"}}`),
			key:  "mcpServers",
			want: `{"mcpServers":{"sind":{"command":"/usr/bin/sind","args":["mcp","start"]}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, f := load(t, tt.in, tt.key)
			f.Set(sindEntry)
			_, err := f.Save(now)
			require.NoError(t, err)
			assert.Equal(t, tt.want, read(t, path))
		})
	}
}

// TestCRLF checks that what Set and Remove change in a file with CRLF line
// breaks has them too.
func TestCRLF(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	for name, tt := range map[string]struct {
		in, want string
		edit     func(*mcpconfig.File)
	}{
		"add": {
			in:   "{\n  \"mcpServers\": {\n    \"a\": {}\n  }\n}\n",
			want: "{\n  \"mcpServers\": {\n    \"a\": {},\n    \"sind\": {\n      \"command\": \"/usr/bin/sind\",\n      \"args\": [\n        \"mcp\",\n        \"start\"\n      ]\n    }\n  }\n}\n",
			edit: func(f *mcpconfig.File) { f.Set(sindEntry) },
		},
		"add the servers": {
			in:   "{\n  \"a\": 1\n}\n",
			want: "{\n  \"a\": 1,\n  \"mcpServers\": {\n    \"sind\": {\n      \"command\": \"/usr/bin/sind\",\n      \"args\": [\n        \"mcp\",\n        \"start\"\n      ]\n    }\n  }\n}\n",
			edit: func(f *mcpconfig.File) { f.Set(sindEntry) },
		},
		"replace": {
			in:   "{\n  \"mcpServers\": {\n    \"sind\": {\"disabled\": true}\n  }\n}\n",
			want: "{\n  \"mcpServers\": {\n    \"sind\": {\n      \"disabled\": true,\n      \"command\": \"/usr/bin/sind\",\n      \"args\": [\n        \"mcp\",\n        \"start\"\n      ]\n    }\n  }\n}\n",
			edit: func(f *mcpconfig.File) { f.Set(sindEntry) },
		},
		"remove": {
			in:   "{\n  \"mcpServers\": {\n    \"a\": {},\n    \"sind\": {}\n  }\n}\n",
			want: "{\n  \"mcpServers\": {\n    \"a\": {}\n  }\n}\n",
			edit: func(f *mcpconfig.File) { f.Remove("sind") },
		},
	} {
		t.Run(name, func(t *testing.T) {
			path, f := load(t, new(crlf(tt.in)), "mcpServers")
			tt.edit(f)
			_, err := f.Save(now)
			require.NoError(t, err)
			assert.Equal(t, crlf(tt.want), read(t, path))
		})
	}
}

func TestServer(t *testing.T) {
	_, f := load(t, new(`{"servers": {"a": {"command": "x"}, "sind": {"command": "/bin/sind", "env": {"K": "v"}}}}`), "servers")
	s, ok := f.Server("sind")
	assert.True(t, ok)
	assert.Equal(t, mcpconfig.Server{Name: "sind", Command: "/bin/sind", Env: map[string]string{"K": "v"}}, s)
	_, ok = f.Server("none")
	assert.False(t, ok)
}

func TestRemove(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "last entry",
			in: `{
  "servers": {
    "other": {"command": "x"}, // keep
    "sind": {"command": "/usr/bin/sind"}
  },
}
`,
			want: `{
  "servers": {
    "other": {"command": "x"} // keep
  },
}
`,
		},
		{
			name: "first entry with its comment",
			in: `{
  "servers": {
    "sind": {"command": "/usr/bin/sind"}, // ours
    "z": {}
  }
}
`,
			want: `{
  "servers": {
    "z": {}
  }
}
`,
		},
		{
			name: "only entry",
			in:   "{\n\t\"servers\": {\n\t\t\"sind\": {}\n\t}\n}\n",
			want: "{\n\t\"servers\": {}\n}\n",
		},
		{
			name: "only entry beside a comment",
			in:   "{\n  \"servers\": {\n    \"sind\": {}\n    // none yet\n  }\n}\n",
			want: "{\n  \"servers\": {\n    // none yet\n  }\n}\n",
		},
		{
			name: "one-line file",
			in:   `{"servers":{"a":{},"sind":{}}}`,
			want: `{"servers":{"a":{}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, f := load(t, &tt.in, "servers")
			assert.True(t, f.Remove("sind"))
			_, err := f.Save(now)
			require.NoError(t, err)
			assert.Equal(t, tt.want, read(t, path))
		})
	}
}

func TestRemove_Missing(t *testing.T) {
	_, f := load(t, new(`{"servers": {"a": {}}}`), "servers")
	assert.False(t, f.Remove("sind"))
	_, f = load(t, nil, "servers")
	assert.False(t, f.Remove("sind"))
}

func TestServers(t *testing.T) {
	path, f := load(t, new(`{
  "mcpServers": {
    "z": {"type": "stdio", "command": "/bin/z", "args": ["a", "b"], "env": {"K": "secret"}},
    "web": {"type": "http", "url": "https://example.com/mcp", /* auth */ "headers": {}},
    "bad": "not an object",
    "odd": {"args": "not a list"},
    "mixed": {"type": 1, "command": "/bin/m", "args": ["a", 2], "env": {"SIND_REALM": "ci", "PORT": 8080, "ON": true, "L": [1, 2]}},
    "envless": {"command": "/bin/e", "env": ["K=V"]},
  }
}`), "mcpServers")
	assert.Equal(t, []mcpconfig.Server{
		{Name: "z", Type: "stdio", Command: "/bin/z", Args: []string{"a", "b"}, Env: map[string]string{"K": "secret"}},
		{Name: "web", Type: "http", URL: "https://example.com/mcp"},
		{Name: "bad"},
		{Name: "odd"},
		// A field of another type is left out alone, and a variable that
		// is no string has its JSON text.
		{Name: "mixed", Command: "/bin/m", Env: map[string]string{"SIND_REALM": "ci", "PORT": "8080", "ON": "true", "L": "[1,2]"}},
		{Name: "envless", Command: "/bin/e"},
	}, f.Servers())
	assert.Equal(t, path, f.Path())

	_, f = load(t, nil, "mcpServers")
	assert.Empty(t, f.Servers())
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name, in, wantErr string
	}{
		{"syntax", `{"a": }`, "hujson: line 1, column 7: invalid character '}' at start of value"},
		{"not an object", `[]`, "not a JSON object"},
		{"servers not an object", `{"mcpServers": []}`, `"mcpServers" is not an object`},
		{"duplicate server", `{"mcpServers": {"sind": {}, "a": {}, "sind": {}}}`, `server "sind" is defined more than once in "mcpServers"`},
		{"duplicate servers", `{"mcpServers": {}, "x": 1, "mcpServers": {}}`, `"mcpServers" is defined more than once`},
		// Another name defined twice before does not hide it.
		{"duplicate servers after another duplicate", `{"a": 1, "a": 2, "mcpServers": {"old": {}}, "mcpServers": {"new": {}}}`,
			`"mcpServers" is defined more than once`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			require.NoError(t, os.WriteFile(path, []byte(tt.in), 0o644))
			_, err := mcpconfig.Load(path, "mcpServers")
			require.EqualError(t, err, "parsing "+path+": "+tt.wantErr)
		})
	}
}

// TestLoad_ByteOrderMark checks that a file that starts with a byte order
// mark loads, and keeps it.
func TestLoad_ByteOrderMark(t *testing.T) {
	path, f := load(t, new("\ufeff{\n  \"mcpServers\": {}\n}\n"), "mcpServers")
	backup, err := f.Save(now)
	require.NoError(t, err)
	assert.Empty(t, backup, "an unchanged file is not written")

	f.Set(sindEntry)
	_, err = f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, "\ufeff"+newServers, read(t, path))

	// One with only whitespace after the mark is empty.
	path, f = load(t, new("\ufeff\n"), "mcpServers")
	f.Set(sindEntry)
	_, err = f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, "\ufeff"+newServers, read(t, path))
}

// TestLoad_OnlyComments checks that a file that holds only comments loads
// as an empty configuration, and keeps its comments before the object.
func TestLoad_OnlyComments(t *testing.T) {
	for in, want := range map[string]string{
		"// nothing yet\n":        "// nothing yet\n" + newServers,
		"/* a */\n// b":           "/* a */\n// b\n" + newServers,
		"\ufeff/* settings */\n":  "\ufeff/* settings */\n" + newServers,
		"// one\r\n/* two */\r\n": "// one\r\n/* two */\r\n" + strings.ReplaceAll(newServers, "\n", "\r\n"),
	} {
		t.Run(in, func(t *testing.T) {
			path, f := load(t, &in, "mcpServers")
			assert.Empty(t, f.Servers())
			f.Set(sindEntry)
			_, err := f.Save(now)
			require.NoError(t, err)
			assert.Equal(t, want, read(t, path))
		})
	}

	// A comment that does not end is still an error.
	path := filepath.Join(t.TempDir(), "c.json")
	require.NoError(t, os.WriteFile(path, []byte("// a\n/* b"), 0o644))
	_, err := mcpconfig.Load(path, "mcpServers")
	require.ErrorContains(t, err, "parsing "+path+": ")
	require.NoError(t, os.WriteFile(path, []byte("// a\n1"), 0o644))
	_, err = mcpconfig.Load(path, "mcpServers")
	require.EqualError(t, err, "parsing "+path+": not a JSON object")
}

func TestLoad_ReadErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := mcpconfig.Load(dir, "mcpServers")
	require.ErrorContains(t, err, "reading "+dir+": ")

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, []byte("{}"), 0o644))
	_, err = mcpconfig.Load(filepath.Join(file, "c.json"), "mcpServers")
	require.ErrorContains(t, err, "reading "+filepath.Join(file, "c.json")+": ")
}

func TestLoad_FollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "c.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("{}\n"), 0o640))
	require.NoError(t, os.Symlink("dotfiles/c.json", filepath.Join(dir, "rel.json")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "rel.json"), filepath.Join(dir, "abs.json")))

	f, err := mcpconfig.Load(filepath.Join(dir, "abs.json"), "mcpServers")
	require.NoError(t, err)
	assert.Equal(t, target, f.Path())
	f.Set(sindEntry)
	backup, err := f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, target+".20261019-163005.bak", backup)

	info, err := os.Lstat(filepath.Join(dir, "rel.json"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&fs.ModeSymlink, "the link stays a link")
	data, err := os.ReadFile(filepath.Join(dir, "abs.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"sind"`)
	info, err = os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o640), info.Mode().Perm(), "the file keeps its permissions")

	// A link to a file that does not exist yet creates the file.
	require.NoError(t, os.Symlink("new.json", filepath.Join(dir, "dangling.json")))
	f, err = mcpconfig.Load(filepath.Join(dir, "dangling.json"), "mcpServers")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "new.json"), f.Path())
}

// TestLoad_LinkBehindALinkedDirectory checks that a relative link is read
// against the directory it is really in, as the system reads it: behind a
// linked directory, ../x is beside the directory the link leads to.
func TestLoad_LinkBehindALinkedDirectory(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	user := filepath.Join(home, "dotfiles", "config", "Code", "User")
	require.NoError(t, os.MkdirAll(user, 0o755))
	require.NoError(t, os.Symlink("dotfiles/config", filepath.Join(home, ".config")))
	require.NoError(t, os.Symlink("../../../shared/mcp.json", filepath.Join(user, "mcp.json")))
	target := filepath.Join(home, "dotfiles", "shared", "mcp.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte(`{"servers": {"mine": {"command": "x"}}}`), 0o600))

	f, err := mcpconfig.Load(filepath.Join(home, ".config", "Code", "User", "mcp.json"), "servers")
	require.NoError(t, err)
	assert.Equal(t, target, f.Path())
	assert.Equal(t, []mcpconfig.Server{{Name: "mine", Command: "x"}}, f.Servers())
	f.Set(sindEntry)
	backup, err := f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, target+".20261019-163005.bak", backup)
	assert.Contains(t, read(t, target), `"mine"`)
	assert.Contains(t, read(t, target), `"sind"`)
	assert.NoDirExists(t, filepath.Join(home, "shared"), "nothing is written beside the link's name")
}

// TestLoad_UntrustedLink checks that Load refuses a symbolic link that
// neither this user nor root owns, whose owner would choose the file Save
// writes, before Save could make a directory or a backup where it leads.
func TestLoad_UntrustedLink(t *testing.T) {
	// untrust makes the link another user's: as root, by giving it away,
	// else by taking Load for another user.
	untrust := func(t *testing.T, link string) {
		t.Helper()
		if os.Geteuid() == 0 {
			require.NoError(t, os.Lchown(link, 12345, 12345))
			return
		}
		t.Cleanup(mcpconfig.Geteuid(os.Geteuid() + 1))
	}

	t.Run("to a file", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		require.NoError(t, os.WriteFile(target, []byte("{}\n"), 0o600))
		link := filepath.Join(dir, "c.json")
		require.NoError(t, os.Symlink("target.json", link))
		untrust(t, link)

		_, err := mcpconfig.Load(link, "mcpServers")
		require.ErrorIs(t, err, fsutil.ErrUntrusted)
		require.ErrorContains(t, err, "reading "+link+": the link is owned by uid ")
		assert.Equal(t, "{}\n", read(t, target), "the file is unchanged")
		files, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, files, 2, "no backup is made")
	})

	t.Run("into a missing directory", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "c.json")
		require.NoError(t, os.Symlink(filepath.Join("target-dir", "sub", "mcp.json"), link))
		untrust(t, link)

		_, err := mcpconfig.Load(link, "mcpServers")
		require.ErrorIs(t, err, fsutil.ErrUntrusted)
		assert.NoDirExists(t, filepath.Join(dir, "target-dir"), "no directory is made")
	})

	t.Run("of root", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("only root can make a link of root")
		}
		t.Cleanup(mcpconfig.Geteuid(12345))
		dir := t.TempDir()
		link := filepath.Join(dir, "c.json")
		require.NoError(t, os.Symlink("target.json", link))
		f, err := mcpconfig.Load(link, "mcpServers")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, "target.json"), f.Path())
	})
}

func TestLoad_SymlinkErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Symlink("b", filepath.Join(dir, "a")))
	require.NoError(t, os.Symlink("a", filepath.Join(dir, "b")))
	_, err := mcpconfig.Load(filepath.Join(dir, "a"), "mcpServers")
	require.ErrorContains(t, err, "too many levels of symbolic links")
}

func TestSave_Backups(t *testing.T) {
	path, f := load(t, new("{}\n"), "mcpServers")

	// Nothing changed, nothing written.
	backup, err := f.Save(now)
	require.NoError(t, err)
	assert.Empty(t, backup)

	f.Set(sindEntry)
	backup, err = f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, path+".20261019-163005.bak", backup)
	assert.Equal(t, "{}\n", read(t, backup))
	saved := read(t, path)

	// A second change in the same second keeps the first backup.
	f.Set(mcpconfig.Server{Name: "sind", Command: "/opt/sind", Args: []string{"mcp", "start"}})
	backup, err = f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, path+".20261019-163005-1.bak", backup)
	assert.Equal(t, saved, read(t, backup))
	assert.Equal(t, "{}\n", read(t, path+".20261019-163005.bak"))

	info, err := os.Stat(backup)
	require.NoError(t, err)
	assert.Zero(t, info.Mode().Perm()&^0o644, "a backup is no wider than the file was")
}

func TestSave_NewFile(t *testing.T) {
	path, f := load(t, nil, "mcpServers")
	f.Set(sindEntry)
	backup, err := f.Save(now)
	require.NoError(t, err)
	assert.Empty(t, backup)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o755), dirInfo.Mode().Perm()&^0o022, "the directory of an editor is readable")

	// Saved once, the file is no longer new: an unchanged one is not
	// written again.
	backup, err = f.Save(now)
	require.NoError(t, err)
	assert.Empty(t, backup)
	files, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, files, 1)
}

// failingBackup is a backup file whose write or close fails.
type failingBackup struct {
	io.WriteCloser
	write, close error
}

func (b failingBackup) Write(p []byte) (int, error) {
	if b.write != nil {
		return 0, b.write
	}
	return b.WriteCloser.Write(p)
}

func (b failingBackup) Close() error {
	err := b.WriteCloser.Close()
	if b.close != nil {
		return b.close
	}
	return err
}

// createBackup creates the file of a backup as Save does.
func createBackup(name string, mode fs.FileMode) (io.WriteCloser, error) {
	out, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func TestSave_Errors(t *testing.T) {
	failure := errors.New("failed")
	backupFails := func(b failingBackup) func(string, fs.FileMode) (io.WriteCloser, error) {
		return func(name string, mode fs.FileMode) (io.WriteCloser, error) {
			out, err := createBackup(name, mode)
			b.WriteCloser = out
			return b, err
		}
	}
	tests := []struct {
		name    string
		exists  bool
		backup  func(string, fs.FileMode) (io.WriteCloser, error)
		setup   func(t *testing.T, path string)
		wantErr string
	}{
		{name: "mkdir", setup: func(t *testing.T, path string) {
			// A file where the directory goes.
			require.NoError(t, os.WriteFile(filepath.Dir(path), nil, 0o600))
		}, wantErr: "writing %s: "},
		{name: "backup open", exists: true,
			backup:  func(string, fs.FileMode) (io.WriteCloser, error) { return nil, failure },
			wantErr: "backing up %s: failed"},
		{name: "backup write", exists: true, backup: backupFails(failingBackup{write: failure}), wantErr: "backing up %s: failed"},
		{name: "backup close", exists: true, backup: backupFails(failingBackup{close: failure}), wantErr: "backing up %s: failed"},
		{name: "write", exists: true, setup: func(t *testing.T, path string) {
			// A directory that is not empty where the file goes.
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.MkdirAll(filepath.Join(path, "x"), 0o755))
		}, wantErr: "writing %s: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var content *string
			if tt.exists {
				content = new("{}\n")
			}
			path, f := load(t, content, "mcpServers")
			f.Set(sindEntry)
			if tt.backup != nil {
				t.Cleanup(mcpconfig.OpenBackup(tt.backup))
			}
			if tt.setup != nil {
				tt.setup(t, path)
			}

			backup, err := f.Save(now)
			require.ErrorContains(t, err, fmt.Sprintf(tt.wantErr, path))
			assert.Empty(t, backup)
			backups, _ := filepath.Glob(path + ".*.bak")
			assert.Empty(t, backups, "a failed save leaves no backup")
			if tt.backup != nil {
				assert.Equal(t, "{}\n", read(t, path), "the file is unchanged")
			}
		})
	}
}
