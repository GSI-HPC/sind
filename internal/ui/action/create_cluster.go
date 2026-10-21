// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

func init() {
	register(Action{ID: "create-cluster", Build: buildCreateCluster, Cluster: func(f Form) string { return strings.TrimSpace(f.Get("name")) }})
}

// Waits are the values of the field "wait" of the New cluster dialog, as
// sind create cluster --wait takes them.
var Waits = []string{"1m", "2m", "5m", "10m", "0"}

// DefaultWait is the --wait of sind create cluster, which the command line
// leaves out.
const DefaultWait = "5m"

// Roles of the New cluster dialog that are a switch: zero or one node.
var switchRoles = []struct{ field, label string }{
	{"backup", "controller-backup"}, {"db", "db"}, {"api", "api"}, {"submitter", "submitter"},
}

// maxWorkers is the most workers the New cluster dialog makes.
const maxWorkers = 64

// imagePattern is what the image reference of a form may look like: the
// characters of a registry, repository, tag and digest.
var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// ConfigPath returns where sind ui saves the config of the cluster name.
func ConfigPath(dir, name string) string { return filepath.Join(dir, name+".yaml") }

func buildCreateCluster(f Form, c Context) (Command, error) {
	realm, err := checkRealm(f)
	if err != nil {
		return Command{}, err
	}
	name := strings.TrimSpace(f.Get("name"))
	if err := config.CheckName("cluster", name); err != nil {
		return Command{}, fmt.Errorf("name: %w", err)
	}
	if _, ok := c.Snap.Cluster(realm, name); ok {
		return Command{}, fmt.Errorf("name: realm %s has a cluster %s already; pick another name, or delete that cluster first", realm, name)
	}
	cfg := config.Cluster{Kind: "Cluster", Name: name}
	if realm != mesh.DefaultRealm {
		cfg.Realm = realm
	}
	if image := strings.TrimSpace(f.Get("image")); image != "" {
		if !imagePattern.MatchString(image) {
			return Command{}, errors.New("image: write an image reference such as ghcr.io/gsi-hpc/sind-node:latest")
		}
		cfg.Defaults.Image = image
	}
	if cfg.Defaults.CPUs, err = number(f, "cpus", "CPUs per node", 1, 64, 1); err != nil {
		return Command{}, err
	}
	if cfg.Defaults.Memory = strings.TrimSpace(f.Get("memory")); cfg.Defaults.Memory != "" {
		if err := config.CheckMemory("memory", cfg.Defaults.Memory); err != nil {
			return Command{}, fmt.Errorf("memory per node: %w", err)
		}
	}
	nodes, planned, err := clusterNodes(f)
	if err != nil {
		return Command{}, err
	}
	cfg.Nodes = nodes
	if cfg.Users, err = clusterUsers(f); err != nil {
		return Command{}, err
	}
	cfg.Monitoring = f.Get("monitoring") != ""
	wait := strings.TrimSpace(f.Get("wait"))
	switch {
	case wait == "":
		wait = DefaultWait
	case !slices.Contains(Waits, wait):
		return Command{}, fmt.Errorf("wait for ready: pick one of %s", strings.Join(Waits, ", "))
	}
	data, dir, home, err := dataStorage(f, c)
	if err != nil {
		return Command{}, err
	}
	if c.ConfigDir == "" {
		return Command{}, errors.New("sind ui has no state directory to save the config in; set XDG_STATE_HOME or HOME")
	}

	content, warns, err := renderConfig(cfg)
	if err != nil {
		return Command{}, err
	}
	argv := sindArgv(realm, "create", "cluster", "--config", ConfigPath(c.ConfigDir, name), "--data", data)
	if f.Get("pull") != "" {
		argv = append(argv, "--pull")
	}
	if wait != DefaultWait {
		argv = append(argv, "--wait", wait)
	}
	cmd := NewCommand(argv...)
	cmd.Config = &File{Path: ConfigPath(c.ConfigDir, name), Content: content}
	cmd.Nodes = planned
	cmd.Summary = fmt.Sprintf("%d containers, %d CPUs, %s each.", len(planned), cfg.Defaults.CPUs*len(planned), memoryLabel(cfg.Defaults.Memory))
	cmd.Warns = warns
	if home {
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("%s is your home directory. Jobs write their output files there as root. Pick a scratch directory such as %s.",
			dir, path.Join(dir, "sind", name)))
	}
	cmd.Notes = append(cmd.Notes, fmt.Sprintf("sind ui saves the config to %s (mode 0600) and keeps it. Copy gives the command; the config is next to it.", cmd.Config.Path))
	if cfg.Monitoring || slices.ContainsFunc(cfg.Nodes, func(n config.Node) bool { return n.Role == config.RoleAPI }) {
		cmd.Notes = append(cmd.Notes, "The api node and monitoring need Slurm 26.05 in the image. sind checks the image when it runs, and stops with an error if it is older.")
	}
	if s := c.Snap.Monitorings[realm]; cfg.Monitoring && s != nil {
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("Realm %s has a monitoring stack. sind adds the new nodes to it.", realm))
	}
	return cmd, nil
}

// memoryLabel is the memory of a node for the summary.
func memoryLabel(m string) string {
	if m == "" {
		return config.DefaultMemory
	}
	return m
}

// clusterNodes reads the roles of the New cluster dialog: the nodes of the
// config, and the same nodes one by one for the preview.
func clusterNodes(f Form) ([]config.Node, []PlannedNode, error) {
	workers, err := number(f, "workers", "Workers", 1, maxWorkers, -1)
	if err != nil {
		return nil, nil, err
	}
	nodes := []config.Node{{Role: config.RoleController}}
	planned := []PlannedNode{{"controller", "controller"}}
	for _, r := range switchRoles {
		n, err := number(f, r.field, r.label, 0, 1, 0)
		if err != nil {
			return nil, nil, err
		}
		if n == 0 {
			continue
		}
		if r.field == "backup" {
			nodes[0].BackupController = true
			planned = append(planned, PlannedNode{"controller-backup", "controller-backup"})
			continue
		}
		nodes = append(nodes, config.Node{Role: config.Role(r.field)})
		planned = append(planned, PlannedNode{r.field, r.field})
	}
	nodes = append(nodes, config.Node{Role: config.RoleWorker, Count: workers})
	for i := range workers {
		planned = append(planned, PlannedNode{"worker-" + strconv.Itoa(i), "worker"})
	}
	return nodes, planned, nil
}

// clusterUsers reads the field "users": names separated by commas or
// spaces, each a valid user name, without duplicates.
func clusterUsers(f Form) ([]config.User, error) {
	var users []config.User
	var seen []string
	for _, u := range strings.FieldsFunc(f.Get("users"), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if err := config.CheckUserName(u); err != nil {
			return nil, fmt.Errorf("users: %w", err)
		}
		if slices.Contains(seen, u) {
			continue
		}
		seen = append(seen, u)
		users = append(users, config.User{Name: u})
	}
	return users, nil
}

// dataStorage reads the fields "dtype" and "dir" into the value of --data,
// and says whether the directory is the user's home.
func dataStorage(f Form, c Context) (data, dir string, home bool, err error) {
	switch dtype := f.Get("dtype"); dtype {
	case "volume":
		return "volume", "", false, nil
	case "", "hostPath":
	default:
		return "", "", false, fmt.Errorf("data: pick a directory or a Docker volume, not %q", dtype)
	}
	dir = strings.TrimSpace(f.Get("dir"))
	switch {
	case dir == "":
		return "", "", false, errors.New("directory: say which directory")
	case dir == "~" || strings.HasPrefix(dir, "~/"):
		if c.Home == "" {
			return "", "", false, errors.New("directory: sind ui does not know your home directory; write the path in full")
		}
		dir = path.Join(c.Home, dir[1:])
	case !path.IsAbs(dir):
		return "", "", false, errors.New("directory: write the path in full, starting with / or ~")
	}
	if !plainText(dir) {
		return "", "", false, errors.New("directory: the path holds control characters")
	}
	dir = path.Clean(dir)
	return dir, dir, c.Home != "" && dir == path.Clean(c.Home), nil
}

// parseConfig is config.Parse; tests replace it.
var parseConfig = config.Parse

// renderConfig writes the config as YAML and checks it as sind create
// cluster will: it parses it, applies the defaults and validates it. The
// warnings are those of sind.
func renderConfig(cfg config.Cluster) (content string, warns []string, err error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	// A struct of plain fields and a buffer: nothing to fail.
	_ = enc.Encode(cfg)
	_ = enc.Close()
	data := buf.Bytes()
	parsed, err := parseConfig(data)
	if err != nil {
		return "", nil, fmt.Errorf("config: %w", err)
	}
	parsed.ApplyDefaults()
	if err := parsed.Validate(); err != nil {
		return "", nil, fmt.Errorf("config: %w", err)
	}
	return string(data), parsed.Warnings(), nil
}
