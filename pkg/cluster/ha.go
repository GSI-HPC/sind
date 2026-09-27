// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// Controller positions in a primary/backup pair, matching the order of the
// SlurmctldHost lines and the labels scontrol ping uses.
const (
	PositionPrimary = "primary"
	PositionBackup  = "backup"
)

// heartbeatMaxAge is how old the slurmctld heartbeat may be for its writer
// to still count as in control. slurmctld rewrites it every
// min(SlurmctldTimeout/4, 30) seconds, so two missed beats at the longest
// interval mean the writer is gone.
const heartbeatMaxAge = 60 * time.Second

// HAStatus describes a controller's place in a primary/backup pair.
type HAStatus struct {
	Position  string `json:"position"`   // "primary" or "backup"
	InControl bool   `json:"in_control"` // this controller currently runs the cluster
}

// controllerPosition returns the pair position for a controller short name.
func controllerPosition(shortName string) (position string, index int) {
	if shortName == ControllerBackupShortName {
		return PositionBackup, 1
	}
	return PositionPrimary, 0
}

// heartbeat is the content of the slurmctld heartbeat file.
type heartbeat struct {
	index int // SlurmctldHost index of the controller that wrote it
	age   time.Duration
}

// readHeartbeat reads the heartbeat the controller in control writes to
// StateSaveLocation: a big-endian uint64 Unix time followed by the writer's
// big-endian uint64 SlurmctldHost index. The container's clock is used for
// the age so host/container skew does not matter. It returns false when the
// file is missing or unreadable.
func readHeartbeat(ctx context.Context, client *docker.Client, container docker.ContainerName) (heartbeat, bool) {
	out, err := client.Exec(ctx, container, "sh", "-c",
		"date +%s; od -A n -t u1 -v "+slurm.StateSaveLocation+"/heartbeat")
	if err != nil {
		sindlog.From(ctx).DebugContext(ctx, "reading slurmctld heartbeat failed", "container", string(container), "err", err)
		return heartbeat{}, false
	}
	fields := strings.Fields(out)
	if len(fields) != 17 {
		return heartbeat{}, false
	}
	now, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return heartbeat{}, false
	}
	var values [2]uint64
	for i, f := range fields[1:] {
		b, err := strconv.ParseUint(f, 10, 8)
		if err != nil {
			return heartbeat{}, false
		}
		values[i/8] = values[i/8]<<8 | b
	}
	return heartbeat{
		index: int(values[1]),                                    //nolint:gosec // SlurmctldHost index, tiny
		age:   time.Duration(now-int64(values[0])) * time.Second, //nolint:gosec // Unix time fits int64
	}, true
}

// inControl reports whether the controller at index runs the cluster: it
// wrote a fresh heartbeat and its slurmctld answers.
func (hb heartbeat) inControl(index int, slurmctldUp bool) bool {
	return slurmctldUp && hb.index == index && hb.age <= heartbeatMaxAge
}

// setControllerHA fills in the HA status of every controller node of a
// primary/backup pair. The heartbeat is read from the first running
// controller; nodes without a readable heartbeat are reported as not in
// control.
func setControllerHA(ctx context.Context, client *docker.Client, realm, clusterName string, nodes []*NodeStatus) {
	var hb heartbeat
	var ok bool
	for _, n := range nodes {
		if n.Role == config.RoleController && n.Health.State == docker.StateRunning {
			shortName := strings.TrimSuffix(n.Name, "."+clusterName)
			hb, ok = readHeartbeat(ctx, client, ContainerName(realm, clusterName, shortName))
			break
		}
	}
	for _, n := range nodes {
		if n.Role != config.RoleController {
			continue
		}
		position, index := controllerPosition(strings.TrimSuffix(n.Name, "."+clusterName))
		n.Health.HA = &HAStatus{
			Position:  position,
			InControl: ok && hb.inControl(index, n.Health.Services[probe.ServiceSlurmctld]),
		}
	}
}

// nodeHA returns the HA status of a single controller, or nil when the node
// is not part of a primary/backup pair. The pair exists when the backup
// container does. The heartbeat is read from the node itself when it runs,
// otherwise from its partner.
func nodeHA(ctx context.Context, client *docker.Client, realm, clusterName, shortName string, health *NodeHealth) (*HAStatus, error) {
	partner := ControllerBackupShortName
	if shortName == ControllerBackupShortName {
		partner = string(config.RoleController)
	}
	partnerInfo, err := client.InspectContainer(ctx, ContainerName(realm, clusterName, partner))
	switch {
	case docker.IsNotFound(err):
		if shortName != ControllerBackupShortName {
			return nil, nil
		}
		partnerInfo = nil
	case err != nil:
		return nil, fmt.Errorf("inspecting %s: %w", partner, err)
	}

	source := ContainerName(realm, clusterName, shortName)
	if health.State != docker.StateRunning {
		if partnerInfo == nil || partnerInfo.Status != docker.StateRunning {
			source = ""
		} else {
			source = partnerInfo.Name
		}
	}

	position, index := controllerPosition(shortName)
	ha := &HAStatus{Position: position}
	if source != "" {
		if hb, ok := readHeartbeat(ctx, client, source); ok {
			ha.InControl = hb.inControl(index, health.Services[probe.ServiceSlurmctld])
		}
	}
	return ha, nil
}
