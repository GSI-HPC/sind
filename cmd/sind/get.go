// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/probe"
	"github.com/spf13/cobra"
)

func newGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}

	cmd.PersistentFlags().StringP("output", "o", "human", "output format (human|json)")
	cmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return validateOutputFlag(cmd)
	}

	cmd.AddCommand(newGetClusterCommand())
	cmd.AddCommand(newGetClustersCommand())
	cmd.AddCommand(newGetNodeCommand())
	cmd.AddCommand(newGetNodesCommand())
	cmd.AddCommand(newGetNetworksCommand())
	cmd.AddCommand(newGetRealmsCommand())
	cmd.AddCommand(newGetVolumesCommand())
	cmd.AddCommand(newGetMungeKeyCommand())
	cmd.AddCommand(newGetDNSCommand())
	cmd.AddCommand(newGetSSHConfigCommand())
	cmd.AddCommand(newGetMeshCommand())
	cmd.AddCommand(newGetSSHPrivateKeyCommand())
	cmd.AddCommand(newGetSSHPublicKeyCommand())
	cmd.AddCommand(newGetSSHKnownHostsCommand())

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
		Use:               "nodes [CLUSTER]",
		Short:             "List nodes",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return runGetNodes(cmd, args[0])
			}
			return runGetAllNodes(cmd)
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

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), clusters)
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

func newGetRealmsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "realms",
		Short: "List all realms",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetRealms(cmd)
		},
	}
}

func runGetRealms(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	realms, err := cluster.GetRealms(cmd.Context(), client)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), realms)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tCLUSTERS")
	for _, r := range realms {
		_, _ = fmt.Fprintf(w, "%s\t%d\n", r.Name, r.Clusters)
	}
	return w.Flush()
}

func newGetNodeCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "node NODE[.CLUSTER]",
		Short:             "Show node health status (accepts bare name or NODE.CLUSTER)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNodeNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGetNode(cmd, args[0])
		},
	}
}

func runGetNode(cmd *cobra.Command, arg string) error {
	if strings.HasSuffix(arg, "."+cluster.DNSSuffix) {
		return fmt.Errorf("use NODE[.CLUSTER], not the FQDN %q", arg)
	}
	// Parse shortName.cluster on the first dot (node short names do not
	// contain dots; cluster names may).
	shortName, clusterName := arg, config.DefaultClusterName
	if i := strings.Index(arg, "."); i >= 0 {
		shortName = arg[:i]
		clusterName = arg[i+1:]
	}

	client := clientFrom(cmd.Context())
	realm := realmFromFlag(cmd)
	containerName := string(cluster.ContainerName(realm, clusterName, shortName))

	info, err := client.InspectContainer(cmd.Context(), docker.ContainerName(containerName))
	if err != nil {
		return fmt.Errorf("inspecting container: %w", err)
	}

	role := config.Role(info.Labels[cluster.LabelRole])
	health, err := cluster.GetNodeHealth(cmd.Context(), client, containerName, role, realm, clusterName)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), cluster.NodeDetail{
			Container: containerName,
			Cluster:   clusterName,
			Role:      role,
			FQDN:      cluster.DNSName(shortName, clusterName, realm),
			IP:        health.IP,
			Status:    health.State,
			Services:  health.Services,
		})
	}

	out := cmd.OutOrStdout()
	fqdn := cluster.DNSName(shortName, clusterName, realm)

	w := newTabWriter(out)
	_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tFQDN\tIP\tSTATUS")
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", containerName, role, fqdn, health.IP, health.State)
	if err := w.Flush(); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "SERVICES")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS")
	for _, name := range sortedServiceNames(health.Services) {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", name, checkmark(health.Services[probe.Service(name)]))
	}
	return w.Flush()
}

// sortedServiceNames returns service names sorted alphabetically.
func sortedServiceNames(services cluster.ServiceHealth) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, string(name))
	}
	sort.Strings(names)
	return names
}

func runGetAllNodes(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	nodes, err := cluster.GetAllNodes(cmd.Context(), client, realmFromFlag(cmd))
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), nodes)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "CONTAINER\tCLUSTER\tROLE\tFQDN\tIP\tSTATUS")
	for _, n := range nodes {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", n.Container, n.Cluster, n.Role, n.FQDN, n.IP, n.State)
	}
	return w.Flush()
}

func runGetNodes(cmd *cobra.Command, name string) error {
	client := clientFrom(cmd.Context())
	nodes, err := cluster.GetNodes(cmd.Context(), client, realmFromFlag(cmd), name)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), nodes)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tFQDN\tIP\tSTATUS")
	for _, n := range nodes {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", n.Container, n.Role, n.FQDN, n.IP, n.State)
	}
	return w.Flush()
}

func runGetNetworks(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	networks, err := cluster.GetNetworks(cmd.Context(), client, realmFromFlag(cmd))
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), networks)
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

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), volumes)
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

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), records)
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
		Use:               "munge-key [CLUSTER]",
		Short:             "Output munge key (base64)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := config.DefaultClusterName
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
	encoded := base64.StdEncoding.EncodeToString(key)
	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), struct {
			Key string `json:"key"`
		}{Key: encoded})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), encoded)
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
			p := filepath.Join(dir, "ssh_config")
			if isJSONOutput(cmd) {
				return writeJSON(cmd.OutOrStdout(), struct {
					Path string `json:"path"`
				}{Path: p})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

func newGetMeshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "mesh",
		Short: "Show mesh infrastructure info",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetMesh(cmd)
		},
	}
}

func runGetMesh(cmd *cobra.Command) error {
	client := clientFrom(cmd.Context())
	realm := realmFromFlag(cmd)
	mgr := meshMgrFrom(cmd.Context(), client, realm)

	info, err := mgr.GetInfo(cmd.Context())
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), info)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "PROPERTY\tVALUE")
	_, _ = fmt.Fprintf(w, "network\t%s\n", info.Network)
	_, _ = fmt.Fprintf(w, "dns-container\t%s\n", info.DNSContainer)
	_, _ = fmt.Fprintf(w, "dns-ip\t%s\n", info.DNSIP)
	_, _ = fmt.Fprintf(w, "dns-zone\t%s\n", info.DNSZone)
	_, _ = fmt.Fprintf(w, "dns-image\t%s\n", info.DNSImage)
	_, _ = fmt.Fprintf(w, "ssh-container\t%s\n", info.SSHContainer)
	_, _ = fmt.Fprintf(w, "ssh-volume\t%s\n", info.SSHVolume)
	_, _ = fmt.Fprintf(w, "ssh-image\t%s\n", info.SSHImage)
	return w.Flush()
}

func newGetClusterCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "cluster [NAME]",
		Short:             "Show cluster health status",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := config.DefaultClusterName
			if len(args) > 0 {
				name = args[0]
			}
			return runGetCluster(cmd, name)
		},
	}
}

func runGetCluster(cmd *cobra.Command, name string) error {
	client := clientFrom(cmd.Context())
	status, err := cluster.GetStatus(cmd.Context(), client, realmFromFlag(cmd), name)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), status)
	}

	out := cmd.OutOrStdout()

	// Header table
	w := newTabWriter(out)
	_, _ = fmt.Fprintln(w, "CLUSTER\tSLURM\tSTATUS (R/S/P/T)")
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", status.Name, status.SlurmVersion, formatState(status))
	if err := w.Flush(); err != nil {
		return err
	}

	// Networks table
	net := status.Network
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "NETWORKS")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tDRIVER\tSUBNET\tGATEWAY\tSTATUS")
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", net.MeshName, net.MeshDriver, net.MeshSubnet, net.MeshGateway, checkmark(net.Mesh))
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", net.ClusterName, net.ClusterDriver, net.ClusterSubnet, net.ClusterGateway, checkmark(net.Cluster))
	if err := w.Flush(); err != nil {
		return err
	}

	// Mesh services table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "MESH SERVICES")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tCONTAINER\tSTATUS")
	_, _ = fmt.Fprintf(w, "dns\t%s\t%s\n", net.DNSName, checkmark(net.DNS))
	if err := w.Flush(); err != nil {
		return err
	}

	// Mounts table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "MOUNTS")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "MOUNT\tSOURCE\tTYPE\tSTATUS")
	for _, m := range status.Mounts {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Path, m.Source, m.Type, checkmark(m.OK))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Nodes table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "NODES")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tROLE\tIP\tSTATUS\tSERVICES")
	for _, n := range status.Nodes {
		h := n.Health
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			n.Name,
			n.Role,
			h.IP,
			h.State,
			formatServices(h.Services),
		)
	}
	return w.Flush()
}

func formatState(status *cluster.Status) string {
	var running, stopped, paused int
	for _, n := range status.Nodes {
		switch n.Health.State {
		case docker.StateRunning:
			running++
		case docker.StatePaused:
			paused++
		default:
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

func formatServices(services cluster.ServiceHealth) string {
	if len(services) == 0 {
		return ""
	}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, string(name))
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		parts = append(parts, name+" "+checkmark(services[probe.Service(name)]))
	}
	return strings.Join(parts, " ")
}

func newGetSSHPrivateKeyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh-private-key",
		Short: "Output SSH private key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetSSHKey(cmd, "private")
		},
	}
}

func newGetSSHPublicKeyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh-public-key",
		Short: "Output SSH public key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetSSHKey(cmd, "public")
		},
	}
}

func newGetSSHKnownHostsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh-known-hosts",
		Short: "Output SSH known_hosts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGetSSHKey(cmd, "known-hosts")
		},
	}
}

func runGetSSHKey(cmd *cobra.Command, kind string) error {
	client := clientFrom(cmd.Context())
	realm := realmFromFlag(cmd)
	mgr := meshMgrFrom(cmd.Context(), client, realm)

	var content string
	var err error
	switch kind {
	case "private":
		content, err = mgr.GetSSHPrivateKey(cmd.Context())
	case "public":
		content, err = mgr.GetSSHPublicKey(cmd.Context())
	case "known-hosts":
		content, err = mgr.GetSSHKnownHosts(cmd.Context())
	}
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		switch kind {
		case "private":
			return writeJSON(cmd.OutOrStdout(), struct {
				PrivateKey string `json:"private_key"`
			}{PrivateKey: content})
		case "public":
			return writeJSON(cmd.OutOrStdout(), struct {
				PublicKey string `json:"public_key"`
			}{PublicKey: content})
		case "known-hosts":
			return writeJSON(cmd.OutOrStdout(), struct {
				KnownHosts string `json:"known_hosts"`
			}{KnownHosts: content})
		}
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), content)
	return nil
}

func newTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
}
