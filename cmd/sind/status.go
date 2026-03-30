// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status [CLUSTER]",
		Short: "Show cluster health status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
			if len(args) > 0 {
				name = args[0]
			}
			return runStatus(cmd, name)
		},
	}
}

func runStatus(cmd *cobra.Command, name string) error {
	client := clientFrom(cmd.Context())
	status, err := cluster.GetStatus(cmd.Context(), client, realmFromFlag(cmd), name)
	if err != nil {
		return err
	}

	cmd.Printf("Cluster: %s\n", status.Name)
	cmd.Printf("Status:  %s\n", status.State)

	// Nodes table
	cmd.Println()
	cmd.Println("NODES")
	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tROLE\tIP\tCONTAINER\tMUNGE\tSSHD\tSERVICES")
	for _, n := range status.Nodes {
		h := n.Health
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			n.Name,
			n.Role,
			h.IP,
			h.Container,
			checkmark(h.Munge),
			checkmark(h.SSHD),
			formatServices(h.Services),
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Network section
	cmd.Println()
	cmd.Println("NETWORK")
	net := status.Network
	cmd.Printf("Mesh:     sind-mesh %s  %s  gw %s\n", checkmark(net.Mesh), net.MeshSubnet, net.MeshGateway)
	cmd.Printf("DNS:      sind-dns %s\n", checkmark(net.DNS))
	cmd.Printf("Cluster:  sind-%s-net %s  %s  gw %s\n", name, checkmark(net.Cluster), net.ClusterSubnet, net.ClusterGateway)

	// Volumes section
	cmd.Println()
	cmd.Println("VOLUMES")
	vol := status.Volumes
	cmd.Printf("sind-%s-config %s\n", name, checkmark(vol.Config))
	cmd.Printf("sind-%s-munge %s\n", name, checkmark(vol.Munge))
	cmd.Printf("sind-%s-data %s\n", name, checkmark(vol.Data))

	return nil
}

func checkmark(ok bool) string {
	if ok {
		return "\u2713"
	}
	return "\u2717"
}

func formatServices(services map[string]bool) string {
	if len(services) == 0 {
		return ""
	}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		parts = append(parts, name+" "+checkmark(services[name]))
	}
	return strings.Join(parts, " ")
}
