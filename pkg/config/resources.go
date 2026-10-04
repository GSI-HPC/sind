// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// MinMemory is the smallest memory limit Docker accepts, in bytes.
const MinMemory = 6 << 20

// memoryUnits maps the units of Docker's size syntax, in lowercase, to
// their size in bytes: b, and k, m, g, t and p, each also with a b or ib
// suffix (kb, kib, ...), all powers of 1024.
var memoryUnits = func() map[string]float64 {
	units := map[string]float64{"": 1, "b": 1}
	for i, prefix := range []string{"k", "m", "g", "t", "p"} {
		size := math.Pow(1024, float64(i+1))
		units[prefix] = size
		units[prefix+"b"] = size
		units[prefix+"ib"] = size
	}
	return units
}()

// memoryNumber matches the number of a memory size: decimal digits with
// an optional fraction.
var memoryNumber = regexp.MustCompile(`^(\d+(\.\d*)?|\.\d+)$`)

// MemoryMB parses a memory limit in Docker's size syntax (docker run
// --memory) and returns it in MiB, rounded down, the unit of Slurm's
// RealMemory. The syntax is a decimal number, optionally with a fraction,
// followed by an optional unit: b, k, m, g, t or p, in either case, the
// last five optionally followed by b or ib. All units are powers of 1024,
// and a number without a unit counts bytes: 512m, 2g, 2GB, 2GiB, 1.5g,
// 2048k and 1073741824 are valid. The limit must be at least MinMemory.
func MemoryMB(s string) (int, error) {
	end := strings.LastIndexAny(s, "0123456789.") + 1
	number, unit := s[:end], strings.ToLower(s[end:])
	size, ok := memoryUnits[unit]
	if !memoryNumber.MatchString(number) || !ok {
		return 0, fmt.Errorf("want a number with an optional unit b, k, m, g, t or p, e.g. 512m or 1.5g")
	}
	n, _ := strconv.ParseFloat(number, 64) // memoryNumber matched
	bytes := n * size
	switch {
	case bytes < MinMemory:
		return 0, fmt.Errorf("it is below Docker's minimum of %dm", MinMemory>>20)
	case bytes >= math.MaxInt64:
		return 0, fmt.Errorf("it is too large")
	}
	return int(int64(bytes) >> 20), nil
}

// CheckMemory reports whether s is a valid memory limit (see MemoryMB);
// empty stands for the default. field names the value in the error.
func CheckMemory(field, s string) error {
	if s == "" {
		return nil
	}
	if _, err := MemoryMB(s); err != nil {
		return fmt.Errorf("invalid %s %q: %w", field, s, err)
	}
	return nil
}

// tmpSizePattern matches the size option of a tmpfs mount as the kernel
// reads it: a number of bytes with an optional unit k, m, g, t, p or e
// (powers of 1024, either case), or a percentage of the memory.
var tmpSizePattern = regexp.MustCompile(`^\d+([kKmMgGtTpPeE%])?$`)

// CheckTmpSize reports whether s is a valid size of a node's /tmp tmpfs,
// which sind passes to the kernel as the mount's size option; empty stands
// for the default. field names the value in the error.
func CheckTmpSize(field, s string) error {
	if s != "" && !tmpSizePattern.MatchString(s) {
		return fmt.Errorf("invalid %s %q: want a whole number with an optional unit k, m, g, t, p or e, or a percentage, e.g. 256m", field, s)
	}
	return nil
}

// CheckCPUs reports whether n is a valid CPU limit: zero stands for the
// default, and a negative number is invalid. field names the value in the
// error.
func CheckCPUs(field string, n int) error {
	if n < 0 {
		return fmt.Errorf("%s must not be negative, got %d", field, n)
	}
	return nil
}

// validateResources checks the resource limits of the defaults and of
// every node.
func (c *Cluster) validateResources() error {
	type limits struct {
		prefix       string
		cpus         int
		memory, size string
	}
	all := []limits{{"defaults.", c.Defaults.CPUs, c.Defaults.Memory, c.Defaults.TmpSize}}
	for _, n := range c.Nodes {
		all = append(all, limits{string(n.Role) + " ", n.CPUs, n.Memory, n.TmpSize})
	}
	for _, l := range all {
		if err := CheckCPUs(l.prefix+"cpus", l.cpus); err != nil {
			return err
		}
		if err := CheckMemory(l.prefix+"memory", l.memory); err != nil {
			return err
		}
		if err := CheckTmpSize(l.prefix+"tmpSize", l.size); err != nil {
			return err
		}
	}
	return nil
}
