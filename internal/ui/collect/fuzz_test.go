// SPDX-License-Identifier: LGPL-3.0-or-later

package collect

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// FuzzParseEvent checks the reader of the docker events stream of
// sind ui: no line makes it panic, and each event it makes names a realm.
func FuzzParseEvent(f *testing.F) {
	for _, seed := range []string{
		"",
		`{"Type":"container","Action":"start","Actor":{"Attributes":{"name":"sind-dev-controller","sind.realm":"sind","sind.cluster":"dev"}}}`,
		`{"Type":"container","Action":"die","Actor":{"Attributes":{"com.docker.compose.project":"sind-mesh"}}}`,
		`{"Type":"container","Action":"exec_create: sh","Actor":{"Attributes":{"sind.realm":"sind"}}}`,
		`{"Type":"network","Action":"create","Actor":{"Attributes":{"name":"sind-lock"}}}`,
		`{"Type":"network","Action":"destroy","Actor":{"Attributes":{"name":"sind-mesh"}}}`,
		`{"Type":"network","Action":"connect","Actor":{"Attributes":{"name":"my-realm-dev-net"}}}`,
		`{"Type":"network","Action":"connect","Actor":{"Attributes":{"name":"-lock"}}}`,
		`{"Type":"volume","Action":"create"}`,
		`{"Type":"network","Action":"create","Actor":null}`,
		`[]`, `null`, `{"Actor":{"Attributes":null}}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		for _, ev := range parseEvent(line, []string{"sind", "my", "my-realm"}) {
			assert.NotEmpty(t, ev.Realm)
			assert.Contains(t, []Kind{KindChange, KindLock}, ev.Kind)
		}
	})
}
