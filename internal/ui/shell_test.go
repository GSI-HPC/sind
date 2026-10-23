// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShellSkipLinkAndThemeToggle(t *testing.T) {
	ts := newTestServer(t).login()
	body := ts.get("/demo/x").Body.String()

	// The skip link is the first thing of the page that takes the focus,
	// and the content can take it.
	assert.Contains(t, body, "<body>\n<a class=\"skip\" href=\"#main\">Skip to content</a>\n<div class=\"app\"")
	assert.Contains(t, body, `<main class="main" id="main" tabindex="-1">`)
	// The theme button is a toggle that says its state.
	assert.Contains(t, body, `data-theme-toggle aria-label="Dark theme" aria-pressed="false"`)
}
