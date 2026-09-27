// SPDX-License-Identifier: LGPL-3.0-or-later

// Package hostname decides whether a name may be used as one label of a
// host name.
//
// Adapted from GSI-HPC/clusterctl internal/hostname.
package hostname

import "fmt"

// maxLabel is the longest label DNS allows between two dots.
const maxLabel = 63

// CheckLabel reports whether label is a host name label as RFC 1123 spells
// it: ASCII letters, digits and hyphens, not empty, not longer than 63
// characters, and not beginning or ending with a hyphen.
//
// A name that passes has no character that a path, a DNS name, a Docker
// resource name or a command line gives a meaning to: no "/", "." or "..",
// no leading "-" that a command would read as an option.
func CheckLabel(label string) error {
	switch {
	case label == "":
		return fmt.Errorf("it is empty")
	case len(label) > maxLabel:
		return fmt.Errorf("it is longer than %d characters", maxLabel)
	case label[0] == '-':
		return fmt.Errorf("it begins with a hyphen")
	case label[len(label)-1] == '-':
		return fmt.Errorf("it ends with a hyphen")
	}
	for _, r := range label {
		if !isHostChar(r) {
			return fmt.Errorf("%q is not a letter, a digit or a hyphen", r)
		}
	}
	return nil
}

func isHostChar(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}
