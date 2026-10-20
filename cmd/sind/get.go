// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/GSI-HPC/sind/pkg/state"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

func newGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}

	cmd.PersistentFlags().StringP("output", "o", "human", "output format (human|json)")
	registerOutputCompletion(cmd)

	cmd.AddCommand(newGetClusterCommand())
	cmd.AddCommand(newGetNodeCommand())
	cmd.AddCommand(newGetNodesCommand())
	cmd.AddCommand(newGetAuthKeyCommand())
	// The subcommands that take no argument.
	for _, c := range []struct {
		use, short string
		run        func(*cobra.Command, []string) error
	}{
		{"clusters", "List all clusters", runGetClusters},
		{"networks", "List sind networks", runGetNetworks},
		{"realms", "List all realms", runGetRealms},
		{"volumes", "List sind volumes", runGetVolumes},
		{"dns", "List mesh DNS records", runGetDNS},
		{"ssh-config", "Show SSH config path for the current realm", runGetSSHConfig},
		{"mesh", "Show mesh infrastructure info", runGetMesh},
		{"coredns-config", "Output a CoreDNS server block that forwards the realm's zone to the mesh DNS server", runGetCoreDNSConfig},
		{"monitoring", "Show the realm's monitoring stack", runGetMonitoring},
		{"ssh-private-key", "Output SSH private key", getSSHKey("private_key", (*mesh.Manager).GetSSHPrivateKey)},
		{"ssh-public-key", "Output SSH public key", getSSHKey("public_key", (*mesh.Manager).GetSSHPublicKey)},
		{"ssh-known-hosts", "Output SSH known_hosts", getSSHKey("known_hosts", (*mesh.Manager).GetSSHKnownHosts)},
	} {
		cmd.AddCommand(&cobra.Command{Use: c.use, Short: c.short, Args: noArgs, RunE: c.run})
	}

	return cmd
}

// fetchFunc fetches what a get subcommand prints, from the realm's resources.
type fetchFunc[T any] func(ctx context.Context, client *docker.Client, realm string) (T, error)

// runGet runs a get subcommand: it checks --output, resolves the realm and
// fetches the resource, which it prints as JSON with -o json and with human
// otherwise.
func runGet[T any](cmd *cobra.Command, fetch fetchFunc[T], human func(cmd *cobra.Command, v T, realm string) error) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	v, err := fetch(cmd.Context(), clientFrom(cmd.Context()), realm)
	if err != nil {
		return err
	}
	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), v)
	}
	return human(cmd, v, realm)
}

// fromMesh adapts a getter of the realm's mesh.Manager, such as
// (*mesh.Manager).GetInfo, to runGet.
func fromMesh[T any](get func(*mesh.Manager, context.Context) (T, error)) fetchFunc[T] {
	return func(ctx context.Context, client *docker.Client, realm string) (T, error) {
		return get(meshMgrFrom(ctx, client, realm), ctx)
	}
}

func newGetNodesCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "nodes [CLUSTER]",
		Short:             "List the nodes of a cluster, or of every cluster of the realm",
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

func runGetClusters(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, cluster.GetClusters, func(cmd *cobra.Command, clusters []*cluster.Summary, realm string) error {
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
		if err := w.Flush(); err != nil {
			return err
		}
		noteNone(cmd, len(clusters), "clusters", realm)
		return nil
	})
}

// runGetRealms lists every realm, so unlike runGet it resolves none: a
// SIND_REALM that names no valid realm does not stop it.
func runGetRealms(cmd *cobra.Command, _ []string) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	realms, err := cluster.GetRealms(cmd.Context(), clientFrom(cmd.Context()))
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
	if err := w.Flush(); err != nil {
		return err
	}
	noteNone(cmd, len(realms), "realms", "")
	return nil
}

func newGetNodeCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "node NODE",
		Short:             "Show node health status",
		Long:              "Show node health status.\n\n" + nodeHelp,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNodeName,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGetNode(cmd, args[0])
		},
	}
}

func runGetNode(cmd *cobra.Command, arg string) error {
	// A bad -o is reported before a bad node name.
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	target, err := parseOneNode("get node", arg)
	if err != nil {
		return err
	}
	shortName, clusterName := target.ShortName, target.Cluster
	getNode := func(ctx context.Context, client *docker.Client, realm string) (*cluster.NodeDetail, error) {
		containerName := cluster.ContainerName(realm, clusterName, shortName)
		info, err := client.InspectContainer(ctx, containerName)
		if docker.IsNotFound(err) {
			// Tell a missing cluster from a missing node, as logs and ssh do.
			if err := cluster.CheckNode(ctx, client, realm, clusterName, shortName); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("node %q not found in cluster %q", shortName, clusterName)
		}
		if err != nil {
			return nil, fmt.Errorf("inspecting container: %w", err)
		}
		role := config.Role(info.Labels[cluster.LabelRole])
		health, err := cluster.GetNodeHealth(ctx, client, info, role, realm, clusterName)
		if err != nil {
			return nil, err
		}
		return &cluster.NodeDetail{
			Container: string(containerName),
			Cluster:   clusterName,
			Role:      role,
			Managed:   cluster.IsManaged(info.Labels),
			FQDN:      cluster.DNSName(shortName, clusterName, realm),
			IP:        health.IP,
			Status:    health.State,
			Services:  health.Services,
			HA:        health.HA,
		}, nil
	}
	return runGet(cmd, getNode, func(cmd *cobra.Command, n *cluster.NodeDetail, _ string) error {
		out := cmd.OutOrStdout()
		w := newTabWriter(out)
		if n.HA != nil {
			_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tHA\tFQDN\tIP\tSTATUS")
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", n.Container, cell(n.Role), formatHA(n.HA), n.FQDN, cell(n.IP), cell(n.Status))
		} else {
			_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tFQDN\tIP\tSTATUS")
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", n.Container, cell(n.Role), n.FQDN, cell(n.IP), cell(n.Status))
		}
		if err := w.Flush(); err != nil {
			return err
		}

		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "SERVICES")
		w = newTabWriter(out)
		_, _ = fmt.Fprintln(w, "NAME\tSTATUS")
		for _, name := range slices.Sorted(maps.Keys(n.Services)) {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", name, checkmark(n.Services[name]))
		}
		return w.Flush()
	})
}

func runGetAllNodes(cmd *cobra.Command) error {
	return runGet(cmd, cluster.GetAllNodes, func(cmd *cobra.Command, nodes []*cluster.NodeSummary, realm string) error {
		w := newTabWriter(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(w, "CONTAINER\tCLUSTER\tROLE\tFQDN\tIP\tSTATUS")
		for _, n := range nodes {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", cell(n.Container), cell(n.Cluster), cell(n.Role), cell(n.FQDN), cell(n.IP), cell(n.State))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		noteNone(cmd, len(nodes), "nodes", realm)
		return nil
	})
}

func runGetNodes(cmd *cobra.Command, name string) error {
	getNodes := func(ctx context.Context, client *docker.Client, realm string) ([]*cluster.NodeSummary, error) {
		return cluster.GetNodes(ctx, client, realm, name)
	}
	return runGet(cmd, getNodes, func(cmd *cobra.Command, nodes []*cluster.NodeSummary, _ string) error {
		w := newTabWriter(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(w, "CONTAINER\tROLE\tFQDN\tIP\tSTATUS")
		for _, n := range nodes {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", cell(n.Container), cell(n.Role), cell(n.FQDN), cell(n.IP), cell(n.State))
		}
		return w.Flush()
	})
}

func runGetNetworks(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, cluster.GetNetworks, func(cmd *cobra.Command, networks []*cluster.NetworkSummary, _ string) error {
		w := newTabWriter(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(w, "NAME\tDRIVER\tSUBNET\tGATEWAY")
		for _, n := range networks {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cell(n.Name), cell(n.Driver), cell(n.Subnet), cell(n.Gateway))
		}
		return w.Flush()
	})
}

func runGetVolumes(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, cluster.GetVolumes, func(cmd *cobra.Command, volumes []*cluster.VolumeSummary, _ string) error {
		w := newTabWriter(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(w, "NAME\tDRIVER")
		for _, v := range volumes {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", cell(v.Name), cell(v.Driver))
		}
		return w.Flush()
	})
}

func runGetDNS(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, fromMesh((*mesh.Manager).GetDNSRecords), func(cmd *cobra.Command, records []mesh.DNSRecord, _ string) error {
		w := newTabWriter(cmd.OutOrStdout())
		_, _ = fmt.Fprintln(w, "HOSTNAME\tIP")
		for _, r := range records {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", cell(r.Hostname), cell(r.IP))
		}
		return w.Flush()
	})
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
otherwise.

` + defaultClusterHelp("CLUSTER"),
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGetAuthKey(cmd, clusterArg(args))
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
	// A bad -o is reported before a bad --type.
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	authType, _ := cmd.Flags().GetString("type")
	if authType != "" && !slices.Contains(cluster.AuthTypes, cluster.AuthType(authType)) {
		return usagef("invalid --type value %q: must be %s", authType, strings.Join(authTypeNames(), ", "))
	}
	type output struct {
		Type cluster.AuthType `json:"type"`
		Key  string           `json:"key"`
	}
	getKey := func(ctx context.Context, client *docker.Client, realm string) (output, error) {
		key, err := cluster.GetAuthKey(ctx, client, realm, name, cluster.AuthType(authType))
		if err != nil {
			return output{}, err
		}
		return output{Type: key.Type, Key: base64.StdEncoding.EncodeToString(key.Key)}, nil
	}
	return runGet(cmd, getKey, func(cmd *cobra.Command, key output, _ string) error {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), key.Key)
		return nil
	})
}

func runGetSSHConfig(cmd *cobra.Command, _ []string) error {
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	realm, err := realmFromFlag(cmd)
	if err != nil {
		return err
	}
	dir, err := state.RealmDir(realm)
	if err != nil {
		return err
	}
	p := filepath.Join(dir, "ssh_config")
	// The path is also right before the file exists, for an
	// Include; a script that reads the file needs to know.
	exists, _ := afero.Exists(fsFrom(cmd.Context()), p)
	if isJSONOutput(cmd) {
		return writeJSON(cmd.OutOrStdout(), struct {
			Path   string `json:"path"`
			Exists bool   `json:"exists"`
		}{Path: p, Exists: exists})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), p)
	if !exists {
		cmd.PrintErrln("Note: the file does not exist yet; sind writes it when a cluster is created in this realm")
	}
	return nil
}

func runGetMesh(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, fromMesh((*mesh.Manager).GetInfo), func(cmd *cobra.Command, info *mesh.Info, _ string) error {
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
	})
}

func runGetCoreDNSConfig(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, fromMesh((*mesh.Manager).GetCoreDNSStub), func(cmd *cobra.Command, stub *mesh.CoreDNSStub, _ string) error {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), stub.Corefile)
		return nil
	})
}

func runGetMonitoring(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, cluster.GetMonitoring, func(cmd *cobra.Command, m *cluster.Monitoring, _ string) error {
		out := cmd.OutOrStdout()
		w := newTabWriter(out)
		_, _ = fmt.Fprintln(w, "SERVICE\tCONTAINER\tADDRESS\tSTATUS")
		for _, s := range m.Services {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Name, cell(s.Container), cell(cmp.Or(s.URL, "-")), formatServiceState(s))
		}
		if err := w.Flush(); err != nil {
			return err
		}

		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "CLUSTERS")
		w = newTabWriter(out)
		_, _ = fmt.Fprintln(w, "NAME\tNODES\tSLURM")
		for _, c := range m.Clusters {
			_, _ = fmt.Fprintf(w, "%s\t%d\t%s\n", cell(c.Name), c.Nodes, formatSlurmVersion(c.SlurmVersion))
		}
		return w.Flush()
	})
}

// formatServiceState renders a monitoring service's state in words, as the
// CLI guidelines keep ✓/✗ to get cluster, get node and doctor: the
// container's state, "not ready" when it runs but its readiness check
// fails, "unchecked" when it runs but Grafana, which runs the check, does
// not, and "missing" when the container does not exist.
func formatServiceState(s cluster.MonitoringService) string {
	switch {
	case s.State == "":
		return "missing"
	case s.State == docker.StateRunning && !s.Checked:
		return "unchecked"
	case s.State == docker.StateRunning && !s.Ready:
		return "not ready"
	}
	return cell(s.State)
}

func newGetClusterCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "cluster [NAME]",
		Short:             "Show cluster health status",
		Long:              "Show cluster health status.\n\n" + defaultClusterHelp("NAME"),
		Args:              optionalCluster,
		ValidArgsFunction: completeClusterNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGetCluster(cmd, clusterArg(args))
		},
	}
}

func runGetCluster(cmd *cobra.Command, name string) error {
	getStatus := func(ctx context.Context, client *docker.Client, realm string) (*cluster.Status, error) {
		return cluster.GetStatus(ctx, client, realm, name)
	}
	return runGet(cmd, getStatus, printStatus)
}

// printStatus prints get cluster's tables.
func printStatus(cmd *cobra.Command, status *cluster.Status, realm string) error {
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
	for i, ready := range net.StackHealth() {
		if ready != nil {
			s := observability.Services[i]
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", s, observability.ContainerName(realm, s), checkmark(*ready))
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Mounts table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "MOUNTS")
	w = newTabWriter(out)
	_, _ = fmt.Fprintln(w, "MOUNT\tSOURCE\tTYPE\tSTATUS")
	for _, m := range status.Mounts {
		source := m.Source
		if m.Subpath != "" {
			source += ":" + m.Subpath + "/<node>"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cell(m.Path), cell(source), cell(m.Type), checkmark(m.OK))
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

// cell makes text that came from a container, a label or docker safe to
// print as a table cell. An image can set labels and print what sind
// reads, so such text may hold control sequences that retitle the
// terminal or write its clipboard; they are shown escaped (\x1b), as on
// the final error line. Tab and newline are shown escaped too (\t, \n): in
// a table cell they would start a column or a row of their own.
func cell[S ~string](s S) string {
	return termtext.Escape(string(s))
}

func checkmark(ok bool) string {
	if ok {
		return "\u2713"
	}
	return "\u2717"
}

// noteNone says on stderr that a table listed nothing, so that a lone header
// is not mistaken for a failure. Stdout stays the table, for scripts.
func noteNone(cmd *cobra.Command, n int, what, realm string) {
	if n > 0 {
		return
	}
	if realm == "" {
		cmd.PrintErrf("No %s found.\n", what)
		return
	}
	cmd.PrintErrf("No %s found in realm %q.\n", what, realm)
}

func formatServices(services cluster.ServiceHealth) string {
	parts := make([]string, 0, len(services))
	for _, name := range slices.Sorted(maps.Keys(services)) {
		parts = append(parts, string(name)+" "+checkmark(services[name]))
	}
	return strings.Join(parts, " ")
}

// getSSHKey returns the run function of a get subcommand that prints a
// file of the realm's SSH setup, under jsonKey with -o json.
func getSSHKey(jsonKey string, get func(*mesh.Manager, context.Context) (string, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		getKey := func(ctx context.Context, client *docker.Client, realm string) (map[string]string, error) {
			content, err := get(meshMgrFrom(ctx, client, realm), ctx)
			return map[string]string{jsonKey: content}, err
		}
		return runGet(cmd, getKey, func(cmd *cobra.Command, v map[string]string, _ string) error {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), v[jsonKey])
			return nil
		})
	}
}

func newTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
}
