// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
)

// validCapabilities lists all Linux capabilities recognized by Docker.
// Names follow Docker convention (without the CAP_ prefix).
// See capabilities(7) and https://docs.docker.com/reference/cli/docker/container/run/#privileged
var validCapabilities = map[string]struct{}{
	"ALL":                {},
	"AUDIT_CONTROL":      {},
	"AUDIT_READ":         {},
	"AUDIT_WRITE":        {},
	"BLOCK_SUSPEND":      {},
	"BPF":                {},
	"CHECKPOINT_RESTORE": {},
	"CHOWN":              {},
	"DAC_OVERRIDE":       {},
	"DAC_READ_SEARCH":    {},
	"FOWNER":             {},
	"FSETID":             {},
	"IPC_LOCK":           {},
	"IPC_OWNER":          {},
	"KILL":               {},
	"LEASE":              {},
	"LINUX_IMMUTABLE":    {},
	"MAC_ADMIN":          {},
	"MAC_OVERRIDE":       {},
	"MKNOD":              {},
	"NET_ADMIN":          {},
	"NET_BIND_SERVICE":   {},
	"NET_BROADCAST":      {},
	"NET_RAW":            {},
	"PERFMON":            {},
	"SETFCAP":            {},
	"SETGID":             {},
	"SETPCAP":            {},
	"SETUID":             {},
	"SYS_ADMIN":          {},
	"SYS_BOOT":           {},
	"SYS_CHROOT":         {},
	"SYS_MODULE":         {},
	"SYS_NICE":           {},
	"SYS_PACCT":          {},
	"SYS_PTRACE":         {},
	"SYS_RAWIO":          {},
	"SYS_RESOURCE":       {},
	"SYS_TIME":           {},
	"SYS_TTY_CONFIG":     {},
	"SYSLOG":             {},
	"WAKE_ALARM":         {},
}

// isValidCapability reports whether name is a recognized Linux capability.
func isValidCapability(name string) bool {
	_, ok := validCapabilities[name]
	return ok
}

// CheckCapabilities reports the first name in caps that is not a recognized
// Linux capability; field names the setting in the error, e.g. "capAdd".
func CheckCapabilities(field string, caps []string) error {
	for _, c := range caps {
		if !isValidCapability(c) {
			return fmt.Errorf("unknown capability %q in %s", c, field)
		}
	}
	return nil
}

// CheckDevices reports the first device whose host path is not absolute.
// A device is HOST_PATH[:CONTAINER_PATH[:PERMISSIONS]].
func CheckDevices(devices []string) error {
	for _, dev := range devices {
		hostDev := strings.SplitN(dev, ":", 2)[0]
		if !strings.HasPrefix(hostDev, "/") {
			return fmt.Errorf("device path must be absolute, got %q", dev)
		}
	}
	return nil
}
