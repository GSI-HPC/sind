// SPDX-License-Identifier: LGPL-3.0-or-later

// Package nodeset provides nodeset pattern expansion for Slurm-style node specifications.
// It implements a subset of the ClusterShell nodeset syntax.
//
// See: https://clustershell.readthedocs.io/en/latest/tools/nodeset.html
package nodeset

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Expand expands a nodeset pattern into a list of individual node names.
// Patterns can include:
//   - Simple names: "compute-0" → ["compute-0"]
//   - Comma-separated: "a,b,c" → ["a", "b", "c"]
//   - Ranges: "node-[0-3]" → ["node-0", "node-1", "node-2", "node-3"]
//   - Padded ranges: "node-[00-03]" → ["node-00", "node-01", "node-02", "node-03"]
//   - Lists: "node-[0,2,5]" → ["node-0", "node-2", "node-5"]
//   - Mixed: "node-[0-2,5]" → ["node-0", "node-1", "node-2", "node-5"]
func Expand(pattern string) ([]string, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}

	// Split on commas outside brackets
	parts := splitOutsideBrackets(pattern)

	var result []string
	for _, part := range parts {
		expanded, err := expandSingle(part)
		if err != nil {
			return nil, err
		}
		result = append(result, expanded...)
	}

	// Deduplicate preserving first occurrence
	seen := make(map[string]struct{}, len(result))
	deduped := make([]string, 0, len(result))
	for _, s := range result {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			deduped = append(deduped, s)
		}
	}

	// Sort using natural ordering (numeric segments compared numerically)
	slices.SortFunc(deduped, nodesetLess)

	return deduped, nil
}

// splitOutsideBrackets splits a pattern on commas that are not inside brackets.
func splitOutsideBrackets(pattern string) []string {
	var parts []string
	var current strings.Builder
	depth := 0

	for _, ch := range pattern {
		switch ch {
		case '[':
			depth++
			current.WriteRune(ch)
		case ']':
			depth--
			current.WriteRune(ch)
		case ',':
			if depth == 0 {
				parts = append(parts, current.String())
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}
	parts = append(parts, current.String())
	return parts
}

// expandSingle expands a single nodeset pattern (no top-level commas).
func expandSingle(pattern string) ([]string, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty element in pattern")
	}

	// Find bracket expression using Cut
	prefix, rest, hasBracket := strings.Cut(pattern, "[")
	if !hasBracket {
		// No brackets, return as-is
		return []string{pattern}, nil
	}

	bracketContent, suffix, hasClosed := strings.Cut(rest, "]")
	if !hasClosed {
		return nil, fmt.Errorf("unclosed bracket in pattern: %s", pattern)
	}

	// Check for multiple bracket groups (not supported)
	if strings.Contains(suffix, "[") {
		return nil, fmt.Errorf("multiple bracket groups not supported: %s", pattern)
	}

	// Parse the bracket content (may contain commas and ranges)
	expanded, err := expandBracket(bracketContent)
	if err != nil {
		return nil, fmt.Errorf("invalid bracket expression in %s: %w", pattern, err)
	}

	// Build result by combining prefix + each expanded value + suffix
	result := make([]string, len(expanded))
	for i, val := range expanded {
		result[i] = prefix + val + suffix
	}
	return result, nil
}

// expandBracket expands the content inside brackets.
// Supports:
//   - Single values: "5" → ["5"]
//   - Ranges: "0-3" → ["0", "1", "2", "3"]
//   - Lists: "0,2,5" → ["0", "2", "5"]
//   - Mixed: "0-2,5" → ["0", "1", "2", "5"]
func expandBracket(content string) ([]string, error) {
	if content == "" {
		return nil, fmt.Errorf("empty bracket expression")
	}

	// Split on commas to handle lists
	parts := strings.Split(content, ",")

	var result []string
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("empty element in bracket expression")
		}
		expanded, err := expandRangeOrValue(part)
		if err != nil {
			return nil, err
		}
		result = append(result, expanded...)
	}
	return result, nil
}

// expandRangeOrValue expands a single range (e.g., "0-3") or value (e.g., "5").
func expandRangeOrValue(part string) ([]string, error) {
	startStr, endStr, isRange := strings.Cut(part, "-")
	if !isRange {
		// Single value
		return []string{part}, nil
	}

	return expandRange(startStr, endStr)
}

// expandRange expands a range like "0" to "3" or "00" to "03".
func expandRange(startStr, endStr string) ([]string, error) {
	start, err := strconv.Atoi(startStr)
	if err != nil {
		return nil, fmt.Errorf("invalid range start: %s", startStr)
	}

	end, err := strconv.Atoi(endStr)
	if err != nil {
		return nil, fmt.Errorf("invalid range end: %s", endStr)
	}

	if start > end {
		return nil, fmt.Errorf("invalid range: start %d > end %d", start, end)
	}

	// Determine padding width from the start string
	width := len(startStr)

	result := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		result = append(result, fmt.Sprintf("%0*d", width, i))
	}
	return result, nil
}

// isDigit reports whether b is an ASCII digit.
func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// parseNodeName splits a node name into prefix, numeric index, and suffix
// by finding the last group of consecutive ASCII digits.
// Examples:
//
//	"compute-0.dev" → ("compute-", "0", ".dev")
//	"controller"    → ("controller", "", "")
//	"node-12"       → ("node-", "12", "")
func parseNodeName(name string) (prefix, indexStr, suffix string) {
	// Scan backwards past non-digit suffix
	i := len(name) - 1
	for i >= 0 && !isDigit(name[i]) {
		i--
	}
	if i < 0 {
		return name, "", ""
	}
	digitEnd := i + 1

	// Scan backwards past digits to find start of numeric group
	for i >= 0 && isDigit(name[i]) {
		i--
	}
	digitStart := i + 1

	return name[:digitStart], name[digitStart:digitEnd], name[digitEnd:]
}

// nodesetLess compares two node names using ClusterShell-compatible ordering.
// Names are split into (prefix, numericIndex, suffix) by finding the last
// digit group. Sorting is by (prefix, suffix, numericIndex), which groups
// nodes sharing the same prefix and suffix together, sorted numerically.
func nodesetLess(a, b string) int {
	prefA, idxA, sufA := parseNodeName(a)
	prefB, idxB, sufB := parseNodeName(b)

	if c := strings.Compare(prefA, prefB); c != 0 {
		return c
	}
	if c := strings.Compare(sufA, sufB); c != 0 {
		return c
	}

	// Same prefix and suffix — compare numeric index
	if idxA == "" || idxB == "" {
		return strings.Compare(idxA, idxB)
	}

	numA, _ := strconv.Atoi(idxA)
	numB, _ := strconv.Atoi(idxB)
	return numA - numB
}
