// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/GSI-HPC/sind/internal/ui/store"
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

// Roles of the New cluster dialog that are a checkbox: zero or one node.
var switchRoles = []struct{ field, label string }{
	{"backup", "controller-backup"}, {"db", "db"}, {"api", "api"}, {"submitter", "submitter"},
}

// maxWorkers is the most workers the New cluster dialog makes.
const maxWorkers = 64

// Defaults of the New cluster dialog that the plan falls back to while
// their field is wrong.
const (
	defaultCPUs    = 1
	defaultWorkers = 1
)

// imagePattern is what the image reference of a form may look like: the
// characters of a registry, repository, tag and digest.
var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// ConfigPath returns where sind ui saves the config of the cluster name.
func ConfigPath(dir, name string) string { return filepath.Join(dir, name+".yaml") }

// FreeName returns base, or else base-2, base-3 and so on: the first name
// of a cluster that realm does not have.
func FreeName(s store.Snapshot, realm, base string) string {
	name := base
	for i := 2; ; i++ {
		if _, taken := s.Cluster(realm, name); !taken {
			return name
		}
		name = base + "-" + strconv.Itoa(i)
	}
}

// problems collects the problems of a form.
type problems []Problem

// add records that field is wrong; def says that the plan uses its
// default.
func (ps *problems) add(field, label string, def bool, err error) {
	*ps = append(*ps, Problem{Field: field, Label: label, Text: err.Error(), Default: def})
}

// has reports whether field has a problem.
func (ps problems) has(field string) bool {
	return slices.ContainsFunc(ps, func(p Problem) bool { return p.Field == field })
}

// buildCreateCluster makes the command of the New cluster dialog. A wrong
// field is a problem of the command, not an error: the plan takes the
// field's default, or the name as it is, so that the dialog can show the
// rest of it.
func buildCreateCluster(f Form, c Context) (Command, error) {
	realm, err := checkRealm(f)
	if err != nil {
		return Command{}, err
	}
	if c.ConfigDir == "" {
		return Command{}, errors.New("sind ui has no state directory to save the config in; set XDG_STATE_HOME or HOME")
	}
	var ps problems
	cfg := config.Cluster{Kind: "Cluster", Name: strings.TrimSpace(f.Get("name"))}
	if realm != mesh.DefaultRealm {
		cfg.Realm = realm
	}
	if image := strings.TrimSpace(f.Get("image")); image != "" {
		if imagePattern.MatchString(image) {
			cfg.Defaults.Image = image
		} else {
			ps.add("image", "Image", true, errors.New("write an image reference such as ghcr.io/gsi-hpc/sind-node:latest"))
		}
	}
	if cfg.Defaults.CPUs, err = number(f, "cpus", "CPUs per node", 1, 64, defaultCPUs); err != nil {
		ps.add("cpus", "CPUs per node", true, errors.New("write a number from 1 to 64"))
		cfg.Defaults.CPUs = defaultCPUs
	}
	if memory := strings.TrimSpace(f.Get("memory")); memory != "" {
		if err := config.CheckMemory("memory", memory); err != nil {
			ps.add("memory", "Memory per node", true, err)
		} else {
			cfg.Defaults.Memory = memory
		}
	}
	nodes, planned := clusterNodes(f, &ps)
	cfg.Nodes = nodes
	cfg.Users = clusterUsers(f, &ps)
	cfg.Monitoring = checkbox(f, "monitoring", "Monitoring", &ps)
	pull := checkbox(f, "pull", "Pull the image", &ps)
	checkClusterName(c, realm, &cfg, &ps)
	wait := strings.TrimSpace(f.Get("wait"))
	switch {
	case wait == "":
		wait = DefaultWait
	case !slices.Contains(Waits, wait):
		ps.add("wait", "Wait for ready", true, fmt.Errorf("pick one of %s", strings.Join(Waits, ", ")))
		wait = DefaultWait
	}
	data := dataStorage(f, c, &ps)
	cfg.Storage.DataStorage = data.storage

	content, warns, err := renderConfig(cfg, ps.has("name"))
	if err != nil {
		ps.add("", "Config", false, err)
	}
	file := cfg.Name
	if config.CheckName("cluster", file) != nil {
		// Never a path from a name that sind refuses, such as ../x.
		file = "NAME"
	}
	slices.SortStableFunc(ps, func(a, b Problem) int { return fieldOrder(a.Field) - fieldOrder(b.Field) })
	argv := sindArgv(realm, "create", "cluster", "--config", ConfigPath(c.ConfigDir, file))
	if pull {
		argv = append(argv, "--pull")
	}
	if wait != DefaultWait {
		argv = append(argv, "--wait", wait)
	}
	cmd := NewCommand(argv...)
	cmd.Config = &File{Path: ConfigPath(c.ConfigDir, file), Content: content}
	cmd.Nodes = planned
	cmd.Summary = fmt.Sprintf("%d containers, %d CPUs, %s each.", len(planned), cfg.Defaults.CPUs*len(planned), memoryLabel(cfg.Defaults.Memory))
	cmd.Warns = warns
	cmd.Problems = ps
	if data.home {
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("%s is your home directory. Jobs write their output files there as root. Pick a scratch directory such as %s.",
			data.dir, path.Join(data.dir, "sind", cfg.Name)))
	}
	if data.warn != "" {
		cmd.Warns = append(cmd.Warns, data.warn)
	}
	cmd.Notes = append(cmd.Notes, fmt.Sprintf("sind ui saves the config to %s (mode 0600) and keeps it. Copy gives the command; the config is next to it.", cmd.Config.Path))
	if data.mkdir {
		cmd.Dirs = []string{data.dir}
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("sind ui creates the directory %s first. It stays if the create fails.", data.dir))
	}
	if cfg.Monitoring {
		cmd.Notes = append(cmd.Notes, "Monitoring needs Slurm 26.05 or later in the image. sind checks the image when it runs, and stops with an error if it is older.")
	}
	if slices.ContainsFunc(cfg.Nodes, func(n config.Node) bool { return n.Role == config.RoleAPI }) {
		cmd.Notes = append(cmd.Notes, "The api node needs slurmrestd in the image. sind-node images have it from Slurm 26.05. sind checks the image when it runs.")
	}
	if s := c.Snap.Monitorings[realm]; cfg.Monitoring && s != nil {
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("Realm %s has a monitoring stack. sind adds the new nodes to it.", realm))
	}
	return cmd, nil
}

// ClusterFields are the fields of the New cluster dialog in the order it shows
// them, which its problems follow.
var ClusterFields = []string{"name", "image", "backup", "db", "api", "submitter", "workers", "monitoring", "cpus", "memory", "users", "dtype", "dir", "mkdir", "mount", "wait", "pull"}

// fieldOrder is the place of field in the dialog; the config as a whole
// comes last.
func fieldOrder(field string) int {
	if i := slices.Index(ClusterFields, field); i >= 0 {
		return i
	}
	return len(ClusterFields)
}

// checkClusterName records the problems of the cluster's name: one that
// sind refuses, one that is too long for accounting, or one that the realm
// has already.
func checkClusterName(c Context, realm string, cfg *config.Cluster, ps *problems) {
	switch err := config.CheckName("cluster", cfg.Name); {
	case err != nil:
		ps.add("name", "Name", false, err)
	case len(cfg.Name) > config.MaxAccountingClusterName && slices.ContainsFunc(cfg.Nodes, func(n config.Node) bool { return n.Role == config.RoleDB }):
		ps.add("name", "Name", false, fmt.Errorf("with a db node the name may have at most %d characters, as slurmdbd builds its table names from it", config.MaxAccountingClusterName))
	default:
		if _, ok := c.Snap.Cluster(realm, cfg.Name); ok {
			ps.add("name", "Name", false, fmt.Errorf("realm %s has a cluster %s already; pick another name, or delete that cluster first", realm, cfg.Name))
		}
	}
}

// checkbox reads the checkbox field: on, or the 1 of a link, is ticked; off,
// 0 or nothing is not.
func checkbox(f Form, field, label string, ps *problems) bool {
	switch strings.TrimSpace(f.Get(field)) {
	case "", "0", "off", "false":
		return false
	case "on", "1", "true":
		return true
	}
	ps.add(field, label, true, errors.New("tick the box or leave it empty"))
	return false
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
func clusterNodes(f Form, ps *problems) ([]config.Node, []PlannedNode) {
	workers, err := number(f, "workers", "Workers", 1, maxWorkers, -1)
	if err != nil {
		ps.add("workers", "Workers", true, fmt.Errorf("write a number from 1 to %d", maxWorkers))
		workers = defaultWorkers
	}
	nodes := []config.Node{{Role: config.RoleController}}
	planned := []PlannedNode{{"controller", "controller"}}
	for _, r := range switchRoles {
		if !checkbox(f, r.field, r.label, ps) {
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
	return nodes, planned
}

// clusterUsers reads the field "users": names separated by commas or
// spaces, each a valid user name, without duplicates. The plan leaves out a
// wrong name.
func clusterUsers(f Form, ps *problems) []config.User {
	var users []config.User
	var seen []string
	for _, u := range strings.FieldsFunc(f.Get("users"), func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if err := config.CheckUserName(u); err != nil {
			if !ps.has("users") {
				ps.add("users", "Users", false, err)
			}
			continue
		}
		if slices.Contains(seen, u) {
			continue
		}
		seen = append(seen, u)
		users = append(users, config.User{Name: u})
	}
	return users
}

// clusterData is the data storage of a new cluster.
type clusterData struct {
	storage config.DataStorage
	// dir is the host directory, mkdir set if sind ui makes it first.
	dir   string
	mkdir bool
	// home says that dir is the user's home; warn is a check of dir that
	// did not work.
	home bool
	warn string
}

// dataStorage reads the fields "dtype", "dir", "mkdir" and "mount" into the
// data storage of the config. A wrong directory stays in the config as it
// is, where the config can take it.
func dataStorage(f Form, c Context, ps *problems) clusterData {
	var d clusterData
	d.storage.MountPath = mountPath(f, ps)
	switch dtype := f.Get("dtype"); dtype {
	case "volume":
		d.storage.Type = config.StorageVolume
		return d
	case "", "hostPath":
	default:
		ps.add("dtype", "Data", false, fmt.Errorf("pick a directory or a Docker volume, not %q", dtype))
	}
	d.storage.Type = config.StorageHostPath
	dir := strings.TrimSpace(f.Get("dir"))
	var err error
	switch {
	case !plainText(dir):
		ps.add("dir", "Directory", false, errors.New("the path holds control characters"))
		d.storage.Type = ""
		return d
	case dir == "":
		err = errors.New("say which directory")
	case dir == "~" || strings.HasPrefix(dir, "~/"):
		if c.Home == "" {
			err = errors.New("sind ui does not know your home directory; write the path in full")
			break
		}
		dir = path.Join(c.Home, dir[1:])
	case !path.IsAbs(dir):
		err = errors.New("write the path in full, starting with / or ~")
	}
	d.storage.HostPath = dir
	if err != nil {
		ps.add("dir", "Directory", false, err)
		if dir == "" {
			d.storage.Type = ""
		}
		return d
	}
	d.dir = path.Clean(dir)
	d.storage.HostPath = d.dir
	d.home = c.Home != "" && d.dir == path.Clean(c.Home)
	mkdir := checkbox(f, "mkdir", "Create the directory", ps)
	if c.Stat == nil {
		return d
	}
	switch fi, err := c.Stat(d.dir); {
	case errors.Is(err, fs.ErrNotExist) && mkdir:
		d.mkdir = true
	case errors.Is(err, fs.ErrNotExist):
		ps.add("dir", "Directory", false, fmt.Errorf("%s does not exist; tick Create the directory, or make it first", d.dir))
	case err != nil:
		d.warn = fmt.Sprintf("sind ui cannot check the directory %s: %v. Docker needs it to exist.", d.dir, err)
	case !fi.IsDir():
		ps.add("dir", "Directory", false, fmt.Errorf("%s is not a directory", d.dir))
	}
	return d
}

// mountPath reads the field "mount": where the nodes mount the data, or ""
// for /data.
func mountPath(f Form, ps *problems) string {
	m := strings.TrimSpace(f.Get("mount"))
	switch {
	case m == "":
		return ""
	case !plainText(m) || !path.IsAbs(m) || strings.Contains(m, ","):
		ps.add("mount", "Mount path", true, errors.New("write a path in the nodes that starts with / and has no comma, such as /data"))
		return ""
	}
	return path.Clean(m)
}

// parseConfig is config.Parse; tests replace it.
var parseConfig = config.Parse

// renderConfig writes the config as YAML and checks it as sind create
// cluster will: it parses it, applies the defaults and validates it. The
// warnings are those of sind. A name that is wrong already is a problem of
// its own: the check puts a valid one in its place.
func renderConfig(cfg config.Cluster, badName bool) (content string, warns []string, err error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	// A struct of plain fields and a buffer: nothing to fail.
	_ = enc.Encode(cfg)
	_ = enc.Close()
	content = buf.String()
	parsed, err := parseConfig(buf.Bytes())
	if err != nil {
		return content, nil, err
	}
	if badName {
		parsed.Name = "cluster"
	}
	parsed.ApplyDefaults()
	if err := parsed.Validate(); err != nil {
		return content, nil, err
	}
	return content, parsed.Warnings(), nil
}
