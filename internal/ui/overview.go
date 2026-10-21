// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

func init() { RegisterPage(overviewPage) }

// overviewPage is the Overview page, template overview.tmpl.
var overviewPage = Page{
	Name:    "overview",
	Pattern: "GET /{$}",
	Title:   "Overview",
	Nav:     "overview",
}
