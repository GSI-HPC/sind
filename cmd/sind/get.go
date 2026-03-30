// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
	"text/tabwriter"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/spf13/cobra"
)

func newGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}

	cmd.AddCommand(newGetClustersCommand())
	cmd.AddCommand(newGetNodesCommand())
	cmd.AddCommand(newGetNetworksCommand())
	cmd.AddCommand(newGetVolumesCommand())
	cmd.AddCommand(newGetMungeKeyCommand())
	cmd.AddCommand(newGetDNSCommand())
	cmd.AddCommand(newGetSSHConfigCommand())

	return cmd
}

func newGetClustersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "List all clusters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetClusters(cmd)
		},
	}
}

func newGetNodesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "nodes [CLUSTER]",
		Short: "List nodes in a cluster",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
			if len(args) > 0 {
				name = args[0]
			}
			return runGetNodes(cmd, name)
		},
	}
}

func newGetNetworksCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "networks",
		Short: "List sind networks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetNetworks(cmd)
		},
	}
}

func newGetVolumesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "volumes",
		Short: "List sind volumes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetVolumes(cmd)
		},
	}
}

func runGetClusters(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	clusters, err := cluster.GetClusters(cmd.Context(), client, realmFromFlag(cmd))
	if err != nil {
		return err
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tNODES (S/C/W)\tSLURM\tSTATUS")
	for _, c := range clusters {
		_, _ = fmt.Fprintf(w, "%s\t%d (%d/%d/%d)\t%s\t%s\n",
			c.Name,
			c.NodeCount, c.Submitters, c.Controllers, c.Workers,
			c.SlurmVersion,
			c.State,
		)
	}
	return w.Flush()
}

func runGetNodes(cmd *cobra.Command, name string) error {
	client := clientFrom(cmd.Context())
	nodes, err := cluster.GetNodes(cmd.Context(), client, realmFromFlag(cmd), name)
	if err != nil {
		return err
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tROLE\tSTATUS")
	for _, n := range nodes {
		_, _ = fmt.Fprintf(w, "%s.%s\t%s\t%s\n", n.Name, name, n.Role, n.State)
	}
	return w.Flush()
}

func runGetNetworks(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	networks, err := cluster.GetNetworks(cmd.Context(), client, realmFromFlag(cmd))
	if err != nil {
		return err
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tDRIVER\tSUBNET\tGATEWAY")
	for _, n := range networks {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n.Name, n.Driver, n.Subnet, n.Gateway)
	}
	return w.Flush()
}

func runGetVolumes(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	volumes, err := cluster.GetVolumes(cmd.Context(), client, realmFromFlag(cmd))
	if err != nil {
		return err
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tDRIVER")
	for _, v := range volumes {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", v.Name, v.Driver)
	}
	return w.Flush()
}

func newGetDNSCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dns",
		Short: "List mesh DNS records",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetDNS(cmd)
		},
	}
}

func runGetDNS(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	realm := realmFromFlag(cmd)
	mgr := meshMgrFrom(cmd.Context(), client, realm)

	records, err := mgr.GetDNSRecords(cmd.Context())
	if err != nil {
		return err
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "HOSTNAME\tIP")
	for _, r := range records {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", r.Hostname, r.IP)
	}
	return w.Flush()
}

func newGetMungeKeyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "munge-key [CLUSTER]",
		Short: "Output munge key (base64)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
			if len(args) > 0 {
				name = args[0]
			}
			return runGetMungeKey(cmd, name)
		},
	}
}

func runGetMungeKey(cmd *cobra.Command, name string) error {
	client := clientFrom(cmd.Context())
	key, err := cluster.GetMungeKey(cmd.Context(), client, realmFromFlag(cmd), name)
	if err != nil {
		return err
	}
	cmd.Println(base64.StdEncoding.EncodeToString(key))
	return nil
}

func newGetSSHConfigCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh-config",
		Short: "Show SSH config path for the current realm",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			realm := realmFromFlag(cmd)
			dir, err := sindStateDir(realm)
			if err != nil {
				return err
			}
			cmd.Println(filepath.Join(dir, "ssh_config"))
			return nil
		},
	}
}

func newTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
}
