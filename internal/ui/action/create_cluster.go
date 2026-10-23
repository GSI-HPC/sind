// SPDX-License-Identifier: LGPL-3.0-or-later

package action

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
)

func init() {
	register(Action{ID: "create-cluster", Build: buildCreateCluster, Cluster: func(f Form) string { return strings.TrimSpace(f.Get("name")) }})
}

// Waits are the values of the field "wait" of the New cluster dialog that
// it offers as buttons, as sind create cluster --wait takes them; the value
// "other" takes the field "wait_other", any time that --wait takes.
var Waits = []string{"2m", "5m", "10m", "0"}

// HostMemoryWarn is the share of the memory of the Docker host, in
// percent, above which the plan of a new cluster warns.
const HostMemoryWarn = 80

// DefaultWait is the --wait of sind create cluster, which the command line
// leaves out.
const DefaultWait = "5m"

// imagePattern is what the image reference of a form may look like: the
// characters of a registry, repository, tag and digest.
var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// ImageRef reports whether s looks like an image reference: the
// characters of a registry, repository, tag and digest.
func ImageRef(s string) bool { return imagePattern.MatchString(s) }

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

// clusterPlan is the config of a new cluster, as the fields of the form or
// its YAML give it: the config, its text and sind's warnings, the nodes one
// by one, and the data storage.
type clusterPlan struct {
	cfg     config.Cluster
	content string
	warns   []string
	planned []PlannedNode
	data    clusterData
}

// buildCreateCluster makes the command of the New cluster dialog. A wrong
// field is a problem of the command, not an error: the plan takes the
// field's default, or the name as it is, so that the dialog can show the
// rest of it. With the field "yaml_on" the config is the YAML that the user
// edits; the run options stay those of the form.
func buildCreateCluster(f Form, c Context) (Command, error) {
	if c.ConfigDir == "" {
		return Command{}, errors.New("sind ui has no state directory to save the config in; set XDG_STATE_HOME or HOME")
	}
	realm := Realm(f)
	var ps problems
	if err := nameError("realm", realm); err != nil {
		ps.add("realm", "Realm", false, err)
	}
	var plan clusterPlan
	if f.Get("yaml_on") != "" {
		plan = yamlPlan(f, c, realm, &ps)
	} else {
		plan = formPlan(f, c, realm, &ps)
	}
	cfg, data, content := plan.cfg, plan.data, plan.content
	pull := clusterPull(f, cfg.Defaults.Image, &ps)
	wait := clusterWait(f, &ps)
	file := cfg.Name
	if config.CheckName("cluster", file) != nil {
		// Never a path from a name that sind refuses, such as ../x.
		file = "NAME"
	}
	// The images go to a command of their own first: their notes come
	// after those of the files.
	var images Command
	monitoring := "monitoring"
	if f.Get("yaml_on") != "" {
		monitoring = "yaml"
	}
	clusterImages(cfg, pull, c.Image, monitoring, &images, &ps)
	order := ClusterFieldsOf(f)
	slices.SortStableFunc(ps, func(a, b Problem) int { return fieldOrder(order, a.Field) - fieldOrder(order, b.Field) })
	var flags []string
	if pull {
		flags = append(flags, "--pull")
	}
	if wait != DefaultWait {
		flags = append(flags, "--wait", wait)
	}
	cmd := NewCommand(append(sindArgv(realm, "create", "cluster", "--config", ConfigPath(c.ConfigDir, file)), flags...)...)
	cmd.Config = &File{Path: ConfigPath(c.ConfigDir, file), Content: content}
	cmd.Nodes = plan.planned
	containers, cpus, memoryMB := clusterSize(cfg.Defaults, cfg.Nodes)
	cmd.Summary = fmt.Sprintf("%d containers, %d CPUs, %s of memory.", containers, cpus, MemoryLabel(memoryMB))
	cmd.Containers, cmd.CPUs, cmd.MemoryMB = containers, cpus, memoryMB
	cmd.Warns = plan.warns
	cmd.Problems = ps
	if data.home {
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("%s is your home directory. Jobs write their output files there as root. Pick a scratch directory such as %s.",
			data.dir, path.Join(data.dir, "sind", cfg.Name)))
	}
	if data.warn != "" {
		cmd.Warns = append(cmd.Warns, data.warn)
	}
	if host := int(c.Snap.Docker.MemTotal >> 20); host > 0 && memoryMB*100 > host*HostMemoryWarn {
		cmd.Warns = append(cmd.Warns, fmt.Sprintf("The nodes may use %s of memory, %d%% of the %s of the Docker host, which then can run out of memory.",
			MemoryLabel(memoryMB), memoryMB*100/host, MemoryLabel(host)))
	}
	cmd.Notes = append(cmd.Notes, fmt.Sprintf("sind ui saves the config to %s (mode 0600) and keeps it.", cmd.Config.Path))
	if data.mkdir {
		cmd.Dirs = []string{data.dir}
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("sind ui creates the directory %s first. It stays if the create fails.", data.dir))
	}
	cmd.Script = clusterScript(realm, file, content, cmd.Dirs, flags)
	cmd.Images = images.Images
	cmd.Notes = append(cmd.Notes, images.Notes...)
	cmd.Warns = append(cmd.Warns, images.Warns...)
	if s := c.Snap.Monitorings[realm]; cfg.Monitoring && s != nil {
		cmd.Notes = append(cmd.Notes, fmt.Sprintf("Realm %s has a monitoring stack. sind adds the new nodes to it.", realm))
	}
	return cmd, nil
}

// formPlan reads the config of a new cluster from the fields of the form.
func formPlan(f Form, c Context, realm string, ps *problems) clusterPlan {
	cfg := config.Cluster{Kind: "Cluster", Name: strings.TrimSpace(f.Get("name"))}
	if realm != mesh.DefaultRealm && !ps.has("realm") {
		cfg.Realm = realm
	}
	if image := strings.TrimSpace(f.Get("image")); image != "" {
		if imagePattern.MatchString(image) {
			cfg.Defaults.Image = image
		} else {
			ps.add("image", "Image", true, errors.New("write an image reference such as ghcr.io/gsi-hpc/sind-node:latest"))
		}
	}
	nodeDefaults(f, &cfg.Defaults, ps)
	nodes, planned := clusterNodes(f, ps)
	cfg.Nodes = nodes
	clusterUsers(f, &cfg, ps)
	clusterSlurm(f, &cfg, ps)
	cfg.Monitoring = checkbox(f, "monitoring", "Monitoring", ps)
	checkClusterName(c, realm, &cfg, ps)
	data := dataStorage(f, c, ps)
	cfg.Storage.DataStorage = data.storage

	content, warns, errs := renderConfig(cfg, ps.has("name"))
	for _, err := range errs {
		*ps = append(*ps, configProblem(f, err))
	}
	return clusterPlan{cfg: cfg, content: content, warns: warns, planned: planned, data: data}
}

// maxPlanned is the most workers of a plan, as the form allows them.
const maxPlanned = maxGroups * maxCount

// yamlPlan reads the config of a new cluster from the field "yaml", which
// sind ui saves as it is. Its problems are those of the YAML, but for the
// name and the realm, whose fields stay beside the YAML. A host directory
// that is missing is created first, as Create the directory does by
// default.
func yamlPlan(f Form, c Context, realm string, ps *problems) clusterPlan {
	p := clusterPlan{content: strings.ReplaceAll(f.Get("yaml"), "\r\n", "\n")}
	var yps problems
	defer func() {
		for _, pr := range yps {
			pr.Field = "yaml"
			*ps = append(*ps, pr)
		}
	}()
	if strings.TrimSpace(p.content) == "" {
		yps.add("", "YAML", false, errors.New("paste a cluster config, or open a file of one"))
		return p
	}
	parsed, err := parseConfig([]byte(p.content))
	if err != nil {
		yps.add("", "YAML", false, err)
		return p
	}
	if parsed.Realm != "" && parsed.Realm != realm {
		ps.add("realm", "Realm", false, fmt.Errorf("the YAML names realm %s, and this field %s; make them the same, or remove realm from the YAML", parsed.Realm, realm))
	}
	parsed.ApplyDefaults()
	var nps problems
	checkClusterName(c, realm, parsed, &nps)
	*ps = append(*ps, nps...)
	name := parsed.Name
	if nps.has("name") {
		// As renderConfig does: the name is a problem of its own.
		parsed.Name = "cluster"
	}
	errs := parsed.ValidateAll()
	parsed.Name = name
	p.cfg = *parsed
	for _, err := range errs {
		yps.add("", "Config", false, err)
	}
	if len(errs) == 0 {
		p.warns = parsed.Warnings()
	}
	p.planned = plannedOf(p.cfg.Nodes, &yps)
	if ds := p.cfg.Storage.DataStorage; ds.UsesHostPath() && path.IsAbs(ds.HostPath) {
		p.data.dir = path.Clean(ds.HostPath)
		checkDir(c, &p.data, true, &yps)
	}
	return p
}

// plannedOf returns the nodes of a config one by one, as sind names them,
// with at most maxPlanned workers.
func plannedOf(nodes []config.Node, ps *problems) []PlannedNode {
	var out []PlannedNode
	workers := 0
	for _, n := range nodes {
		switch n.Role {
		case config.RoleWorker:
			for range max(n.Count, 1) {
				if workers == maxPlanned {
					ps.add("", "Workers", false, fmt.Errorf("a cluster here has at most %d workers", maxPlanned))
					return out
				}
				out = append(out, PlannedNode{"worker-" + strconv.Itoa(workers), "worker", "yaml"})
				workers++
			}
		case config.RoleController:
			out = append(out, PlannedNode{"controller", "controller", "controller"})
			if n.BackupController {
				out = append(out, PlannedNode{"controller-backup", "controller-backup", "backup"})
			}
		default:
			out = append(out, PlannedNode{string(n.Role), string(n.Role), string(n.Role)})
		}
	}
	return out
}

// fieldOrder is the place of field in order, the fields of the dialog;
// the config as a whole comes last.
func fieldOrder(order []string, field string) int {
	if i := slices.Index(order, field); i >= 0 {
		return i
	}
	return len(order)
}

// checkClusterName records the problems of the cluster's name: one that
// sind refuses, one that is too long for accounting, or one that the realm
// has already.
func checkClusterName(c Context, realm string, cfg *config.Cluster, ps *problems) {
	switch err := nameError("cluster", cfg.Name); {
	case err != nil:
		ps.add("name", "Name", false, err)
	case len(cfg.Name) > config.MaxAccountingClusterName && slices.ContainsFunc(cfg.Nodes, func(n config.Node) bool { return n.Role == config.RoleDB }):
		ps.add("name", "Name", false, fmt.Errorf("with a db node, use at most %d characters, as slurmdbd builds its table names from the name", config.MaxAccountingClusterName))
	default:
		if _, ok := c.Snap.Cluster(realm, cfg.Name); ok {
			ps.add("name", "Name", false, fmt.Errorf("realm %s has a cluster %s already; pick another name, or delete that cluster first", realm, cfg.Name))
		}
	}
}

// clusterPull reads the field "pull", unless the field "pull_auto" says to
// follow the image: then sind ui pulls as PullByDefault says.
func clusterPull(f Form, image string, ps *problems) bool {
	if f.Get("pull_auto") != "" {
		pull, _ := PullByDefault(image)
		return pull
	}
	return checkbox(f, "pull", "Pull the image", ps)
}

// movingTag is a tag that a registry moves to newer images: a major or a
// major and minor version, such as 26 or 26.05.
var movingTag = regexp.MustCompile(`^v?\d+(\.\d+)?$`)

// PullByDefault reports whether sind ui pulls the image unless told
// otherwise, and why: it pulls an image of a registry whose tag can move to
// a newer image, as latest does, so that the cluster gets that image. An
// empty image is config.DefaultImage.
func PullByDefault(image string) (bool, string) {
	if image == "" {
		image = config.DefaultImage
	}
	if strings.Contains(image, "@") {
		return false, "the image names a digest, which is fixed"
	}
	repo, rest, ok := strings.Cut(image, "/")
	if !ok || !strings.ContainsAny(repo, ".:") && repo != "localhost" {
		return false, "the image names no registry, as an image you built does"
	}
	tag := "latest"
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		tag = rest[i+1:]
	}
	if tag == "latest" || movingTag.MatchString(tag) {
		return true, fmt.Sprintf("the tag %s can move to a newer image", tag)
	}
	return false, fmt.Sprintf("the tag %s is fixed", tag)
}

// clusterWait reads the wait for ready: a button of Waits, or "other" with
// the field "wait_other". Empty is DefaultWait.
func clusterWait(f Form, ps *problems) string {
	wait, field := strings.TrimSpace(f.Get("wait")), "wait"
	if wait == "other" {
		wait, field = strings.TrimSpace(f.Get("wait_other")), "wait_other"
	}
	switch d, err := time.ParseDuration(wait); {
	case wait == "" && field == "wait":
		return DefaultWait
	case err != nil || d < 0:
		ps.add(field, "Wait for ready", true, errors.New("write a time such as 15m or 1h30m, or pick No limit"))
		return DefaultWait
	case d == 0:
		return "0"
	case d == 5*time.Minute:
		return DefaultWait
	}
	return wait
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
	checkDir(c, &d, checkbox(f, "mkdir", "Create the directory", ps), ps)
	return d
}

// checkDir looks at the host directory d.dir, which mkdir says to create
// if it is missing, and whether it is the user's home.
func checkDir(c Context, d *clusterData, mkdir bool, ps *problems) {
	d.home = c.Home != "" && d.dir == path.Clean(c.Home)
	if c.Stat == nil {
		return
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

// clusterScript is the shell script that does what Create does: it writes
// the config, readable by its owner alone, creates the directories and
// runs the command. It always names the realm, as SIND_REALM in the shell
// would else take the place of the config's.
func clusterScript(realm, file, content string, dirs, flags []string) string {
	sum := sha256.Sum256([]byte(content))
	// The tag of the here-document: a line that the config cannot hold.
	tag := "SIND_CONFIG_" + hex.EncodeToString(sum[:8])
	var b strings.Builder
	fmt.Fprintf(&b, "cfg=\"${XDG_STATE_HOME:-$HOME/.local/state}/sind/ui/configs/%s.yaml\"\n", file)
	b.WriteString("mkdir -p \"$(dirname \"$cfg\")\"\n")
	fmt.Fprintf(&b, "(umask 077 && cat >\"$cfg\") <<'%s'\n%s%s\n", tag, content, tag)
	for _, d := range dirs {
		fmt.Fprintf(&b, "mkdir -p %s\n", quoteWord(d))
	}
	fmt.Fprintf(&b, "sind --realm %s create cluster --config \"$cfg\"", quoteWord(realm))
	if len(flags) > 0 {
		b.WriteString(" " + Quote(flags))
	}
	b.WriteString("\n")
	return b.String()
}

// parseConfig is config.Parse; tests replace it.
var parseConfig = config.Parse

// renderConfig writes the config as YAML and checks it as sind create
// cluster will: it parses it, applies the defaults and validates it. It
// returns every error of the check, where sind stops at the first. The
// warnings are those of sind. A name that is wrong already is a problem of
// its own: the check puts a valid one in its place.
func renderConfig(cfg config.Cluster, badName bool) (content string, warns []string, errs []error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	// A struct of plain fields and a buffer: nothing to fail.
	_ = enc.Encode(cfg)
	_ = enc.Close()
	content = buf.String()
	parsed, err := parseConfig(buf.Bytes())
	if err != nil {
		return content, nil, []error{err}
	}
	if badName {
		parsed.Name = "cluster"
	}
	parsed.ApplyDefaults()
	if errs := parsed.ValidateAll(); len(errs) > 0 {
		return content, nil, errs
	}
	return content, parsed.Warnings(), nil
}
