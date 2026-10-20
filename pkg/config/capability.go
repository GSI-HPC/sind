// SPDX-License-Identifier: LGPL-3.0-or-later

package config

import (
	"fmt"
	"strings"
)

// validCapabilities lists all Linux capabilities recognized by Docker.
// Names follow Docker convention (without the CAP_ prefix).
// See capabilities(7) and https://docs.docker.com/reference/cli/docker/container/run/#privileged
var validCapabilities = map[string]bool{
	"ALL":                true,
	"AUDIT_CONTROL":      true,
	"AUDIT_READ":         true,
	"AUDIT_WRITE":        true,
	"BLOCK_SUSPEND":      true,
	"BPF":                true,
	"CHECKPOINT_RESTORE": true,
	"CHOWN":              true,
	"DAC_OVERRIDE":       true,
	"DAC_READ_SEARCH":    true,
	"FOWNER":             true,
	"FSETID":             true,
	"IPC_LOCK":           true,
	"IPC_OWNER":          true,
	"KILL":               true,
	"LEASE":              true,
	"LINUX_IMMUTABLE":    true,
	"MAC_ADMIN":          true,
	"MAC_OVERRIDE":       true,
	"MKNOD":              true,
	"NET_ADMIN":          true,
	"NET_BIND_SERVICE":   true,
	"NET_BROADCAST":      true,
	"NET_RAW":            true,
	"PERFMON":            true,
	"SETFCAP":            true,
	"SETGID":             true,
	"SETPCAP":            true,
	"SETUID":             true,
	"SYS_ADMIN":          true,
	"SYS_BOOT":           true,
	"SYS_CHROOT":         true,
	"SYS_MODULE":         true,
	"SYS_NICE":           true,
	"SYS_PACCT":          true,
	"SYS_PTRACE":         true,
	"SYS_RAWIO":          true,
	"SYS_RESOURCE":       true,
	"SYS_TIME":           true,
	"SYS_TTY_CONFIG":     true,
	"SYSLOG":             true,
	"WAKE_ALARM":         true,
}

// CheckCapabilities reports the first name in caps that is not a recognized
// Linux capability; field names the setting in the error, e.g. "capAdd".
func CheckCapabilities(field string, caps []string) error {
	for _, c := range caps {
		if !validCapabilities[c] {
			return fmt.Errorf("unknown capability %q in %s", c, field)
		}
	}
	return nil
}

// securityOptNames lists the names of the Linux --security-opt options
// Docker takes: label, apparmor, seccomp, no-new-privileges and
// writable-cgroups, which the daemon reads, and systempaths, which the
// docker CLI reads.
var securityOptNames = map[string]bool{
	"label":             true,
	"apparmor":          true,
	"seccomp":           true,
	"no-new-privileges": true,
	"writable-cgroups":  true,
	"systempaths":       true,
}

// CheckSecurityOpts reports the first security option in opts that Docker
// would reject for its form: NAME=VALUE (or Docker's older NAME:VALUE) with
// a name in securityOptNames and a value, or no-new-privileges alone. field
// names the setting in the error, e.g. "securityOpt". The values are left
// to Docker, which checks them when it creates the container.
func CheckSecurityOpts(field string, opts []string) error {
	for _, opt := range opts {
		if opt == "no-new-privileges" {
			continue
		}
		name, value, ok := strings.Cut(opt, "=")
		if !ok {
			name, value, ok = strings.Cut(opt, ":")
		}
		if !ok || value == "" || !securityOptNames[name] {
			return fmt.Errorf("unknown security option %q in %s: want label=, apparmor=, seccomp=, no-new-privileges, writable-cgroups= or systempaths=", opt, field)
		}
	}
	return nil
}

// CheckDevices reports the first device whose host path is not absolute.
// A device is HOST_PATH[:CONTAINER_PATH[:PERMISSIONS]]. field names the
// setting in the error, e.g. "devices".
func CheckDevices(field string, devices []string) error {
	for _, dev := range devices {
		hostDev := strings.SplitN(dev, ":", 2)[0]
		if !strings.HasPrefix(hostDev, "/") {
			return fmt.Errorf("device %q in %s: host path must be absolute", dev, field)
		}
	}
	return nil
}
