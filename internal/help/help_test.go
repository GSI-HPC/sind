// SPDX-License-Identifier: LGPL-3.0-or-later

package help

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wave2Actions are the action ids that the actions part of sind ui owns.
var wave2Actions = []string{
	"create-cluster", "create-worker", "delete-cluster", "delete-clusters", "delete-worker",
	"power", "restart-service", "connect", "disconnect", "delete-realm",
}

// wave2Paths are the pages of sind ui, as paths and the page that serves each.
var wave2Paths = map[string]string{
	"/":                        "overview",
	"/clusters":                "clusters",
	"/clusters/new":            "new-cluster",
	"/clusters/sind/dev":       "cluster",
	"/nodes":                   "nodes",
	"/nodes/sind/dev/worker-1": "node",
	"/mesh":                    "mesh",
	"/monitoring":              "monitoring",
	"/topology":                "topology",
	"/doctor":                  "doctor",
	"/activity":                "activity",
	"/runs/0123456789abcdef":   "run",
	"/tasks/t1":                "task",
	"/help":                    "help",
}

func TestEmbeddedTextsAreComplete(t *testing.T) {
	h := Default()
	assert.Same(t, h, Default())
	for _, id := range wave2Actions {
		a, ok := h.Action(id)
		require.True(t, ok, "action %s has no entry", id)
		assert.NotEmpty(t, a.Words, id)
	}
	assert.Len(t, h.Actions(), len(wave2Actions), "an action without a button")
	for path, id := range wave2Paths {
		p, ok := h.PageFor(path)
		require.True(t, ok, "path %s has no page entry", path)
		assert.Equal(t, id, p.ID, path)
	}
	assert.Len(t, h.Pages(), len(wave2Paths))
	// Every service and role that the pages of sind ui show is a word.
	for _, id := range []string{
		"realm", "mesh", "nodeset", "controller", "worker", "munge", "slurmctld", "lock", "activity-log", "rollback", "trace",
		"slurmd", "slurmdbd", "slurmrestd", "sackd", "mariadb", "sshd", "submitter", "db", "api", "controller-backup", "job",
	} {
		_, ok := h.Word(id)
		assert.True(t, ok, "word %s", id)
	}
	for _, id := range []string{"first-cluster", "job-output", "job-pending", "clean-up"} {
		_, ok := h.Howto(id)
		assert.True(t, ok, "howto %s", id)
	}
	assert.NotEmpty(t, h.Words())
	assert.NotEmpty(t, h.Howtos())
}

func TestEmbeddedCommandsStartWithSind(t *testing.T) {
	for _, c := range Default().Commands() {
		assert.True(t, strings.HasPrefix(c, "sind "), c)
	}
}

// docsRoot is the directory of the Hugo content.
const docsRoot = "../../docs/content"

var headingRE = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)

// anchor returns the id that Hugo gives a heading.
func anchor(h string) string {
	h = strings.ToLower(strings.NewReplacer("`", "", "*", "").Replace(h))
	var b strings.Builder
	for _, r := range h {
		switch {
		case r == ' ' || r == '-':
			b.WriteByte('-')
		case r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestDocLinksExist(t *testing.T) {
	h := Default()
	var docs []string
	for _, w := range h.Words() {
		docs = append(docs, w.Doc)
	}
	for _, a := range h.Actions() {
		docs = append(docs, a.Doc)
	}
	for _, p := range h.Pages() {
		docs = append(docs, p.Doc)
	}
	for _, x := range h.Howtos() {
		docs = append(docs, x.Doc)
	}
	for _, doc := range docs {
		if doc == "" {
			continue // a word may have no page of its own
		}
		path, frag, hasFrag := strings.Cut(doc, "#")
		data, err := os.ReadFile(filepath.Join(docsRoot, path+".md"))
		if err != nil {
			data, err = os.ReadFile(filepath.Join(docsRoot, path, "_index.md"))
		}
		require.NoError(t, err, "doc %s", doc)
		if !hasFrag {
			continue
		}
		var anchors []string
		for _, m := range headingRE.FindAllStringSubmatch(string(data), -1) {
			anchors = append(anchors, anchor(m[1]))
		}
		assert.Contains(t, anchors, frag, "doc %s", doc)
	}
}

func TestDocURL(t *testing.T) {
	assert.Equal(t, "https://gsi-hpc.github.io/sind/usage/progress/", DocURL("usage/progress"))
	assert.Equal(t, "https://gsi-hpc.github.io/sind/usage/progress/#event-log", DocURL("usage/progress#event-log"))
}

func TestUIDocURL(t *testing.T) {
	assert.Equal(t, "https://gsi-hpc.github.io/sind/usage/progress/?via=ui", UIDocURL("usage/progress"))
	assert.Equal(t, "https://gsi-hpc.github.io/sind/usage/progress/?via=ui#event-log", UIDocURL("usage/progress#event-log"))
}

func TestLookups(t *testing.T) {
	h := Default()
	w, ok := h.Word("realm")
	require.True(t, ok)
	assert.Equal(t, "Realm", w.Title)
	_, ok = h.Word("nope")
	assert.False(t, ok)
	_, ok = h.Action("nope")
	assert.False(t, ok)
	p, ok := h.Page("overview")
	require.True(t, ok)
	assert.Equal(t, "/", p.Route)
	_, ok = h.Page("nope")
	assert.False(t, ok)
	_, ok = h.Howto("nope")
	assert.False(t, ok)

	for id, kind := range map[string]Kind{"realm": KindWord, "power": KindAction, "slow-create": KindHowto} {
		k, ok := h.Topic(id)
		assert.True(t, ok, id)
		assert.Equal(t, kind, k, id)
	}
	_, ok = h.Topic("nope")
	assert.False(t, ok)
}

func TestPageFor(t *testing.T) {
	h := Default()
	p, ok := h.PageFor("/clusters/sind/dev?tab=nodes#x")
	require.True(t, ok)
	assert.Equal(t, "cluster", p.ID)
	for _, path := range []string{"/nope", "/clusters/sind", "/clusters//dev", "/clusters/a/b/c", "/runs/"} {
		_, ok := h.PageFor(path)
		assert.False(t, ok, path)
	}
}

func TestSearch(t *testing.T) {
	h := Default()
	assert.Nil(t, h.Search("  "))
	assert.Nil(t, h.Search("zzzzqqq"))

	hits := h.Search("realm")
	require.NotEmpty(t, hits)
	assert.Equal(t, Hit{KindWord, "realm", "Realm", mustWord(t, "realm").Summary}, hits[0])

	hits = h.Search("lock")
	require.NotEmpty(t, hits)
	assert.Equal(t, "lock", hits[0].ID)

	// A title that holds the query comes before a summary that does.
	hits = h.Search("node")
	require.NotEmpty(t, hits)
	assert.Contains(t, strings.ToLower(hits[0].Title), "node")

	// Every word must match; the kinds are mixed.
	hits = h.Search("sind create worker")
	var kinds []Kind
	for _, hit := range hits {
		kinds = append(kinds, hit.Kind)
	}
	assert.Contains(t, kinds, KindAction)
	assert.Contains(t, kinds, KindHowto)
}

func mustWord(t *testing.T, id string) Word {
	t.Helper()
	w, ok := Default().Word(id)
	require.True(t, ok)
	return w
}

const (
	okWords   = "- {id: a, title: A, summary: S}\n"
	okActions = "- {id: x, title: X, summary: S, command: sind x, undo: U, doc: d, words: [a]}\n"
	okPages   = "- {id: p, title: P, summary: S, route: /p, doc: d, words: [a], howtos: [h]}\n"
	okHowtos  = "- {id: h, title: H, summary: S, steps: [one], commands: [], doc: d, words: [a]}\n"
)

func fixture(words, actions, pages, howtos string) fstest.MapFS {
	return fstest.MapFS{
		"words.yaml":   {Data: []byte(words)},
		"actions.yaml": {Data: []byte(actions)},
		"pages.yaml":   {Data: []byte(pages)},
		"howtos.yaml":  {Data: []byte(howtos)},
	}
}

func TestLoadFixtureIsValid(t *testing.T) {
	_, err := Load(fixture(okWords, okActions, okPages, okHowtos))
	require.NoError(t, err)
}

func TestLoadRefusesFaults(t *testing.T) {
	for name, tc := range map[string]struct {
		fs   fstest.MapFS
		want string
	}{
		"empty summary":        {fixture("- {id: a, title: A, summary: ' '}\n", okActions, okPages, okHowtos), "word \"a\": summary is empty"},
		"empty see":            {fixture("- {id: a, title: A, summary: S, see: ['']}\n", okActions, okPages, okHowtos), "see 1 is empty"},
		"empty command":        {fixture(okWords, strings.Replace(okActions, "command: sind x", "command: ''", 1), okPages, okHowtos), "action \"x\": command is empty"},
		"empty route":          {fixture(okWords, okActions, strings.Replace(okPages, "route: /p", "route: ''", 1), okHowtos), "route is empty"},
		"relative route":       {fixture(okWords, okActions, strings.Replace(okPages, "route: /p", "route: p", 1), okHowtos), "route must start with /"},
		"empty doc":            {fixture(okWords, okActions, okPages, strings.Replace(okHowtos, "doc: d", "doc: ''", 1)), "doc is empty"},
		"no steps":             {fixture(okWords, okActions, okPages, strings.Replace(okHowtos, "steps: [one]", "steps: []", 1)), "it has no steps"},
		"bad id":               {fixture("- {id: A b, title: A, summary: S}\n", okActions, okPages, okHowtos), "lower case"},
		"twice":                {fixture(okWords+okWords, okActions, okPages, okHowtos), "used twice"},
		"topic clash":          {fixture(okWords, strings.Replace(okActions, "id: x", "id: a", 1), okPages, okHowtos), "also a word"},
		"page may share an id": {fixture(okWords, okActions, strings.Replace(okPages, "id: p", "id: a", 1), okHowtos), ""},
		"unknown word":         {fixture(okWords, strings.Replace(okActions, "words: [a]", "words: [z]", 1), okPages, okHowtos), "word \"z\" does not exist"},
		"unknown see":          {fixture("- {id: a, title: A, summary: S, see: [z]}\n", okActions, okPages, okHowtos), "word \"z\" does not exist"},
		"unknown howto":        {fixture(okWords, okActions, strings.Replace(okPages, "howtos: [h]", "howtos: [z]", 1), okHowtos), "howto \"z\" does not exist"},
		"unknown page word":    {fixture(okWords, okActions, strings.Replace(okPages, "words: [a]", "words: [z]", 1), okHowtos), "word \"z\" does not exist"},
		"unknown howto word":   {fixture(okWords, okActions, okPages, strings.Replace(okHowtos, "words: [a]", "words: [z]", 1)), "word \"z\" does not exist"},
		"unknown field":        {fixture("- {id: a, title: A, summary: S, color: red}\n", okActions, okPages, okHowtos), "field color"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(tc.fs)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	fsys := fixture(okWords, okActions, okPages, okHowtos)
	delete(fsys, "pages.yaml")
	_, err := Load(fsys)
	require.Error(t, err)
}

func TestMustLoadPanicsOnFaultyTexts(t *testing.T) {
	assert.PanicsWithValue(t, "sind help texts: words.yaml: yaml: line 1: did not find expected ',' or ']'", func() {
		mustLoad(fixture("not: [a list", okActions, okPages, okHowtos))
	})
	assert.NotNil(t, mustLoad(fixture(okWords, okActions, okPages, okHowtos)))
}

func TestTextWithoutRelatedWords(t *testing.T) {
	h, err := Load(fixture(okWords, okActions, okPages, okHowtos))
	require.NoError(t, err)
	text, ok := h.Text("a")
	require.True(t, ok)
	assert.NotContains(t, text, "Related")
	assert.NotContains(t, text, "In a terminal")
}

func TestText(t *testing.T) {
	h := Default()
	for id, want := range map[string][]string{
		"realm":          {"Realm (word)", "$ sind get realms", "Related: mesh, lock", "https://gsi-hpc.github.io/sind/configuration/realms/"},
		"delete-cluster": {"Delete cluster (action of sind ui)", "Cannot be undone.", "$ sind delete cluster NAME"},
		"slow-create":    {"(how to)", "1. Open the run", "$ sind logs controller slurmctld"},
		"lock":           {"Realm lock (word)"},
	} {
		text, ok := h.Text(id)
		require.True(t, ok, id)
		for _, s := range want {
			assert.Contains(t, text, s, id)
		}
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, "More in the docs") {
				assert.LessOrEqual(t, len(line), width, line)
			}
		}
		assert.True(t, strings.HasSuffix(text, "\n"))
		assert.False(t, strings.HasSuffix(text, "\n\n"))
	}
	_, ok := h.Text("nope")
	assert.False(t, ok)
}

func TestIndex(t *testing.T) {
	idx := Default().Index()
	for _, s := range []string{"How to:", "Words:", "Actions of sind ui:", "slow-create", "realm", "delete-realm"} {
		assert.Contains(t, idx, s)
	}
}

func TestWrap(t *testing.T) {
	assert.Equal(t, "\n", wrap("", "", ""))
	long := strings.Repeat("word ", 40)
	lines := strings.Split(strings.TrimRight(wrap(long, "1. ", "   "), "\n"), "\n")
	require.Greater(t, len(lines), 1)
	assert.True(t, strings.HasPrefix(lines[0], "1. "))
	assert.True(t, strings.HasPrefix(lines[1], "   "))
	for _, l := range lines {
		assert.LessOrEqual(t, len(l), width)
	}
}

func TestActionUndoText(t *testing.T) {
	assert.Equal(t, "To undo: delete the workers.", Action{Undo: "Delete the workers"}.UndoText())
	assert.Equal(t, "Cannot be undone.", Action{Undo: "Cannot be undone", Irreversible: true}.UndoText())
	assert.Equal(t, ".", Action{}.UndoText())
}
