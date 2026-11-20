// SPDX-License-Identifier: LGPL-3.0-or-later

package natsort

import (
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// name draws a name of the kinds sind sorts: letters, separators and runs
// of digits, with leading zeros and runs too long for an integer.
func name() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.StringMatching(`[a-c-]{0,3}[0-9]{0,3}[a-c.]{0,2}[0-9]{0,25}`),
		rapid.StringMatching(`(worker|compute|dev)-?0{0,2}[0-9]{1,3}`),
		rapid.String(),
	)
}

// TestCompare_IsATotalOrder checks what a sort needs of Compare, and what
// the tables of the CLI and the UI need: only equal names compare equal,
// so that every sort of the same names gives the same order.
func TestCompare_IsATotalOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b, c := name().Draw(t, "a"), name().Draw(t, "b"), name().Draw(t, "c")

		assert.Equal(t, a == b, Compare(a, b) == 0, "only equal names compare equal")
		assert.Equal(t, Compare(a, b), -Compare(b, a), "the order of %q and %q does not depend on the side", a, b)
		if Compare(a, b) <= 0 && Compare(b, c) <= 0 {
			assert.LessOrEqual(t, Compare(a, c), 0, "%q <= %q <= %q", a, b, c)
		}

		names := []string{a, b, c}
		slices.SortFunc(names, Compare)
		reversed := []string{c, b, a}
		slices.SortFunc(reversed, Compare)
		assert.Equal(t, names, reversed, "the order does not depend on the input order")
	})
}

// TestCompare_OrdersNumbersByValue checks the natural order itself: names
// that differ in one number are in the order of that number, whatever its
// length and leading zeros.
func TestCompare_OrdersNumbersByValue(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := rapid.StringMatching(`[a-z-]{0,6}`).Draw(t, "prefix")
		suffix := rapid.StringMatching(`([a-z-][a-z0-9-]{0,5})?`).Draw(t, "suffix")
		x := rapid.StringMatching(`[0-9]{1,25}`).Draw(t, "x")
		y := rapid.StringMatching(`[0-9]{1,25}`).Draw(t, "y")

		byValue := value(t, x).Cmp(value(t, y))
		got := Compare(prefix+x+suffix, prefix+y+suffix)
		if byValue != 0 {
			assert.Equal(t, byValue, got, "%s and %s after %q", x, y, prefix)
		}
		assert.Equal(t, byValue == 0, Key(prefix+x+suffix) == Key(prefix+y+suffix),
			"numbers of one value, and only those, have one key")
		assert.Equal(t, strings.Compare(Key(prefix+x+suffix), Key(prefix+y+suffix)), byValue)
	})
}

// value returns the number that a run of digits stands for.
func value(t *rapid.T, digits string) *big.Int {
	n, ok := new(big.Int).SetString(digits, 10)
	require.True(t, ok, digits)
	return n
}
