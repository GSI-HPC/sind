// SPDX-License-Identifier: LGPL-3.0-or-later

package mcpconfig_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/mcpconfig"
)

var (
	sindEntry = mcpconfig.Server{Name: "sind", Command: "/usr/bin/sind", Args: []string{"mcp", "start"}}
	now       = time.Date(2026, 10, 19, 16, 30, 5, 0, time.Local)
)

// load writes content (unless nil) to c.json in a new in-memory file system
// and loads it.
func load(t *testing.T, content *string, key string) (afero.Fs, *mcpconfig.File) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	if content != nil {
		require.NoError(t, afero.WriteFile(fsys, "/cfg/c.json", []byte(*content), 0o644))
	}
	f, err := mcpconfig.Load(fsys, "/cfg/c.json", key)
	require.NoError(t, err)
	return fsys, f
}

func read(t *testing.T, fsys afero.Fs, path string) string {
	t.Helper()
	data, err := afero.ReadFile(fsys, path)
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
			fsys, f := load(t, tt.in, tt.key)
			f.Set(sindEntry)
			_, err := f.Save(now)
			require.NoError(t, err)
			assert.Equal(t, tt.want, read(t, fsys, "/cfg/c.json"))
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
			fsys, f := load(t, new(crlf(tt.in)), "mcpServers")
			tt.edit(f)
			_, err := f.Save(now)
			require.NoError(t, err)
			assert.Equal(t, crlf(tt.want), read(t, fsys, "/cfg/c.json"))
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
			fsys, f := load(t, &tt.in, "servers")
			assert.True(t, f.Remove("sind"))
			_, err := f.Save(now)
			require.NoError(t, err)
			assert.Equal(t, tt.want, read(t, fsys, "/cfg/c.json"))
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
	_, f := load(t, new(`{
  "mcpServers": {
    "z": {"type": "stdio", "command": "/bin/z", "args": ["a", "b"], "env": {"K": "secret"}},
    "web": {"type": "http", "url": "https://example.com/mcp", /* auth */ "headers": {}},
    "bad": "not an object",
    "odd": {"args": "not a list"},
  }
}`), "mcpServers")
	assert.Equal(t, []mcpconfig.Server{
		{Name: "z", Type: "stdio", Command: "/bin/z", Args: []string{"a", "b"}, Env: map[string]string{"K": "secret"}},
		{Name: "web", Type: "http", URL: "https://example.com/mcp"},
		{Name: "bad"},
		{Name: "odd"},
	}, f.Servers())
	assert.Equal(t, "/cfg/c.json", f.Path())

	_, f = load(t, nil, "mcpServers")
	assert.Empty(t, f.Servers())
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name, in, wantErr string
	}{
		{"syntax", `{"a": }`, "parsing /cfg/c.json: hujson: line 1, column 7: invalid character '}' at start of value"},
		{"not an object", `[]`, "parsing /cfg/c.json: not a JSON object"},
		{"servers not an object", `{"mcpServers": []}`, `parsing /cfg/c.json: "mcpServers" is not an object`},
		{"duplicate server", `{"mcpServers": {"sind": {}, "a": {}, "sind": {}}}`, `parsing /cfg/c.json: server "sind" is defined more than once in "mcpServers"`},
		{"duplicate servers", `{"mcpServers": {}, "x": 1, "mcpServers": {}}`, `parsing /cfg/c.json: "mcpServers" is defined more than once`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := afero.NewMemMapFs()
			require.NoError(t, afero.WriteFile(fsys, "/cfg/c.json", []byte(tt.in), 0o644))
			_, err := mcpconfig.Load(fsys, "/cfg/c.json", "mcpServers")
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestLoad_ReadErrors(t *testing.T) {
	fsys := afero.NewOsFs()
	dir := t.TempDir()
	_, err := mcpconfig.Load(fsys, dir, "mcpServers")
	require.ErrorContains(t, err, "reading "+dir+": ")

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, []byte("{}"), 0o644))
	_, err = mcpconfig.Load(fsys, filepath.Join(file, "c.json"), "mcpServers")
	require.ErrorContains(t, err, "reading "+filepath.Join(file, "c.json")+": ")

	statErr := errors.New("stat failed")
	failing := &failFs{Fs: afero.NewMemMapFs(), fail: map[string]error{"stat": statErr}}
	require.NoError(t, afero.WriteFile(failing.Fs, "/c.json", []byte("{}"), 0o644))
	_, err = mcpconfig.Load(failing, "/c.json", "mcpServers")
	require.ErrorIs(t, err, statErr)
}

func TestLoad_FollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "c.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("{}\n"), 0o640))
	require.NoError(t, os.Symlink("dotfiles/c.json", filepath.Join(dir, "rel.json")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "rel.json"), filepath.Join(dir, "abs.json")))

	f, err := mcpconfig.Load(afero.NewOsFs(), filepath.Join(dir, "abs.json"), "mcpServers")
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
	f, err = mcpconfig.Load(afero.NewOsFs(), filepath.Join(dir, "dangling.json"), "mcpServers")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "new.json"), f.Path())
}

func TestLoad_SymlinkErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Symlink("b", filepath.Join(dir, "a")))
	require.NoError(t, os.Symlink("a", filepath.Join(dir, "b")))
	_, err := mcpconfig.Load(afero.NewOsFs(), filepath.Join(dir, "a"), "mcpServers")
	require.ErrorContains(t, err, "too many levels of symbolic links")

	readlinkErr := errors.New("readlink failed")
	_, err = mcpconfig.Load(brokenLinkFs{Fs: afero.NewMemMapFs(), err: readlinkErr}, "/c.json", "mcpServers")
	require.ErrorIs(t, err, readlinkErr)
}

func TestSave_Backups(t *testing.T) {
	fsys, f := load(t, new("{}\n"), "mcpServers")

	// Nothing changed, nothing written.
	backup, err := f.Save(now)
	require.NoError(t, err)
	assert.Empty(t, backup)

	f.Set(sindEntry)
	backup, err = f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, "/cfg/c.json.20261019-163005.bak", backup)
	assert.Equal(t, "{}\n", read(t, fsys, backup))
	saved := read(t, fsys, "/cfg/c.json")

	// A second change in the same second keeps the first backup.
	f.Set(mcpconfig.Server{Name: "sind", Command: "/opt/sind", Args: []string{"mcp", "start"}})
	backup, err = f.Save(now)
	require.NoError(t, err)
	assert.Equal(t, "/cfg/c.json.20261019-163005-1.bak", backup)
	assert.Equal(t, saved, read(t, fsys, backup))
	assert.Equal(t, "{}\n", read(t, fsys, "/cfg/c.json.20261019-163005.bak"))

	info, err := fsys.Stat(backup)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o644), info.Mode().Perm())
}

func TestSave_NewFile(t *testing.T) {
	fsys, f := load(t, nil, "mcpServers")
	f.Set(sindEntry)
	backup, err := f.Save(now)
	require.NoError(t, err)
	assert.Empty(t, backup)
	info, err := fsys.Stat("/cfg/c.json")
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())

	// Saved once, the file is no longer new: an unchanged one is not
	// written again.
	backup, err = f.Save(now)
	require.NoError(t, err)
	assert.Empty(t, backup)
	files, err := afero.ReadDir(fsys, "/cfg")
	require.NoError(t, err)
	assert.Len(t, files, 1)
}

func TestSave_Errors(t *testing.T) {
	failure := errors.New("failed")
	tests := []struct {
		name    string
		exists  bool
		fail    string
		wantErr string
	}{
		{"mkdir", false, "mkdir", "writing /cfg/c.json: failed"},
		{"backup open", true, "open /cfg/c.json.20261019-163005.bak", "backing up /cfg/c.json: failed"},
		{"backup write", true, "write /cfg/c.json.20261019-163005.bak", "backing up /cfg/c.json: failed"},
		{"backup close", true, "close /cfg/c.json.20261019-163005.bak", "backing up /cfg/c.json: failed"},
		{"rename", true, "rename", "writing /cfg/c.json: failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := &failFs{Fs: afero.NewMemMapFs()}
			if tt.exists {
				require.NoError(t, afero.WriteFile(fsys.Fs, "/cfg/c.json", []byte("{}\n"), 0o644))
			}
			f, err := mcpconfig.Load(fsys, "/cfg/c.json", "mcpServers")
			require.NoError(t, err)
			f.Set(sindEntry)
			fsys.fail = map[string]error{tt.fail: failure}

			_, err = f.Save(now)
			require.EqualError(t, err, tt.wantErr)
			if tt.exists {
				assert.Equal(t, "{}\n", read(t, fsys.Fs, "/cfg/c.json"), "the file is unchanged")
			}
			files, _ := afero.ReadDir(fsys.Fs, "/cfg")
			for _, file := range files {
				assert.False(t, strings.HasPrefix(file.Name(), ".c.json-"), "temporary file %s left behind", file.Name())
			}
		})
	}
}

// failFs fails the operations named in fail: "stat", "mkdir", "rename",
// or "open", "write" or "close" followed by a space and a prefix of the
// file's name.
type failFs struct {
	afero.Fs
	fail map[string]error
}

func (f *failFs) err(op, name string) error {
	if err, ok := f.fail[op]; ok {
		return err
	}
	for key, err := range f.fail {
		if prefix, ok := strings.CutPrefix(key, op+" "); ok && strings.HasPrefix(name, prefix) {
			return err
		}
	}
	return nil
}

func (f *failFs) Stat(name string) (os.FileInfo, error) {
	if err := f.err("stat", name); err != nil {
		return nil, err
	}
	return f.Fs.Stat(name)
}

func (f *failFs) MkdirAll(path string, perm os.FileMode) error {
	if err := f.err("mkdir", path); err != nil {
		return err
	}
	return f.Fs.MkdirAll(path, perm)
}

func (f *failFs) Rename(oldname, newname string) error {
	if err := f.err("rename", newname); err != nil {
		return err
	}
	return f.Fs.Rename(oldname, newname)
}

func (f *failFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if err := f.err("open", name); err != nil {
		return nil, err
	}
	file, err := f.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &failFile{File: file, fs: f}, nil
}

type failFile struct {
	afero.File
	fs *failFs
}

func (f *failFile) Write(p []byte) (int, error) {
	if err := f.fs.err("write", f.Name()); err != nil {
		return 0, err
	}
	return f.File.Write(p)
}

func (f *failFile) Close() error {
	if err := f.fs.err("close", f.Name()); err != nil {
		_ = f.File.Close()
		return err
	}
	return f.File.Close()
}

// brokenLinkFs reports every file as a symbolic link it cannot read.
type brokenLinkFs struct {
	afero.Fs
	err error
}

func (b brokenLinkFs) LstatIfPossible(string) (os.FileInfo, bool, error) {
	return linkInfo{}, true, nil
}

func (b brokenLinkFs) ReadlinkIfPossible(string) (string, error) {
	return "", b.err
}

type linkInfo struct{ os.FileInfo }

func (linkInfo) Mode() fs.FileMode { return fs.ModeSymlink }
