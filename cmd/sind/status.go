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
	status, err := cluster.GetStatus(cmd.Context(), client, name)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Cluster: %s\n", status.Name)
	fmt.Fprintf(out, "Status:  %s\n", status.Status)

	// Nodes table
	fmt.Fprintln(out)
	fmt.Fprintln(out, "NODES")
	w := newTabWriter(out)
	fmt.Fprintln(w, "NAME\tROLE\tIP\tCONTAINER\tMUNGE\tSSHD\tSERVICES")
	for _, n := range status.Nodes {
		h := n.Health
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			n.Name,
			n.Role,
			h.IP,
			h.Container,
			checkmark(h.Munge),
			checkmark(h.SSHD),
			formatServices(h.Services),
		)
	}
	w.Flush()

	// Network section
	fmt.Fprintln(out)
	fmt.Fprintln(out, "NETWORK")
	net := status.Network
	fmt.Fprintf(out, "Mesh:     sind-mesh %s\n", checkmark(net.Mesh))
	fmt.Fprintf(out, "DNS:      sind-dns %s\n", checkmark(net.DNS))
	fmt.Fprintf(out, "Cluster:  sind-%s-net %s\n", name, checkmark(net.Cluster))

	// Volumes section
	fmt.Fprintln(out)
	fmt.Fprintln(out, "VOLUMES")
	vol := status.Volumes
	fmt.Fprintf(out, "sind-%s-config %s\n", name, checkmark(vol.Config))
	fmt.Fprintf(out, "sind-%s-munge %s\n", name, checkmark(vol.Munge))
	fmt.Fprintf(out, "sind-%s-data %s\n", name, checkmark(vol.Data))

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
