// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/GSI-HPC/sind/pkg/docker"
	sindlog "github.com/GSI-HPC/sind/pkg/log"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/probe"
)

// labelRealm is the realm label of the stack's volumes, as pkg/mesh and
// pkg/cluster name it. The containers do not carry it: a container with
// sind.realm would count as another cluster's to the delete of a realm's
// last cluster.
const labelRealm = "sind.realm"

// DefaultReadyTimeout is how long EnsureStack waits for the stack to be
// ready, when the context has no earlier deadline: Loki reports ready only
// some seconds after it starts.
const DefaultReadyTimeout = 2 * time.Minute

// Manager creates, configures and removes a realm's monitoring stack.
type Manager struct {
	Docker *docker.Client
	Realm  string
	Pull   bool // pull the stack's images afresh (--pull always)
	// Interval is the delay between readiness probe rounds, zero for
	// probe.DefaultInterval; ReadyTimeout bounds the wait, zero for
	// DefaultReadyTimeout.
	Interval     time.Duration
	ReadyTimeout time.Duration
}

// NewManager returns a Manager of a realm's stack.
func NewManager(dc *docker.Client, realm string) *Manager {
	return &Manager{Docker: dc, Realm: realm}
}

// network is the realm's mesh network, which the stack runs on.
func (m *Manager) network() docker.NetworkName {
	return docker.NetworkName(m.Realm + "-mesh")
}

// composeProject is the Compose project of the realm's mesh, which the
// stack's containers and volumes join.
func (m *Manager) composeProject() string {
	return m.Realm + "-mesh"
}

// dataDir is where a service keeps its data, on its volume.
func (s Service) dataDir() string {
	switch s {
	case ServiceGrafana:
		return "/var/lib/grafana"
	case ServicePrometheus:
		return "/prometheus"
	case ServiceLoki:
		return "/loki"
	default:
		return "/var/lib/alloy/data"
	}
}

// args are the arguments of a service's command, after its image.
func (s Service) args() []string {
	switch s {
	case ServicePrometheus:
		return prometheusArgs
	case ServiceLoki:
		return lokiArgs
	case ServiceAlloy:
		return alloyArgs
	default:
		return nil
	}
}

// createArgs returns the docker create arguments of a service's container:
// on the mesh network, at ip unless it is empty, with the Compose labels
// of the mesh's project, the fingerprint fp, its memory cap and its data
// volume, pulling its image afresh with pull. Grafana gets its environment (see grafanaEnv). Alloy runs as
// root in the host's cgroup namespace, with the host's cgroup tree
// read-only, so that its cAdvisor reads the nodes' cgroups, and the journal
// volume, where it also clears and removes the nodes' directories; it is
// exempt from SELinux labeling, which would deny it both.
func (m *Manager) createArgs(s Service, ip, fp string, pull bool) []string {
	labels := docker.ComposeLabels(m.composeProject(), string(s), 1)
	labels[LabelVersion] = fp
	args := []string{"--name", string(ContainerName(m.Realm, s)), "--network", string(m.network())}
	if ip != "" {
		args = append(args, "--ip", ip)
	}
	args = append(args, docker.SortedLabelFlags(labels)...)
	args = append(args,
		"--memory", s.memoryLimit(),
		"--mount", docker.VolumeMount(VolumeName(m.Realm, s), s.dataDir(), "", false, false),
	)
	switch s {
	case ServiceGrafana:
		for _, e := range grafanaEnv {
			args = append(args, "--env", e)
		}
	case ServiceAlloy:
		args = append(args,
			"--user", "0:0",
			"--cgroupns", "host",
			"--security-opt", "label=disable",
			"--mount", "type=bind,source=/sys/fs/cgroup,target=/sys/fs/cgroup,readonly",
			"--mount", docker.VolumeMount(JournalVolume(m.Realm), JournalDir, "", false, false),
		)
	}
	if pull {
		args = append(args, "--pull", "always")
	}
	args = append(args, s.Image())
	return append(args, s.args()...)
}

// Fingerprint returns the fingerprint of the stack a realm gets from this
// sind: of its services' container arguments and of the files sind
// provisions them with, except Alloy's configuration, which each sync
// rewrites, and of --pull. A stack created by another sind version, with other images,
// arguments, configuration or dashboards, has another one.
func (m *Manager) Fingerprint() string {
	h := sha256.New()
	for _, s := range Services {
		for _, a := range m.createArgs(s, "", "", false) {
			_, _ = fmt.Fprintf(h, "%s\x00", a) // a hash never fails a write
		}
		dir, files := serviceFiles(m.Realm, s, "")
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			_, _ = fmt.Fprintf(h, "%s\x00%o\x00%s\x00", path.Join(dir, name), files[name].Mode, files[name].Content)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// startOrder is the order EnsureStack starts the services in: the stores
// before Alloy, which writes to them, and Grafana, which reads them.
var startOrder = []Service{ServicePrometheus, ServiceLoki, ServiceAlloy, ServiceGrafana}

// EnsureStack brings up the realm's stack on the mesh network: it creates
// the data volumes and containers that do not exist yet, with alloyConfig
// as Alloy's first configuration, recreates those of another fingerprint
// (another sind version's) and keeps their volumes, starts those that are
// stopped, and waits until all four are ready. dnsIP is the mesh DNS
// container's pinned address, from which Grafana, Prometheus and Loki take
// theirs (see mesh.ServiceIP); empty for a mesh without one, whose stack
// gets dynamic addresses. It reports whether it created or started a
// container.
func (m *Manager) EnsureStack(ctx context.Context, dnsIP, alloyConfig string) (changed bool, err error) {
	log := sindlog.From(ctx)
	fp := m.Fingerprint()
	for _, s := range startOrder {
		labels := docker.Labels{
			labelRealm:                 m.Realm,
			docker.ComposeProjectLabel: m.composeProject(),
			docker.ComposeVolumeLabel:  string(s),
		}
		if err := m.Docker.CreateVolume(ctx, VolumeName(m.Realm, s), labels); err != nil {
			return changed, fmt.Errorf("creating %s volume: %w", s, err)
		}
	}
	for _, s := range startOrder {
		name := ContainerName(m.Realm, s)
		info, err := docker.IfExists(m.Docker.InspectContainer(ctx, name))
		if err != nil {
			return changed, fmt.Errorf("checking %s container: %w", s, err)
		}
		if info != nil && info.Labels[LabelVersion] != fp {
			log.InfoContext(ctx, "recreating monitoring container", "container", string(name),
				"fingerprint", info.Labels[LabelVersion], "want", fp)
			if err := docker.IgnoreNotFound(m.Docker.RemoveContainer(ctx, name)); err != nil {
				return changed, fmt.Errorf("removing %s container: %w", s, err)
			}
			info = nil
		}
		if info == nil {
			if err := m.create(ctx, s, dnsIP, fp, alloyConfig); err != nil {
				return true, err
			}
			info = &docker.ContainerInfo{Status: docker.StateCreated}
		}
		if info.Status != docker.StateRunning {
			changed = true
			if err := m.Docker.StartContainer(ctx, name); err != nil {
				return changed, fmt.Errorf("starting %s container: %w", s, err)
			}
		}
	}
	return changed, m.WaitReady(ctx)
}

// Start starts the stack's containers that are stopped, as after a host
// reboot, and waits until the stack is ready. It creates nothing: a realm
// without any of the containers has no stack, and found is false then. A
// stack that lacks some of its containers is an error, as it cannot get
// ready; EnsureStack creates them again.
func (m *Manager) Start(ctx context.Context) (found bool, err error) {
	var missing []string
	for _, s := range startOrder {
		name := ContainerName(m.Realm, s)
		info, err := docker.IfExists(m.Docker.InspectContainer(ctx, name))
		if err != nil {
			return found, fmt.Errorf("checking %s container: %w", s, err)
		}
		if info == nil {
			missing = append(missing, string(name))
			continue
		}
		found = true
		if info.Status == docker.StateRunning {
			continue
		}
		sindlog.From(ctx).InfoContext(ctx, "starting monitoring container", "container", string(name), "state", string(info.Status))
		if err := m.Docker.StartContainer(ctx, name); err != nil {
			return found, fmt.Errorf("starting %s container: %w", s, err)
		}
	}
	if !found {
		return false, nil
	}
	if len(missing) > 0 {
		return true, fmt.Errorf("the monitoring stack lacks %s: creating a monitored cluster creates it again", strings.Join(missing, ", "))
	}
	return true, m.WaitReady(ctx)
}

// create creates a service's container at its pinned address and copies
// its configuration into it.
func (m *Manager) create(ctx context.Context, s Service, dnsIP, fp, alloyConfig string) error {
	ip := ""
	if n := s.pinnedOffset(); n > 0 && dnsIP != "" {
		ip = mesh.ServiceIP(dnsIP, n)
	}
	if _, err := m.Docker.CreateContainer(ctx, m.createArgs(s, ip, fp, m.Pull)...); err != nil {
		return fmt.Errorf("creating %s container: %w", s, err)
	}
	dir, files := serviceFiles(m.Realm, s, alloyConfig)
	if err := m.Docker.CopyFilesToContainer(ctx, ContainerName(m.Realm, s), dir, files); err != nil {
		return fmt.Errorf("writing %s configuration: %w", s, err)
	}
	return nil
}

// readyPath is the path of a service's readiness endpoint.
func (s Service) readyPath() string {
	switch s {
	case ServiceGrafana:
		return "/api/health"
	case ServiceLoki:
		return "/ready"
	default:
		return "/-/ready"
	}
}

// URL returns the address of a service's endpoint path on the mesh
// network, by container name.
func (m *Manager) URL(s Service, p string) string {
	return "http://" + string(ContainerName(m.Realm, s)) + ":" + s.Port() + p
}

// curl runs curl in the Grafana container, the one image of the four with
// curl, which reaches the others by container name.
func (m *Manager) curl(ctx context.Context, args ...string) (string, error) {
	cmd := append([]string{"curl", "-fsS", "--max-time", "5"}, args...)
	return m.Docker.Exec(ctx, ContainerName(m.Realm, ServiceGrafana), cmd...)
}

// ServiceReady checks that a service's container runs and that its
// readiness endpoint answers with success, through Grafana's curl. A
// stopped or dead container is a probe.TerminalError.
func (m *Manager) ServiceReady(ctx context.Context, s Service) error {
	if err := probe.ContainerRunning(ctx, m.Docker, ContainerName(m.Realm, s)); err != nil {
		return err
	}
	if _, err := m.curl(ctx, "-o", "/dev/null", m.URL(s, s.readyPath())); err != nil {
		return fmt.Errorf("%s not ready: %w", s, err)
	}
	return nil
}

// WaitReady waits until every service of the stack is ready (see
// ServiceReady), Grafana first, as the others are probed through it.
func (m *Manager) WaitReady(ctx context.Context) error {
	timeout := m.ReadyTimeout
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	probes := make([]probe.Probe, 0, len(Services))
	for _, s := range Services {
		probes = append(probes, probe.Probe{
			Name: string(s),
			Check: func(ctx context.Context, _ *docker.Client, _ docker.ContainerName) error {
				return m.ServiceReady(ctx, s)
			},
		})
	}
	if err := probe.UntilReady(ctx, m.Docker, ContainerName(m.Realm, ServiceGrafana), probes, m.Interval); err != nil {
		return fmt.Errorf("waiting for the monitoring stack: %w", err)
	}
	return nil
}

// Sync writes Alloy's configuration and has Alloy reload it.
func (m *Manager) Sync(ctx context.Context, alloyConfig string) error {
	dir, files := serviceFiles(m.Realm, ServiceAlloy, alloyConfig)
	if err := m.Docker.CopyFilesToContainer(ctx, ContainerName(m.Realm, ServiceAlloy), dir, files); err != nil {
		return fmt.Errorf("writing Alloy configuration: %w", err)
	}
	if _, err := m.curl(ctx, "-X", "POST", "-o", "/dev/null", m.URL(ServiceAlloy, "/-/reload")); err != nil {
		return fmt.Errorf("reloading Alloy configuration: %w", err)
	}
	return nil
}

// journalScript empties each directory it is given, creating it, with
// reset as its first argument, and removes them otherwise.
const journalScript = `op=$1; shift
for d; do
  rm -rf -- "$d" || exit
  if [ "$op" = reset ]; then mkdir -p -- "$d" || exit; fi
done`

// journalOp runs journalScript in the Alloy container on the directories of
// nodes of a cluster in the journal volume, or on the cluster's directory
// when nodes is empty.
func (m *Manager) journalOp(ctx context.Context, op, cluster string, nodes []string) error {
	dirs := []string{path.Join(JournalDir, cluster)}
	if len(nodes) > 0 {
		dirs = dirs[:0]
		for _, n := range nodes {
			dirs = append(dirs, path.Join(JournalDir, JournalSubpath(cluster, n)))
		}
	}
	cmd := append([]string{"sh", "-c", journalScript, "sh", op}, dirs...)
	_, err := m.Docker.Exec(ctx, ContainerName(m.Realm, ServiceAlloy), cmd...)
	return err
}

// ResetJournals empties the journal directories of nodes of a cluster,
// creating them, so that a node's container can mount its directory and
// shows no journal of an earlier node of its name.
func (m *Manager) ResetJournals(ctx context.Context, cluster string, nodes ...string) error {
	if err := m.journalOp(ctx, "reset", cluster, nodes); err != nil {
		return fmt.Errorf("resetting journal directories: %w", err)
	}
	return nil
}

// RemoveJournals removes the journal directories of nodes of a cluster, or
// the cluster's whole directory when no node is given.
func (m *Manager) RemoveJournals(ctx context.Context, cluster string, nodes ...string) error {
	if err := m.journalOp(ctx, "remove", cluster, nodes); err != nil {
		return fmt.Errorf("removing journal directories: %w", err)
	}
	return nil
}

// Remove removes the stack's containers, keeping their volumes. It finds
// them by their labels first, so that a realm without a stack costs one
// docker call.
func (m *Manager) Remove(ctx context.Context) error {
	containers, err := m.containers(ctx)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	for _, s := range Services {
		name := ContainerName(m.Realm, s)
		if !slices.ContainsFunc(containers, func(c docker.ContainerListEntry) bool { return c.Name == name }) {
			continue
		}
		g.Go(func() error {
			if err := docker.IgnoreNotFound(m.Docker.RemoveContainer(gctx, name)); err != nil {
				return fmt.Errorf("removing %s container: %w", s, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// containers lists the stack's containers by their labels: the mesh's
// Compose project and LabelVersion.
func (m *Manager) containers(ctx context.Context) ([]docker.ContainerListEntry, error) {
	containers, err := m.Docker.ListContainers(ctx,
		"label="+docker.ComposeProjectLabel+"="+m.composeProject(),
		"label="+LabelVersion)
	if err != nil {
		return nil, fmt.Errorf("listing the monitoring containers: %w", err)
	}
	return containers, nil
}

// RemoveVolumes removes the stack's data volumes and the journal volume,
// once nothing mounts them. It finds them by their Compose project first.
func (m *Manager) RemoveVolumes(ctx context.Context) error {
	existing, err := m.Docker.ListVolumes(ctx, "label="+docker.ComposeProjectLabel+"="+m.composeProject())
	if err != nil {
		return fmt.Errorf("listing the monitoring volumes: %w", err)
	}
	volumes := []docker.VolumeName{JournalVolume(m.Realm)}
	for _, s := range Services {
		volumes = append(volumes, VolumeName(m.Realm, s))
	}
	for _, v := range volumes {
		if !slices.ContainsFunc(existing, func(e docker.VolumeListEntry) bool { return e.Name == v }) {
			continue
		}
		if err := docker.IgnoreNotFound(m.Docker.RemoveVolume(ctx, v)); err != nil {
			return fmt.Errorf("removing volume %s: %w", v, err)
		}
	}
	return nil
}

// ServiceStatus is the state of one of the stack's services.
type ServiceStatus struct {
	Service   Service               `json:"service"`
	Container docker.ContainerName  `json:"container"`
	State     docker.ContainerState `json:"status"` // empty when the container does not exist
	Image     string                `json:"image"`
	IP        string                `json:"ip"`
	DNSName   string                `json:"dns_name,omitempty"` // empty for Alloy, which has none
	Port      string                `json:"port"`
	Ready     bool                  `json:"ready"`
	Checked   bool                  `json:"checked"` // whether Ready was checked, which needs Grafana running
}

// Status returns the state of each of the stack's services, in the order
// of Services, and whether any of their containers exists. A running
// service is ready when ServiceReady passes; that check runs in Grafana,
// so without Grafana running none is checked. Like Remove,
// it lists the stack's containers first, so that a realm without a stack
// costs one docker call, and inspects those that exist in one more.
func (m *Manager) Status(ctx context.Context) ([]ServiceStatus, bool, error) {
	statuses := make([]ServiceStatus, len(Services))
	for i, s := range Services {
		statuses[i] = ServiceStatus{Service: s, Container: ContainerName(m.Realm, s), Image: s.Image(), Port: s.Port()}
		if s != ServiceAlloy {
			statuses[i].DNSName = DNSName(m.Realm, s)
		}
	}
	containers, err := m.containers(ctx)
	if err != nil {
		return nil, false, err
	}
	var names []docker.ContainerName
	for _, st := range statuses {
		if slices.ContainsFunc(containers, func(c docker.ContainerListEntry) bool { return c.Name == st.Container }) {
			names = append(names, st.Container)
		}
	}
	found := len(names) > 0
	if found {
		infos, err := m.Docker.InspectContainers(ctx, names...)
		if err != nil {
			return nil, false, fmt.Errorf("inspecting the monitoring stack: %w", err)
		}
		for _, info := range infos {
			i := slices.IndexFunc(statuses, func(st ServiceStatus) bool { return st.Container == info.Name })
			statuses[i].State = info.Status
			statuses[i].Image = info.ImageRef
			statuses[i].IP = info.IPs[m.network()]
		}
	}
	if !found {
		return statuses, false, nil
	}
	grafanaUp := statuses[0].State == docker.StateRunning
	g, gctx := errgroup.WithContext(ctx)
	for i := range statuses {
		if !grafanaUp || statuses[i].State != docker.StateRunning {
			continue
		}
		g.Go(func() error {
			s := statuses[i].Service
			_, err := m.curl(gctx, "-o", "/dev/null", m.URL(s, s.readyPath()))
			statuses[i].Ready = err == nil
			statuses[i].Checked = true
			return nil
		})
	}
	_ = g.Wait() // the checks report through Ready
	return statuses, true, nil
}
