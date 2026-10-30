// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/go-clikit/cliout"
	"github.com/GSI-HPC/go-clikit/termtext"
	"github.com/GSI-HPC/sind/internal/envvar"
	"github.com/GSI-HPC/sind/pkg/cluster"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/observability"
	"github.com/spf13/cobra"
)

func newGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display resources",
	}

	cmd.PersistentFlags().StringP("output", "o", "", outputUsage)
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
		return cliout.WriteJSON(cmd.OutOrStdout(), v)
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
		Use:   "nodes [CLUSTER]",
		Short: "List the nodes of a cluster, or of every cluster of the realm",
		Long: `List the nodes of a cluster, or of every cluster of the realm.

Without CLUSTER, it lists the nodes of every cluster of the realm, whatever
` + envvar.Cluster + ` names.`,
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
		t := newTable("NAME", "NODES (S/C/D/A/W)", "SLURM", "STATUS")
		for _, c := range clusters {
			t.Add(cell(c.Name),
				fmt.Sprintf("%d (%d/%d/%d/%d/%d)", c.NodeCount, c.Submitters, c.Controllers, c.DBs, c.APIs, c.Workers),
				formatSlurmVersion(c.SlurmVersion),
				string(c.State),
			)
		}
		if err := writeTable(cmd.OutOrStdout(), t); err != nil {
			return err
		}
		noteNone(cmd, len(clusters), "clusters", inRealm(realm))
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
		return cliout.WriteJSON(cmd.OutOrStdout(), realms)
	}
	t := newTable("NAME", "CLUSTERS")
	for _, r := range realms {
		t.Add(cell(r.Name), strconv.Itoa(r.Clusters))
	}
	if err := writeTable(cmd.OutOrStdout(), t); err != nil {
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
		return cluster.GetNode(ctx, client, realm, clusterName, shortName)
	}
	return runGet(cmd, getNode, func(cmd *cobra.Command, n *cluster.NodeDetail, _ string) error {
		out := cmd.OutOrStdout()
		var t *cliout.Table
		if n.HA != nil {
			t = newTable("CONTAINER", "ROLE", "HA", "FQDN", "IP", "STATUS")
			t.Add(n.Container, cell(n.Role), formatHA(n.HA), n.FQDN, cell(n.IP), cell(n.Status))
		} else {
			t = newTable("CONTAINER", "ROLE", "FQDN", "IP", "STATUS")
			t.Add(n.Container, cell(n.Role), n.FQDN, cell(n.IP), cell(n.Status))
		}
		if err := writeTable(out, t); err != nil {
			return err
		}

		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "SERVICES")
		t = newTable("NAME", "STATUS")
		for _, name := range slices.Sorted(maps.Keys(n.Services)) {
			t.Add(string(name), checkmark(n.Services[name]))
		}
		return writeTable(out, t)
	})
}

func runGetAllNodes(cmd *cobra.Command) error {
	return runGet(cmd, cluster.GetAllNodes, func(cmd *cobra.Command, nodes []*cluster.NodeSummary, realm string) error {
		t := newTable("CONTAINER", "CLUSTER", "ROLE", "FQDN", "IP", "STATUS")
		for _, n := range nodes {
			t.Add(cell(n.Container), cell(n.Cluster), cell(n.Role), cell(n.FQDN), cell(n.IP), cell(n.State))
		}
		if err := writeTable(cmd.OutOrStdout(), t); err != nil {
			return err
		}
		noteNone(cmd, len(nodes), "nodes", inRealm(realm))
		return nil
	})
}

func runGetNodes(cmd *cobra.Command, name string) error {
	getNodes := func(ctx context.Context, client *docker.Client, realm string) ([]*cluster.NodeSummary, error) {
		return cluster.GetNodes(ctx, client, realm, name)
	}
	return runGet(cmd, getNodes, func(cmd *cobra.Command, nodes []*cluster.NodeSummary, _ string) error {
		t := newTable("CONTAINER", "ROLE", "FQDN", "IP", "STATUS")
		for _, n := range nodes {
			t.Add(cell(n.Container), cell(n.Role), cell(n.FQDN), cell(n.IP), cell(n.State))
		}
		return writeTable(cmd.OutOrStdout(), t)
	})
}

func runGetNetworks(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, cluster.GetNetworks, func(cmd *cobra.Command, networks []*cluster.NetworkSummary, realm string) error {
		t := newTable("NAME", "DRIVER", "SUBNET", "GATEWAY")
		for _, n := range networks {
			t.Add(cell(n.Name), cell(n.Driver), cell(n.Subnet), cell(n.Gateway))
		}
		if err := writeTable(cmd.OutOrStdout(), t); err != nil {
			return err
		}
		noteNone(cmd, len(networks), "networks", inRealm(realm))
		return nil
	})
}

func runGetVolumes(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, cluster.GetVolumes, func(cmd *cobra.Command, volumes []*cluster.VolumeSummary, realm string) error {
		t := newTable("NAME", "DRIVER")
		for _, v := range volumes {
			t.Add(cell(v.Name), cell(v.Driver))
		}
		if err := writeTable(cmd.OutOrStdout(), t); err != nil {
			return err
		}
		noteNone(cmd, len(volumes), "volumes", inRealm(realm))
		return nil
	})
}

func runGetDNS(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, fromMesh((*mesh.Manager).GetDNSRecords), func(cmd *cobra.Command, records []mesh.DNSRecord, realm string) error {
		t := newTable("HOSTNAME", "IP")
		for _, r := range records {
			t.Add(cell(r.Hostname), cell(r.IP))
		}
		if err := writeTable(cmd.OutOrStdout(), t); err != nil {
			return err
		}
		noteNone(cmd, len(records), "DNS records", inRealm(realm))
		return nil
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
			name, err := clusterArg(args)
			if err != nil {
				return err
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
	// A bad -o is reported before a bad --type.
	if err := validateOutputFlag(cmd); err != nil {
		return err
	}
	authType, _ := cmd.Flags().GetString("type")
	if authType != "" && !slices.Contains(cluster.AuthTypes, cluster.AuthType(authType)) {
		return invalidFlagValue("type", authType, authTypeNames()...)
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
	conf, err := cluster.GetSSHConfig(fsFrom(cmd.Context()), realm)
	if err != nil {
		return err
	}
	if isJSONOutput(cmd) {
		return cliout.WriteJSON(cmd.OutOrStdout(), conf)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), conf.Path)
	if !conf.Exists {
		cmd.PrintErrln("Note: the file does not exist yet; sind writes it when a cluster is created in this realm")
	}
	return nil
}

func runGetMesh(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, fromMesh((*mesh.Manager).GetInfo), func(cmd *cobra.Command, info *mesh.Info, _ string) error {
		t := newTable("PROPERTY", "VALUE")
		for _, row := range [][2]string{
			{"network", info.Network},
			{"dns-container", info.DNSContainer},
			{"dns-ip", info.DNSIP},
			{"dns-zone", info.DNSZone},
			{"dns-image", info.DNSImage},
			{"ssh-container", info.SSHContainer},
			{"ssh-volume", info.SSHVolume},
			{"ssh-image", info.SSHImage},
		} {
			t.Add(row[0], cell(row[1]))
		}
		return writeTable(cmd.OutOrStdout(), t)
	})
}

func runGetCoreDNSConfig(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, fromMesh((*mesh.Manager).GetCoreDNSStub), func(cmd *cobra.Command, stub *mesh.CoreDNSStub, _ string) error {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), stub.Corefile)
		return nil
	})
}

func runGetMonitoring(cmd *cobra.Command, _ []string) error {
	return runGet(cmd, cluster.GetMonitoring, func(cmd *cobra.Command, m *cluster.Monitoring, realm string) error {
		out := cmd.OutOrStdout()
		t := newTable("SERVICE", "CONTAINER", "ADDRESS", "STATUS")
		for _, s := range m.Services {
			t.Add(string(s.Name), cell(s.Container), cell(s.URL), formatServiceState(s))
		}
		if err := writeTable(out, t); err != nil {
			return err
		}

		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "CLUSTERS")
		t = newTable("NAME", "NODES", "SLURM")
		for _, c := range m.Clusters {
			t.Add(cell(c.Name), strconv.Itoa(c.Nodes), formatSlurmVersion(c.SlurmVersion))
		}
		if err := writeTable(out, t); err != nil {
			return err
		}
		noteNone(cmd, len(m.Clusters), "monitored clusters", inRealm(realm))
		return nil
	})
}

// formatServiceState renders a monitoring service's state in words, as the
// CLI guidelines keep ✓/✗ to get cluster, get node and doctor: the
// container's state, "not ready" when it runs but its readiness check
// fails, "unchecked" when it runs but Grafana, which runs the check, does
// not, and "missing" when the container does not exist.
func formatServiceState(s cluster.MonitoringService) string {
	switch {
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
			name, err := clusterArg(args)
			if err != nil {
				return err
			}
			return runGetCluster(cmd, name)
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
	t := newTable("CLUSTER", "SLURM", "STATUS (R/S/P/T)")
	t.Add(cell(status.Name), formatSlurmVersion(status.SlurmVersion), formatState(status))
	if err := writeTable(out, t); err != nil {
		return err
	}

	// Networks table
	net := status.Network
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "NETWORKS")
	t = newTable("NAME", "DRIVER", "SUBNET", "GATEWAY", "STATUS")
	t.Add(cell(net.MeshName), cell(net.MeshDriver), cell(net.MeshSubnet), cell(net.MeshGateway), checkmark(net.Mesh))
	t.Add(cell(net.ClusterName), cell(net.ClusterDriver), cell(net.ClusterSubnet), cell(net.ClusterGateway), checkmark(net.Cluster))
	if err := writeTable(out, t); err != nil {
		return err
	}

	// Mesh services table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "MESH SERVICES")
	t = newTable("NAME", "CONTAINER", "STATUS")
	t.Add("dns", cell(net.DNSName), checkmark(net.DNS))
	t.Add("ssh", cell(net.SSHName), checkmark(net.SSH))
	for i, ready := range net.StackHealth() {
		if ready != nil {
			s := observability.Services[i]
			t.Add(string(s), string(observability.ContainerName(realm, s)), checkmark(*ready))
		}
	}
	if err := writeTable(out, t); err != nil {
		return err
	}

	// Mounts table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "MOUNTS")
	t = newTable("MOUNT", "SOURCE", "TYPE", "STATUS")
	for _, m := range status.Mounts {
		source := m.Source
		if m.Subpath != "" {
			source += ":" + m.Subpath + "/<node>"
		}
		t.Add(cell(m.Path), cell(source), cell(m.Type), checkmark(m.OK))
	}
	if err := writeTable(out, t); err != nil {
		return err
	}

	// Nodes table
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "NODES")
	withHA := slices.ContainsFunc(status.Nodes, func(n *cluster.NodeStatus) bool { return n.Health.HA != nil })
	if withHA {
		t = newTable("NAME", "ROLE", "HA", "IP", "STATUS", "SERVICES")
	} else {
		t = newTable("NAME", "ROLE", "IP", "STATUS", "SERVICES")
	}
	for _, n := range status.Nodes {
		h := n.Health
		if withHA {
			t.Add(cell(n.Name), cell(n.Role), formatHA(h.HA), cell(h.IP), cell(h.State), formatServices(h.Services))
			continue
		}
		t.Add(cell(n.Name), cell(n.Role), cell(h.IP), cell(h.State), formatServices(h.Services))
	}
	return writeTable(out, t)
}

// formatHA renders a controller's pair position, marking the controller in
// control with "*". Nodes outside the pair render "-".
func formatHA(ha *cluster.HAStatus) string {
	if ha == nil {
		return "-"
	}
	if ha.InControl {
		return ha.Position + "*"
	}
	return ha.Position
}

func formatState(status *cluster.Status) string {
	running, stopped, paused := status.NodeCounts()
	total := running + stopped + paused
	return fmt.Sprintf("%s (%d/%d/%d/%d)", status.State, running, stopped, paused, total)
}

// formatSlurmVersion renders a cluster's Slurm version, "-" when sind does
// not know it (unmanaged clusters). The version comes from a label, so it
// is escaped like any other cell.
func formatSlurmVersion(version string) string {
	return cell(version)
}

// cell makes text that came from a container, a label or docker safe to
// print as a table cell. An image can set labels and print what sind
// reads, so such text may hold control sequences that retitle the
// terminal or write its clipboard; they are shown escaped (\x1b), as on
// the final error line. Tab and newline are shown escaped too (\t, \n): in
// a table cell they would start a column or a row of their own. An empty
// cell is "-", so that every row has all its columns for awk and cut, as
// a stopped node has no IP.
func cell[S ~string](s S) string {
	if s == "" {
		return "-"
	}
	return termtext.Escape(string(s))
}

func checkmark(ok bool) string {
	if ok {
		return "\u2713"
	}
	return "\u2717"
}

// noteNone says on stderr that a table listed nothing, so that a lone header
// is not mistaken for a failure, and where sind looked, unless where is
// empty: inRealm, or the file it read. Stdout stays the table, for scripts.
func noteNone(cmd *cobra.Command, n int, what, where string) {
	if n > 0 {
		return
	}
	if where == "" {
		cmd.PrintErrf("No %s found.\n", what)
		return
	}
	cmd.PrintErrf("No %s found in %s.\n", what, where)
}

// inRealm names a realm for noteNone.
func inRealm(realm string) string {
	return fmt.Sprintf("realm %q", realm)
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

// newTable returns a table with the columns named, every one shown.
func newTable(names ...string) *cliout.Table {
	t := &cliout.Table{Columns: make([]cliout.Column, len(names))}
	for i, name := range names {
		t.Columns[i].Name = name
	}
	return t
}

// writeTable writes t to out, without the columns that only -o wide shows,
// which sind has none of.
func writeTable(out io.Writer, t *cliout.Table) error {
	return t.Write(out, false)
}
