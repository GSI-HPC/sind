// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

func init() { defaultRegistry.AddFragment(railFragment()) }

// railFragment is the rail, the page list at the left of every page. It
// depends on every topic, as its counts come from any part of the snapshot;
// the server sends it only when its HTML differs.
func railFragment() Fragment {
	return Fragment{
		Name:     "rail",
		Prefixes: []string{""},
		Shell:    true,
		Load:     func(v *View) error { v.Data = v.reg.rail(v); return nil },
	}
}
