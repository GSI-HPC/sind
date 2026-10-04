// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/GSI-HPC/sind/internal/termtext"
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

	cmd.AddCommand(newGetClusterCommand())
	cmd.AddCommand(newGetClustersCommand())
	cmd.AddCommand(newGetNodeCommand())
	cmd.AddCommand(newGetNodesCommand())
	cmd.AddCommand(newGetNetworksCommand())
	cmd.AddCommand(newGetRealmsCommand())
	cmd.AddCommand(newGetVolumesCommand())
	cmd.AddCommand(newGetAuthKeyCommand())
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
		Args:              optionalCluster,
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	clusters, err := cluster.GetClusters(cmd.Context(), client, realm)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), clusters)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tNODES (S/C/D/A/W)\tSLURM\tSTATUS")
	for _, c := range clusters {
		_, _ = fmt.Fprintf(w, "%s\t%d (%d/%d/%d/%d/%d)\t%s\t%s\n",
			cell(c.Name),
			c.NodeCount, c.Submitters, c.Controllers, c.DBs, c.APIs, c.Workers,
			formatSlurmVersion(c.SlurmVersion),
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
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
		_, _ = fmt.Fprintf(w, "%s\t%d\n", cell(r.Name), r.Clusters)
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	// An FQDN is NODE.CLUSTER.REALM.sind. Neither node short names nor
	// cluster names contain dots, so only the dot count tells it apart
	// from NODE.CLUSTER: the suffix alone also matches a cluster named
	// "sind" (controller.sind).
	if strings.Count(arg, ".") >= 2 && strings.HasSuffix(arg, "."+cluster.DNSSuffix) {
		return usagef("use NODE[.CLUSTER], not the FQDN %q", arg)
	}
	// Parse shortName.cluster on the first dot (neither node short names
	// nor cluster names contain dots).
	shortName, clusterName := arg, config.DefaultClusterName
	if i := strings.Index(arg, "."); i >= 0 {
		shortName = arg[:i]
		clusterName = arg[i+1:]
	}
	if err := config.CheckName("cluster", clusterName); err != nil {
		return usagef("invalid node name %q: %w", arg, err)
	}

	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	containerName := string(cluster.ContainerName(realm, clusterName, shortName))

	info, err := client.InspectContainer(cmd.Context(), docker.ContainerName(containerName))
	if docker.IsNotFound(err) {
		return fmt.Errorf("node %q not found in cluster %q", shortName, clusterName)
	}
	if err != nil {
		return fmt.Errorf("inspecting container: %w", err)
	}

	role := config.Role(info.Labels[cluster.LabelRole])
	health, err := cluster.GetNodeHealth(cmd.Context(), client, info, role, realm, clusterName)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), cluster.NodeDetail{
			Container: containerName,
			Cluster:   clusterName,
			Role:      role,
			Managed:   cluster.IsManaged(info.Labels),
			FQDN:      cluster.DNSName(shortName, clusterName, realm),
			IP:        health.IP,
			Status:    health.State,
			Services:  health.Services,
			HA:        health.HA,
		})
	}

	out := cmd.OutOrStdout()
	fqdn := cluster.DNSName(shortName, clusterName, realm)

	w := newTabWriter(out)
	if health.HA != nil {
		_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tHA\tFQDN\tIP\tSTATUS")
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", containerName, cell(role), formatHA(health.HA), fqdn, cell(health.IP), cell(health.State))
	} else {
		_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tFQDN\tIP\tSTATUS")
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", containerName, cell(role), fqdn, cell(health.IP), cell(health.State))
	}
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	nodes, err := cluster.GetAllNodes(cmd.Context(), client, realm)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), nodes)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "CONTAINER\tCLUSTER\tROLE\tFQDN\tIP\tSTATUS")
	for _, n := range nodes {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", cell(n.Container), cell(n.Cluster), cell(n.Role), cell(n.FQDN), cell(n.IP), cell(n.State))
	}
	return w.Flush()
}

func runGetNodes(cmd *cobra.Command, name string) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	nodes, err := cluster.GetNodes(cmd.Context(), client, realm, name)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), nodes)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tFQDN\tIP\tSTATUS")
	for _, n := range nodes {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", cell(n.Container), cell(n.Role), cell(n.FQDN), cell(n.IP), cell(n.State))
	}
	return w.Flush()
}

func runGetNetworks(cmd *cobra.Command) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	networks, err := cluster.GetNetworks(cmd.Context(), client, realm)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), networks)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tDRIVER\tSUBNET\tGATEWAY")
	for _, n := range networks {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cell(n.Name), cell(n.Driver), cell(n.Subnet), cell(n.Gateway))
	}
	return w.Flush()
}

func runGetVolumes(cmd *cobra.Command) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	volumes, err := cluster.GetVolumes(cmd.Context(), client, realm)
	if err != nil {
		return err
	}

	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), volumes)
	}

	w := newTabWriter(cmd.OutOrStdout())
	_, _ = fmt.Fprintln(w, "NAME\tDRIVER")
	for _, v := range volumes {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", cell(v.Name), cell(v.Driver))
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
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
		_, _ = fmt.Fprintf(w, "%s\t%s\n", cell(r.Hostname), cell(r.IP))
	}
	return w.Flush()
}

func newGetAuthKeyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth-key [CLUSTER]",
		Short: "Output a key that authenticates Slurm traffic (base64)",
		Long: `Output a key that authenticates the cluster's Slurm traffic, encoded as base64.

--type picks the Slurm authentication plugin whose key to print: munge,
slurm (auth/slurm, identity clientIds) or jwt (auth/jwt, which signs the
REST API tokens of a cluster with an api node). Without it, the key of the
cluster's AuthType: slurm.key with identity clientIds, the munge key
otherwise.`,
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := config.DefaultClusterName
			if len(args) > 0 {
				name = args[0]
			}
			return runGetAuthKey(cmd, name)
		},
	}
	cmd.Flags().String("type", "", "authentication plugin of the key: munge, slurm or jwt (default: the cluster's AuthType)")
	_ = cmd.RegisterFlagCompletionFunc("type", cobra.FixedCompletions(authTypeNames(), cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// authTypeNames returns the values --type of get auth-key takes.
func authTypeNames() []string {
	names := make([]string, len(cluster.AuthTypes))
	for i, t := range cluster.AuthTypes {
		names[i] = string(t)
	}
	return names
}

func runGetAuthKey(cmd *cobra.Command, name string) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	authType, _ := cmd.Flags().GetString("type")
	if authType != "" && !slices.Contains(cluster.AuthTypes, cluster.AuthType(authType)) {
		return usagef("invalid --type value %q: must be %s", authType, strings.Join(authTypeNames(), ", "))
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	key, err := cluster.GetAuthKey(cmd.Context(), client, realm, name, cluster.AuthType(authType))
	if err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(key.Key)
	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), struct {
			Type cluster.AuthType `json:"type"`
			Key  string           `json:"key"`
		}{Type: key.Type, Key: encoded})
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
			if err := validateOutputFlag(cmd); err != nil {
				return err
			}
			realm, err := realmFromFlag(cmd)
			if err != nil {
				return err
			}
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
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
	_, _ = fmt.Fprintf(w, "network\t%s\n", cell(info.Network))
	_, _ = fmt.Fprintf(w, "dns-container\t%s\n", cell(info.DNSContainer))
	_, _ = fmt.Fprintf(w, "dns-ip\t%s\n", cell(info.DNSIP))
	_, _ = fmt.Fprintf(w, "dns-zone\t%s\n", cell(info.DNSZone))
	_, _ = fmt.Fprintf(w, "dns-image\t%s\n", cell(info.DNSImage))
	_, _ = fmt.Fprintf(w, "ssh-container\t%s\n", cell(info.SSHContainer))
	_, _ = fmt.Fprintf(w, "ssh-volume\t%s\n", cell(info.SSHVolume))
	_, _ = fmt.Fprintf(w, "ssh-image\t%s\n", cell(info.SSHImage))
	return w.Flush()
}

func newGetClusterCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "cluster [NAME]",
		Short:             "Show cluster health status",
		Args:              optionalCluster,
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	status, err := cluster.GetStatus(cmd.Context(), client, realm, name)
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
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", cell(status.Name), formatSlurmVersion(status.SlurmVersion), formatState(status))
	if err := w.Flush(); err != nil {
		return err
	}

	// Networks table
	net := status.Network
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "NETWORKS")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tDRIVER\tSUBNET\tGATEWAY\tSTATUS")
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", cell(net.MeshName), cell(net.MeshDriver), cell(net.MeshSubnet), cell(net.MeshGateway), checkmark(net.Mesh))
	_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", cell(net.ClusterName), cell(net.ClusterDriver), cell(net.ClusterSubnet), cell(net.ClusterGateway), checkmark(net.Cluster))
	if err := w.Flush(); err != nil {
		return err
	}

	// Mesh services table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "MESH SERVICES")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "NAME\tCONTAINER\tSTATUS")
	_, _ = fmt.Fprintf(w, "dns\t%s\t%s\n", cell(net.DNSName), checkmark(net.DNS))
	_, _ = fmt.Fprintf(w, "ssh\t%s\t%s\n", cell(net.SSHName), checkmark(net.SSH))
	if err := w.Flush(); err != nil {
		return err
	}

	// Mounts table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "MOUNTS")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "MOUNT\tSOURCE\tTYPE\tSTATUS")
	for _, m := range status.Mounts {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cell(m.Path), cell(m.Source), cell(m.Type), checkmark(m.OK))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Nodes table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "NODES")
	w = newTabWriter(out)
	withHA := slices.ContainsFunc(status.Nodes, func(n *cluster.NodeStatus) bool { return n.Health.HA != nil })
	if withHA {
		_, _ = fmt.Fprintln(w, "NAME\tROLE\tHA\tIP\tSTATUS\tSERVICES")
	} else {
		_, _ = fmt.Fprintln(w, "NAME\tROLE\tIP\tSTATUS\tSERVICES")
	}
	for _, n := range status.Nodes {
		h := n.Health
		if withHA {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				cell(n.Name), cell(n.Role), formatHA(h.HA), cell(h.IP), cell(h.State), formatServices(h.Services))
			continue
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			cell(n.Name),
			cell(n.Role),
			cell(h.IP),
			cell(h.State),
			formatServices(h.Services),
		)
	}
	return w.Flush()
}

// formatHA renders a controller's pair position, marking the controller in
// control with "*". Nodes outside the pair render empty.
func formatHA(ha *cluster.HAStatus) string {
	if ha == nil {
		return ""
	}
	if ha.InControl {
		return ha.Position + "*"
	}
	return ha.Position
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

// formatSlurmVersion renders a cluster's Slurm version, "-" when sind does
// not know it (unmanaged clusters). The version comes from a label, so it
// is escaped like any other cell.
func formatSlurmVersion(version string) string {
	if version == "" {
		return "-"
	}
	return cell(version)
}

// cellReplacer shows the tab and newline that termtext.EscapeText keeps:
// in a table cell they would start a column or a row of their own.
var cellReplacer = strings.NewReplacer("\t", `\t`, "\n", `\n`)

// cell makes text that came from a container, a label or docker safe to
// print as a table cell. An image can set labels and print what sind
// reads, so such text may hold control sequences that retitle the
// terminal or write its clipboard; they are shown escaped (\x1b), as on
// the final error line.
func cell[S ~string](s S) string {
	return cellReplacer.Replace(termtext.EscapeText(string(s)))
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
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	client := clientFrom(cmd.Context())
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	mgr := meshMgrFrom(cmd.Context(), client, realm)

	var content string
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
