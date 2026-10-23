// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/slurm"
)

// SlurmInfo is what Slurm itself says about a cluster: its nodes and
// partitions as sinfo reports them on the controller, and the Slurm config
// files that sind wrote.
type SlurmInfo struct {
	Nodes      []slurm.NodeInfo      `json:"nodes"`
	Partitions []slurm.PartitionInfo `json:"partitions"`
	// Files are slurm.conf and sind-nodes.conf, the ones of them that the
	// controller could read. slurmdbd.conf holds a password and is not
	// read.
	Files []SlurmFile `json:"files"`
}

// SlurmFile is a config file of the config volume.
type SlurmFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// slurmInfoFiles are the files that GetSlurmInfo reads.
var slurmInfoFiles = []string{slurm.SlurmConfPath, slurm.NodesConfPath}

// slurmInfoTimeout bounds GetSlurmInfo: sinfo waits for a slurmctld that
// hangs, or for a failover to the backup controller, for its message
// timeout and retries, and sind ui reads it every few seconds.
var slurmInfoTimeout = 10 * time.Second

// errSlurmInfoTimeout is the cause of GetSlurmInfo's context when
// slurmInfoTimeout ends it.
var errSlurmInfoTimeout = errors.New("sinfo timed out")

// GetSlurmInfo reads the cluster's slurm.conf and sind-nodes.conf on its
// controller and runs sinfo there, within slurmInfoTimeout. A controller
// that does not run, or a slurmctld that does not answer, is an error; the
// SlurmInfo then holds the files alone, which cat reads without slurmctld,
// and is nil when there are none. A file that cannot be read is left out.
func GetSlurmInfo(ctx context.Context, client *docker.Client, realm, clusterName string) (*SlurmInfo, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, slurmInfoTimeout, errSlurmInfoTimeout)
	defer cancel()
	controller := ContainerName(realm, clusterName, slurm.ControllerHost)
	info := &SlurmInfo{}
	for _, path := range slurmInfoFiles {
		if content, err := client.ReadFile(ctx, controller, path); err == nil {
			info.Files = append(info.Files, SlurmFile{Path: path, Content: content})
		}
	}
	err := readSinfo(ctx, client, controller, info)
	if err == nil {
		return info, nil
	}
	if errors.Is(context.Cause(ctx), errSlurmInfoTimeout) {
		err = fmt.Errorf("sinfo on the controller did not answer within %s", slurmInfoTimeout)
	}
	if len(info.Files) == 0 {
		return nil, err
	}
	return &SlurmInfo{Files: info.Files}, err
}

// readSinfo runs sinfo for the nodes and the partitions on the controller
// into info.
func readSinfo(ctx context.Context, client *docker.Client, controller docker.ContainerName, info *SlurmInfo) error {
	out, err := client.Exec(ctx, controller, slurm.SinfoNodesCommand...)
	if err != nil {
		return fmt.Errorf("sinfo on the controller: %w", err)
	}
	if info.Nodes, err = slurm.ParseSinfoNodes(out); err != nil {
		return err
	}
	if out, err = client.Exec(ctx, controller, slurm.SinfoPartitionsCommand...); err != nil {
		return fmt.Errorf("sinfo on the controller: %w", err)
	}
	info.Partitions, err = slurm.ParseSinfoPartitions(out)
	return err
}
