// SPDX-License-Identifier: LGPL-3.0-or-later

package mcpconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/mcpconfig"
)

// FuzzLoad checks the editor of an MCP configuration file, which belongs
// to the user's editor and can hold anything: no content makes Load or
// the methods of the file panic, and for a file that loads, the server
// that Set adds reads back from the file that Save writes, and is gone
// once Remove has removed it.
func FuzzLoad(f *testing.F) {
	for _, seed := range []string{
		"",
		"{}",
		newServers,
		"\ufeff{\r\n  \"mcpServers\": {}\r\n}\r\n",
		"// only a comment",
		"/* c */ {\"mcpServers\": {\"sind\": {\"command\": \"old\"}, \"other\": {\"url\": \"http://x\", \"type\": \"http\"},},}",
		`{"mcpServers": {"sind": 1}}`,
		`{"mcpServers": {"sind": {"args": "mcp", "env": {"A": 1}}}}`,
		`{"mcpServers": []}`,
		`{"mcpServers": {}, "mcpServers": {}}`,
		`{"servers": {"a": {}, "a": {}}}`,
		`{"mcpServers": {"": {"command": "a"}, "": {}}}`,
		`[]`, `null`, `{"a": }`, "{\"mcpServers\": null}", "{\"\\u006dcpServers\": {\"sind\": {}}}",
	} {
		f.Add([]byte(seed), false)
		f.Add([]byte(seed), true)
	}
	f.Fuzz(func(t *testing.T, data []byte, other bool) {
		key := "mcpServers"
		if other {
			key = "servers"
		}
		path := filepath.Join(t.TempDir(), "c.json")
		require.NoError(t, os.WriteFile(path, data, 0o644))
		file, err := mcpconfig.Load(path, key)
		if err != nil {
			return
		}
		for _, s := range file.Servers() {
			got, ok := file.Server(s.Name)
			assert.True(t, ok, "server %q is listed", s.Name)
			assert.Equal(t, s, got)
		}

		file.Set(sindEntry)
		_, err = file.Save(now)
		require.NoError(t, err)
		file, err = mcpconfig.Load(path, key)
		require.NoError(t, err, "the saved file does not load:\n%s", read(t, path))
		got, ok := file.Server(sindEntry.Name)
		assert.True(t, ok, "the saved file has no server sind:\n%s", read(t, path))
		assert.Equal(t, sindEntry, got)

		assert.True(t, file.Remove(sindEntry.Name))
		_, err = file.Save(now)
		require.NoError(t, err)
		file, err = mcpconfig.Load(path, key)
		require.NoError(t, err, "the saved file does not load:\n%s", read(t, path))
		_, ok = file.Server(sindEntry.Name)
		assert.False(t, ok, "the saved file still has the server sind:\n%s", read(t, path))
	})
}
