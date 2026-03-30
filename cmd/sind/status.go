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
		Use:               "status [CLUSTER]",
		Short:             "Show cluster health status",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeClusterNames,
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

	out := cmd.OutOrStdout()

	// Header table
	w := newTabWriter(out)
	_, _ = fmt.Fprintln(w, "CLUSTER\tSTATUS (R/S/P/T)")
	_, _ = fmt.Fprintf(w, "%s\t%s\n", status.Name, formatState(status))
	if err := w.Flush(); err != nil {
		return err
	}

	// Networks table
	net := status.Network
	cmd.Println()
	cmd.Println("NETWORKS")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tDRIVER\tSUBNET\tGATEWAY\tSTATUS")
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", net.MeshName, net.MeshDriver, net.MeshSubnet, net.MeshGateway, checkmark(net.Mesh))
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", net.ClusterName, net.ClusterDriver, net.ClusterSubnet, net.ClusterGateway, checkmark(net.Cluster))
	if err := w.Flush(); err != nil {
		return err
	}

	// Mesh services table
	cmd.Println()
	cmd.Println("MESH SERVICES")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tCONTAINER\tSTATUS")
	_, _ = fmt.Fprintf(w, "dns\t%s\t%s\n", net.DNSName, checkmark(net.DNS))
	if err := w.Flush(); err != nil {
		return err
	}

	// Mounts table
	cmd.Println()
	cmd.Println("MOUNTS")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "MOUNT\tSOURCE\tTYPE\tSTATUS")
	for _, m := range status.Mounts {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Path, m.Source, m.Type, checkmark(m.OK))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Nodes table (last, as it can be the longest)
	cmd.Println()
	cmd.Println("NODES")
	w = newTabWriter(out)
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

	return nil
}

func formatState(status *cluster.Status) string {
	var running, stopped, paused int
	for _, n := range status.Nodes {
		switch n.Health.Container {
		case "running":
			running++
		case "paused":
			paused++
		default: // exited, dead, created, etc.
			stopped++
		}
	}
	total := running + stopped + paused
	return fmt.Sprintf("%s (%d/%d/%d/%d)", status.State, running, stopped, paused, total)
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
