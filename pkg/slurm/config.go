// SPDX-License-Identifier: LGPL-3.0-or-later

package slurm

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/GSI-HPC/sind/pkg/config"
)

// Container paths for Slurm configuration files.
const (
	ConfDir          = "/etc/slurm"
	SlurmConfFile    = "slurm.conf"
	SlurmConfPath    = ConfDir + "/" + SlurmConfFile
	NodesConfFile    = "sind-nodes.conf"
	NodesConfPath    = ConfDir + "/" + NodesConfFile
	SlurmdbdConfFile = "slurmdbd.conf"
	SlurmdbdConfPath = ConfDir + "/" + SlurmdbdConfFile
)

// Accounting: the db node's hostname and the MariaDB database and user
// slurmdbd stores accounting data in.
const (
	DBHost       = "db"
	AccountingDB = "slurm_acct_db"
	StorageUser  = "slurm"
)

// Controller hostnames and the slurmctld state directory.
const (
	ControllerHost       = "controller"
	BackupControllerHost = "controller-backup"
	StateSaveLocation    = "/var/spool/slurmctld"
)

// DefaultSlurmctldTimeout is the SlurmctldTimeout sind writes for a
// primary/backup controller pair, in seconds. Slurm's default of 120s makes
// failover tests needlessly slow; users can override it in the main section.
const DefaultSlurmctldTimeout = 20

// ConfOptions selects the optional parts of the generated slurm.conf.
type ConfOptions struct {
	// BackupController configures controller-backup as the backup
	// controller of a primary/backup pair.
	BackupController bool
	// Accounting sends accounting data to the slurmdbd on a managed db node
	// and gathers per-job resource usage.
	Accounting bool
	// Identity adds the settings of the identity modes other than local:
	// nss_slurm for both, and auth/slurm with client IDs for clientIds.
	Identity config.IdentityMode
	// JWT adds auth/jwt as an alternative authentication type, for
	// slurmrestd on a managed api node (see jwtDefaults).
	JWT bool
	// DefMemPerCPU is the default memory of a job per allocated CPU, in
	// MB, 0 for none (see DefMemPerCPU).
	DefMemPerCPU int
}

// parameter is a slurm.conf parameter sind sets unless the main section
// sets it.
type parameter struct{ key, value string }

// DefaultTaskPlugin is the TaskPlugin sind sets unless the main section
// sets one. It leaves out task/affinity: sind limits a node's CPUs with a
// CPU quota, not a cpuset, so every worker sees all host CPUs, and
// task/affinity would bind the jobs of every worker to the same first
// host CPUs.
const DefaultTaskPlugin = "task/cgroup"

// baseDefaults are the slurm.conf parameters sind sets on every managed
// cluster unless the main section sets them, in output order: cgroup
// process tracking and task containment, PMIx as the MPI type of srun, and
// workers that return to service when they register with a valid
// configuration.
var baseDefaults = []parameter{
	{"ProctrackType", "proctrack/cgroup"},
	{"TaskPlugin", DefaultTaskPlugin},
	{"MpiDefault", "pmix"},
	{"ReturnToService", "2"},
}

// accountingDefaults are the slurm.conf parameters sind sets when the
// cluster has a managed db node, in output order.
var accountingDefaults = []parameter{
	{"AccountingStorageType", "accounting_storage/slurmdbd"},
	{"AccountingStorageHost", DBHost},
	{"JobAcctGatherType", "jobacct_gather/cgroup"},
}

// identityDefaults returns the slurm.conf parameters of an identity mode
// (see config.IdentityMode.SlurmParameters).
func identityDefaults(mode config.IdentityMode) []parameter {
	var defaults []parameter
	for _, p := range mode.SlurmParameters() {
		defaults = append(defaults, parameter{p.Key, p.Value})
	}
	return defaults
}

// jwtDefaults returns the parameters, of slurm.conf and of slurmdbd.conf,
// that set up auth/jwt for slurmrestd: an alternative authentication type
// next to the AuthType, with JWTKeyPath. With identity clientIds, where
// slurmctld and slurmdbd have no Linux accounts to look a token's user up
// in, tokens may also carry the user's identity (use_jwt_client_ids), as
// sackd's auth/slurm tokens do.
func jwtDefaults(identity config.IdentityMode) []parameter {
	params := "jwt_key=" + JWTKeyPath
	if identity == config.IdentityClientIDs {
		params += ",use_jwt_client_ids"
	}
	return []parameter{
		{"AuthAltTypes", config.JWTAuthType},
		{"AuthAltParameters", params},
	}
}

// writeDefaults writes each parameter that the main section does not set,
// after a blank line if there are any.
func writeDefaults(b *strings.Builder, main config.Section, defaults []parameter) {
	blank := "\n"
	for _, p := range defaults {
		if !main.SetsParameter(p.key) {
			b.WriteString(blank + p.key + "=" + p.value + "\n")
			blank = ""
		}
	}
}

// TaskAffinity reports whether the slurm.conf sind generates with a main
// section enables task/affinity: whether the main section's TaskPlugin, or
// else DefaultTaskPlugin, lists it. slurmstepd then sets each task's CPU
// affinity after the task has become the job's user, which needs
// CAP_SYS_NICE for users other than root.
func TaskAffinity(main config.Section) bool {
	plugins, ok := main.Parameter("TaskPlugin")
	if !ok {
		plugins = DefaultTaskPlugin
	}
	return listsTaskAffinity(plugins)
}

// listsTaskAffinity reports whether a TaskPlugin value lists task/affinity;
// Slurm also takes a plugin name without its task/ prefix.
func listsTaskAffinity(plugins string) bool {
	return config.ListsValue(plugins, "task/affinity") || config.ListsValue(plugins, "affinity")
}

// maxIncludeDepth bounds the nesting of include directives that
// ReadSlurmConf follows, which could otherwise loop.
const maxIncludeDepth = 10

// ReadSlurmConf returns a cluster's slurm.conf as slurmctld and slurmd
// read it, as a section whose content holds its lines with each include
// directive replaced by the lines of the file it names. read returns a
// file of the config volume by path. sind-nodes.conf holds only sind's
// nodes and partition and is left out, as are include paths with a "*",
// which Slurm skips.
func ReadSlurmConf(read func(path string) (string, error)) (config.Section, error) {
	var b strings.Builder
	var clusterName string
	if err := readConfFile(read, SlurmConfPath, &b, &clusterName, 0); err != nil {
		return config.Section{}, err
	}
	return config.Section{Content: b.String()}, nil
}

// readConfFile writes the lines of the file at path to b, each ending in a
// newline, with the files it includes in place of their include
// directives (see ReadSlurmConf). clusterName holds the ClusterName read
// so far, which Slurm puts in place of %c in an include path.
func readConfFile(read func(path string) (string, error), path string, b *strings.Builder, clusterName *string, depth int) error {
	if depth > maxIncludeDepth {
		return fmt.Errorf("reading %s: includes nested more than %d deep", path, maxIncludeDepth)
	}
	content, err := read(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	for line := range strings.Lines(content) {
		if inc, ok := includePath(line, *clusterName); ok {
			if inc == NodesConfPath || strings.Contains(inc, "*") {
				continue
			}
			if err := readConfFile(read, inc, b, clusterName, depth+1); err != nil {
				return err
			}
			continue
		}
		if v, ok := config.LineParameter(line, "ClusterName"); ok {
			*clusterName = strings.ToLower(v)
		}
		b.WriteString(strings.TrimSuffix(line, "\n") + "\n")
	}
	return nil
}

// EnablesTaskAffinity reports whether a cluster's slurm.conf, as
// ReadSlurmConf returns it, enables task/affinity, as slurmd on a worker
// added now would read it: whether its TaskPlugin lists it. Without a
// TaskPlugin, Slurm runs no task plugin.
func EnablesTaskAffinity(conf config.Section) bool {
	plugins, _ := conf.Parameter("TaskPlugin")
	return listsTaskAffinity(plugins)
}

// includePath returns the file an include directive of slurm.conf names,
// as Slurm reads it: the first word after "include", with %c replaced by
// the cluster name and a relative path taken from ConfDir.
func includePath(line, clusterName string) (string, bool) {
	line, _, _ = strings.Cut(line, "#")
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "include") {
		return "", false
	}
	file := strings.ReplaceAll(fields[1], "%c", clusterName)
	if !path.IsAbs(file) {
		file = path.Join(ConfDir, file)
	}
	return file, true
}

// GenerateSlurmConf generates the main slurm.conf content for a cluster.
// Node definitions are not included here; they go in sind-nodes.conf.
// The main section can append content (string form) or include a .conf.d
// directory (map form).
//
// The cluster name, the controller, SlurmUser, the spool directories and
// PlugStackConfig are fixed: sind's volumes and images depend on them. The
// parameters in baseDefaults follow, each unless the main section sets it,
// and opts.DefMemPerCPU unless it sets DefMemPerCPU or DefMemPerNode, which
// Slurm does not take together.
// With opts.BackupController, controller-backup is configured as the backup
// controller and SlurmctldTimeout defaults to DefaultSlurmctldTimeout unless
// the main section sets it. With opts.Accounting, the accounting parameters
// in accountingDefaults are added, with opts.Identity those of the
// identity mode, and with opts.JWT those of jwtDefaults, each unless the
// main section sets it.
func GenerateSlurmConf(clusterName string, main config.Section, opts ConfOptions) string {
	var b strings.Builder
	b.WriteString("# Generated by sind\n")
	b.WriteString("ClusterName=" + clusterName + "\n")
	b.WriteString("SlurmctldHost=" + ControllerHost + "\n")
	if opts.BackupController {
		b.WriteString("SlurmctldHost=" + BackupControllerHost + "\n")
		if !main.SetsParameter("SlurmctldTimeout") {
			b.WriteString("SlurmctldTimeout=" + strconv.Itoa(DefaultSlurmctldTimeout) + "\n")
		}
	}
	b.WriteString("SlurmUser=slurm\n")
	b.WriteString("StateSaveLocation=" + StateSaveLocation + "\n")
	b.WriteString("SlurmdSpoolDir=/var/spool/slurmd\n")
	b.WriteString("PlugStackConfig=" + ConfDir + "/plugstack.conf\n")
	base := baseDefaults
	if opts.DefMemPerCPU > 0 && !main.SetsParameter("DefMemPerNode") {
		base = append(slices.Clip(base), parameter{"DefMemPerCPU", strconv.Itoa(opts.DefMemPerCPU)})
	}
	writeDefaults(&b, main, base)
	writeDefaults(&b, main, identityDefaults(opts.Identity))
	if opts.JWT {
		writeDefaults(&b, main, jwtDefaults(opts.Identity))
	}
	if opts.Accounting {
		writeDefaults(&b, main, accountingDefaults)
	}
	b.WriteString("\n")
	appendSection(&b, "slurm", main)
	b.WriteString(nodesInclude)
	return b.String()
}

// nodesInclude ends slurm.conf: sind-nodes.conf comes after the main
// section, so that its NodeName=DEFAULT and PartitionName=DEFAULT lines
// apply to sind's nodes and partition. slurmctld reads every node before
// any partition, so the main section's partitions may name sind's nodes.
const nodesInclude = "include " + NodesConfPath + "\n"

// GenerateSlurmdbdConf generates slurmdbd.conf for the db node: slurmdbd
// runs as SlurmUser and stores accounting data in the local MariaDB as
// StorageUser, which MariaDB authenticates by unix_socket as the OS user
// slurmdbd runs as, or by the StoragePass the slurmdbd section sets. It
// authenticates with munge, or with identity clientIds with auth/slurm and
// the users' client IDs, as slurmctld does, and with jwt also with auth/jwt
// (see jwtDefaults), each JWT parameter unless the slurmdbd section sets it:
// slurmdbd has no default key path. The slurmdbd section can append content
// (string form) or include .conf.d fragments (map form).
//
// The pid file lives in /run/slurmdbd, the RuntimeDirectory slurmdbd.service
// creates for the slurm user: /run itself is not writable for it. sind
// writes no StoragePass line, since slurmdbd rejects an empty value.
func GenerateSlurmdbdConf(slurmdbd config.Section, identity config.IdentityMode, jwt bool) string {
	var b strings.Builder
	b.WriteString("# Generated by sind\n")
	if identity == config.IdentityClientIDs {
		b.WriteString("AuthType=auth/slurm\n")
		b.WriteString("AuthInfo=use_client_ids\n")
	} else {
		b.WriteString("AuthType=auth/munge\n")
	}
	if jwt {
		for _, p := range jwtDefaults(identity) {
			if !slurmdbd.SetsParameter(p.key) {
				b.WriteString(p.key + "=" + p.value + "\n")
			}
		}
	}
	b.WriteString("DbdHost=" + DBHost + "\n")
	b.WriteString("SlurmUser=slurm\n")
	b.WriteString("LogFile=/var/log/slurm/slurmdbd.log\n")
	b.WriteString("PidFile=/run/slurmdbd/slurmdbd.pid\n")
	b.WriteString("\n")
	b.WriteString("StorageType=accounting_storage/mysql\n")
	b.WriteString("StorageHost=localhost\n")
	b.WriteString("StorageLoc=" + AccountingDB + "\n")
	b.WriteString("StorageUser=" + StorageUser + "\n")
	appendSection(&b, "slurmdbd", slurmdbd)
	return b.String()
}

// GenerateCgroupConf generates the cgroup.conf content for cgroupv2 support.
// The cgroup section can append content (string form) or include a .conf.d
// directory (map form).
func GenerateCgroupConf(cgroup config.Section) string {
	var b strings.Builder
	b.WriteString("# Generated by sind\n")
	b.WriteString("CgroupPlugin=autodetect\n")
	appendSection(&b, "cgroup", cgroup)
	return b.String()
}

// GeneratePlugstackConf generates the plugstack.conf content.
// Always includes the .conf.d directory for fragment support.
func GeneratePlugstackConf(plugstack config.Section) string {
	var b strings.Builder
	b.WriteString("# Generated by sind\n")
	b.WriteString("include " + ConfDir + "/plugstack.conf.d/*\n")
	if plugstack.Content != "" {
		b.WriteString(plugstack.Content)
	}
	return b.String()
}

// GenerateSectionConf generates the content for a standalone config file
// (gres.conf, topology.conf). String form returns content directly;
// map form returns include lines for each fragment file.
func GenerateSectionConf(name string, section config.Section) string {
	if section.IsMap() {
		var b strings.Builder
		appendFragmentIncludes(&b, name, section)
		return b.String()
	}
	return section.Content
}

// appendSection appends section content to a config file builder.
// String form appends content directly, ending with a newline; map form
// appends an include line for each fragment file (Slurm does not support
// glob includes).
func appendSection(b *strings.Builder, name string, s config.Section) {
	if s.IsEmpty() {
		return
	}
	if s.IsMap() {
		appendFragmentIncludes(b, name, s)
		return
	}
	b.WriteString(s.Content)
	if !strings.HasSuffix(s.Content, "\n") {
		b.WriteString("\n")
	}
}

// appendFragmentIncludes writes an include line for each fragment file
// in sorted order.
func appendFragmentIncludes(b *strings.Builder, name string, s config.Section) {
	for _, key := range s.FragmentNames() {
		b.WriteString("include " + ConfDir + "/" + name + ".conf.d/" + key + ".conf\n")
	}
}
