// SPDX-License-Identifier: LGPL-3.0-or-later

package cluster_test

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/spf13/afero"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/cmdexec"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
)

// ExampleCreateInRealm creates a cluster as sind create cluster does: it
// checks the daemon before it takes the realm lock, holds the lock for the
// whole change, creates the cluster with its mesh, and brings the realm's
// SSH config export up to date.
func ExampleCreateInRealm() {
	if err := createCluster(context.Background(), "cluster.yaml"); err != nil {
		log.Fatal(err)
	}
}

// createCluster creates the cluster of the config file path.
func createCluster(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Parse reads the config; Create applies its defaults and validates it.
	cfg, err := config.Parse(data)
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings() {
		fmt.Fprintln(os.Stderr, "Warning:", w)
	}
	// The cluster's realm: the config's, else the default realm.
	realm := cmp.Or(cfg.Realm, mesh.DefaultRealm)
	cfg.Realm = realm
	// Create waits for the nodes and Slurm to become ready, as sind create
	// cluster --wait does.
	cfg.Wait = 5 * time.Minute

	client := docker.NewClient(&cmdexec.OSExecutor{})
	// A daemon that cannot run sind nodes fails here, before the caller
	// waits for the lock of another.
	if err := cluster.CheckDaemon(ctx, client); err != nil {
		return err
	}
	// The realm lock serializes the changes of sind processes and other
	// clients of the daemon; Create takes none itself.
	unlock, err := state.LockRealm(ctx, realm, &state.LockOptions{Client: client})
	if err != nil {
		return err
	}
	defer unlock()

	// CreateInRealm sets up the realm's mesh, removes a mesh it made when
	// that fails, and creates the cluster; Create alone needs a mesh that
	// is set up already.
	meshMgr := mesh.NewManager(client, realm)
	if err := cluster.CreateInRealm(ctx, client, meshMgr, cfg, cluster.CreateOptions{}); err != nil {
		return err
	}
	// The SSH config export in the state directory, for ssh -F.
	if err := cluster.ExportSSHConfig(ctx, client, afero.NewOsFs(), realm); err != nil {
		fmt.Fprintln(os.Stderr, "Warning: could not update SSH config:", err)
	}
	return nil
}
