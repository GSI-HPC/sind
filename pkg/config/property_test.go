// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"
)

// text draws a string that a config field may hold, whether or not sind
// takes it: words that YAML reads as another type, YAML syntax, and any
// other text.
func text() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom([]string{
			"", "dev", "worker", "512m", "2g", "1", "010", "1.10", "0x10", "1e3", "true", "no", "null", "~",
			"a: b", "- a", "# a", "{a}", "[a]", "*a", "&a", "!!str a", "|", ">", "'", `"`, "<<",
			" a ", "a\nb\n", "a\n\nb", "\ta", "a #b", "ä", "2026-11-20", "1:30",
		}),
		rapid.String(),
	)
}

// line draws a string of one line, for the fields that are not text of
// several lines in any valid config: the YAML encoder writes a string
// that starts with a tab and has a line break in a form that the decoder
// refuses, which the sections avoid (see yamlText).
func line() *rapid.Generator[string] {
	return text().Filter(func(s string) bool { return !strings.HasPrefix(s, "\t") || !strings.Contains(s, "\n") })
}

// texts draws a list of strings, nil when it has none, as the decoder
// leaves a list that the file does not have.
func texts(most int) *rapid.Generator[[]string] {
	return rapid.Custom(func(t *rapid.T) []string {
		list := rapid.SliceOfN(line(), 0, most).Draw(t, "list")
		if len(list) == 0 {
			return nil
		}
		return list
	})
}

// section draws a section in one of its forms, or none.
func section() *rapid.Generator[Section] {
	return rapid.Custom(func(t *rapid.T) Section {
		switch rapid.IntRange(0, 2).Draw(t, "form") {
		case 0:
			return Section{}
		case 1:
			return Section{Content: text().Draw(t, "content")}
		}
		return Section{Fragments: rapid.MapOfN(line(), text(), 1, 3).Draw(t, "fragments")}
	})
}

// anyCluster draws a cluster config with every field that a file can set,
// of any content that Parse reads, valid or not.
func anyCluster() *rapid.Generator[Cluster] {
	// Parse refuses a value that is not of the set of its type.
	roles := rapid.SampledFrom([]Role{RoleController, RoleDB, RoleAPI, RoleSubmitter, RoleWorker})
	node := rapid.Custom(func(t *rapid.T) Node {
		n := Node{
			Role:             roles.Draw(t, "role"),
			Count:            rapid.IntRange(-1, 3).Draw(t, "count"),
			Image:            line().Draw(t, "image"),
			CPUs:             rapid.IntRange(-1, 4).Draw(t, "cpus"),
			Memory:           line().Draw(t, "memory"),
			TmpSize:          line().Draw(t, "tmpSize"),
			BackupController: rapid.Bool().Draw(t, "backupController"),
			CapAdd:           texts(2).Draw(t, "capAdd"),
			CapDrop:          texts(2).Draw(t, "capDrop"),
			Devices:          texts(2).Draw(t, "devices"),
			SecurityOpt:      texts(2).Draw(t, "securityOpt"),
		}
		if rapid.Bool().Draw(t, "hasManaged") {
			n.Managed = new(rapid.Bool().Draw(t, "managed"))
		}
		return n
	})
	user := rapid.Custom(func(t *rapid.T) User {
		return User{
			Name:        line().Draw(t, "name"),
			UID:         rapid.SampledFrom([]int{0, 0, MinUID, MinUID + 1, MinUID + 2, 65533, 65534}).Draw(t, "uid"),
			Group:       line().Draw(t, "group"),
			Groups:      texts(2).Draw(t, "groups"),
			Accounts:    texts(2).Draw(t, "accounts"),
			Coordinator: texts(2).Draw(t, "coordinator"),
			AdminLevel:  rapid.SampledFrom([]AdminLevel{"", AdminOperator, AdminAdmin}).Draw(t, "adminLevel"),
		}
	})
	group := rapid.Custom(func(t *rapid.T) Group {
		return Group{
			Name: line().Draw(t, "name"),
			GID:  rapid.SampledFrom([]int{0, 0, MinUID, MinUID + 1, MinUID + 3, 65535}).Draw(t, "gid"),
		}
	})
	account := rapid.Custom(func(t *rapid.T) Account {
		a := Account{Name: line().Draw(t, "name"), Parent: line().Draw(t, "parent")}
		if limits := rapid.MapOfN(line(), line(), 0, 2).Draw(t, "limits"); len(limits) > 0 {
			a.Limits = limits
		}
		return a
	})
	return rapid.Custom(func(t *rapid.T) Cluster {
		c := Cluster{
			Kind:  "Cluster",
			Name:  line().Filter(func(s string) bool { return s != "" }).Draw(t, "name"),
			Realm: line().Draw(t, "realm"),
			Defaults: Defaults{
				Image:       line().Draw(t, "image"),
				CPUs:        rapid.IntRange(-1, 4).Draw(t, "cpus"),
				Memory:      line().Draw(t, "memory"),
				TmpSize:     line().Draw(t, "tmpSize"),
				CapAdd:      texts(2).Draw(t, "capAdd"),
				CapDrop:     texts(2).Draw(t, "capDrop"),
				Devices:     texts(2).Draw(t, "devices"),
				SecurityOpt: texts(2).Draw(t, "securityOpt"),
			},
			Storage: Storage{
				DataStorage: DataStorage{
					Type:      rapid.SampledFrom([]StorageType{"", StorageVolume, StorageHostPath}).Draw(t, "storageType"),
					HostPath:  line().Draw(t, "hostPath"),
					MountPath: line().Draw(t, "mountPath"),
				},
				CVMFS: rapid.Bool().Draw(t, "cvmfs"),
			},
			Slurm: Slurm{
				Main:      section().Draw(t, "main"),
				Cgroup:    section().Draw(t, "cgroup"),
				Gres:      section().Draw(t, "gres"),
				Topology:  section().Draw(t, "topology"),
				Plugstack: section().Draw(t, "plugstack"),
				Slurmdbd:  section().Draw(t, "slurmdbd"),
			},
			Identity: Identity{
				Mode: rapid.SampledFrom([]IdentityMode{
					"", IdentityLocal, IdentityNSSSlurm, IdentityClientIDs,
				}).Draw(t, "identity"),
				ControllerUsers: rapid.Bool().Draw(t, "controllerUsers"),
			},
			Monitoring: rapid.Bool().Draw(t, "monitoring"),
		}
		if users := rapid.SliceOfN(user, 0, 4).Draw(t, "users"); len(users) > 0 {
			c.Users = users
		}
		if groups := rapid.SliceOfN(group, 0, 3).Draw(t, "groups"); len(groups) > 0 {
			c.Groups = groups
		}
		if accounts := rapid.SliceOfN(account, 0, 3).Draw(t, "accounts"); len(accounts) > 0 {
			c.Accounts = accounts
		}
		if nodes := rapid.SliceOfN(node, 0, 4).Draw(t, "nodes"); len(nodes) > 0 {
			c.Nodes = nodes
		}
		return c
	})
}

// encode writes a config as YAML, as the web UI does for the file it
// passes to sind create cluster.
func encode(t *rapid.T, c Cluster) []byte {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	require.NoError(t, enc.Encode(c))
	require.NoError(t, enc.Close())
	return buf.Bytes()
}

// TestParse_ReadsBackWhatTheEncoderWrites checks the YAML forms of the
// config types against each other: Parse reads the file that the encoder
// writes for any config, and gives that config. The web UI depends on it:
// it builds a config from its form, and sind create cluster reads the file.
func TestParse_ReadsBackWhatTheEncoderWrites(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		want := anyCluster().Draw(t, "config")
		data := encode(t, want)

		got, err := Parse(data)
		require.NoError(t, err, "the file:\n%s", data)
		assert.Equal(t, want, *got, "the file:\n%s", data)
	})
}

// TestApplyDefaults_Properties checks ApplyDefaults on any config:
//   - a second call changes nothing, so that a config that was read from a
//     file, completed and read again stays the same
//   - every node has an image, CPUs, memory and a /tmp size, and each
//     entry of the defaults' lists, once
//   - every user and group has an ID, and an ID that sind assigned is one
//     that no other user or group has
func TestApplyDefaults_Properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := anyCluster().Draw(t, "config")
		explicit := make(map[int]bool)
		for _, u := range c.Users {
			explicit[u.UID] = true
		}
		for _, g := range c.Groups {
			explicit[g.GID] = true
		}
		assigned := func(id int) bool { return !explicit[id] }

		c.ApplyDefaults()
		once, err := json.Marshal(c)
		require.NoError(t, err)
		c.ApplyDefaults()
		twice, err := json.Marshal(c)
		require.NoError(t, err)
		assert.JSONEq(t, string(once), string(twice), "a second ApplyDefaults changes the config")

		require.NotEmpty(t, c.Nodes)
		for _, n := range c.Nodes {
			assert.NotEmpty(t, n.Image)
			assert.NotZero(t, n.CPUs)
			assert.NotEmpty(t, n.Memory)
			assert.NotEmpty(t, n.TmpSize)
			for _, cap := range c.Defaults.CapAdd {
				assert.Equal(t, 1, count(n.CapAdd, cap), "capAdd %q of the defaults in %q", cap, n.CapAdd)
			}
		}

		uses := make(map[int]int)
		for _, u := range c.Users {
			require.NotZero(t, u.UID, "user %q", u.Name)
			uses[u.UID]++
		}
		for _, g := range c.Groups {
			require.NotZero(t, g.GID, "group %q", g.Name)
			uses[g.GID]++
		}
		for id, n := range uses {
			if assigned(id) {
				assert.Equal(t, 1, n, "the assigned ID %d", id)
				assert.GreaterOrEqual(t, id, MinUID)
				assert.NotContains(t, ReservedIDs, id)
			}
		}
	})
}

// count returns how often list holds item.
func count(list []string, item string) int {
	return len(slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s != item }))
}
