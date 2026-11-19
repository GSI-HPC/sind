// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/GSI-HPC/sind/internal/ui/demo"
)

// TestScreenshotToolAgrees checks the screenshot tool of the docs, a
// module of its own that cannot import sind's packages, against what it
// copies of them: the time of the still demo, to which it sets the clock
// of the pages, and the start of the line in which sind ui prints its URL.
func TestScreenshotToolAgrees(t *testing.T) {
	src, err := os.ReadFile("../../tools/screenshots/main.go")
	require.NoError(t, err)
	constant := func(name string) string {
		m := regexp.MustCompile(`(?m)^const ` + name + ` = ("[^"\n]*")$`).FindSubmatch(src)
		require.NotNil(t, m, "tools/screenshots/main.go has no const %s", name)
		v, err := strconv.Unquote(string(m[1]))
		require.NoError(t, err)
		return v
	}
	assert.Equal(t, demo.Epoch.Format(time.RFC3339), constant("epoch"), "the epoch of tools/screenshots is demo.Epoch")
	assert.Equal(t, urlLine, constant("urlPrefix"), "tools/screenshots finds the URL after urlLine")
}
