// SPDX-License-Identifier: LGPL-3.0-or-later

package mesh

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// FuzzParseEntries checks the reader of the DNS container's Corefile,
// which a user can change in the container: no content makes it panic,
// each entry it returns starts with an IP address, and a Corefile written
// with the entries reads back as the same entries.
func FuzzParseEntries(f *testing.F) {
	for _, seed := range []string{
		"",
		generateCorefile("sind", nil),
		generateCorefile("sind", []string{"172.18.0.3 controller.default.sind.sind", "fd00::3 worker-0.default.sind.sind"}),
		"hosts {\n10.0.0.1 a\n}\nhosts {\n10.0.0.2\tb\nfallthrough\n10.0.0.3 c\n}\n",
		"hosts {\n  ttl 5\n  not-an-ip name\n  1.2.3.4\n",
		"1.2.3.4 outside\nhosts {\r\n1.2.3.4 a\r\n}\r\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, corefile string) {
		entries := parseEntries(corefile)
		for _, e := range entries {
			assert.True(t, isEntry(e), "entry %q", e)
			assert.Equal(t, strings.TrimSpace(e), e)
		}
		assert.Equal(t, entries, parseEntries(generateCorefile("sind", entries)))

		// Removing the names of the entries leaves none of them.
		var names []string
		for _, e := range entries {
			if fields := strings.Fields(e); len(fields) > 1 {
				names = append(names, fields[1])
			}
		}
		for _, e := range withoutNames(slices.Clone(entries), 1, names) {
			assert.Len(t, strings.Fields(e), 1, "entry %q has a name", e)
		}
	})
}
