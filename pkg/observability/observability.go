// SPDX-License-Identifier: LGPL-3.0-or-later

// Package observability manages a realm's monitoring stack: Grafana Alloy,
// which collects the Slurm metrics, the container resource use and the
// journals of the monitored clusters' nodes, Prometheus and Loki, which
// store them, and Grafana, which shows them. The stack runs as four
// containers on the realm's mesh network. The package generates their
// configuration from the nodes it is given; pkg/cluster decides which
// nodes those are and when the stack is created, synced and removed.
//
// It is not called monitor: pkg/monitor is the Docker and systemd event
// watcher of the readiness probes.
package observability

import (
	"github.com/GSI-HPC/sind/pkg/docker"
)

// The stack's images, each pinned and bumped on its own. Grafana must be
// the standard image: its Prometheus and Loki datasources ship only in the
// standard image's bundled plugins, and the -slim image would fetch them
// at start. The standard image is also the only one of the four with curl,
// which sind runs there to reach the others.
const (
	AlloyImage      = "grafana/alloy:v1.20.1"
	PrometheusImage = "prom/prometheus:v3.15.0"
	LokiImage       = "grafana/loki:3.7.8"
	GrafanaImage    = "grafana/grafana:13.2.3"
)

// Images returns the stack's images, in the order of its services.
func Images() []string {
	return []string{GrafanaImage, PrometheusImage, LokiImage, AlloyImage}
}

// Service is one of the stack's containers.
type Service string

// The stack's services, which name their containers and volumes
// (<realm>-<service>) and their Compose services.
const (
	ServiceGrafana    Service = "grafana"
	ServicePrometheus Service = "prometheus"
	ServiceLoki       Service = "loki"
	ServiceAlloy      Service = "alloy"
)

// Services lists the stack's services in the order sind get monitoring
// shows them.
var Services = []Service{ServiceGrafana, ServicePrometheus, ServiceLoki, ServiceAlloy}

// Ports the services listen on, on the mesh network.
const (
	GrafanaPort    = "3000"
	PrometheusPort = "9090"
	LokiPort       = "3100"
	AlloyPort      = "12345"
)

// serviceSpec is what sets a service's container apart.
type serviceSpec struct {
	image string
	port  string // the port it listens on, on the mesh network
	// pinnedOffset is how far below the mesh DNS address the service's
	// pinned address is (see mesh.ServiceIP), 0 for Alloy, which has a
	// dynamic one and no DNS name.
	pinnedOffset int
	memoryLimit  string   // the --memory cap of its container
	dataDir      string   // where it keeps its data, on its volume
	readyPath    string   // the path of its readiness endpoint
	args         []string // its command's arguments, after its image
}

// specs are the services' specs.
var specs = map[Service]serviceSpec{
	ServiceGrafana:    {image: GrafanaImage, port: GrafanaPort, pinnedOffset: 1, memoryLimit: "1g", dataDir: "/var/lib/grafana", readyPath: "/api/health"},
	ServicePrometheus: {image: PrometheusImage, port: PrometheusPort, pinnedOffset: 2, memoryLimit: "1g", dataDir: "/prometheus", readyPath: "/-/ready", args: prometheusArgs},
	ServiceLoki:       {image: LokiImage, port: LokiPort, pinnedOffset: 3, memoryLimit: "1g", dataDir: "/loki", readyPath: "/ready", args: lokiArgs},
	ServiceAlloy:      {image: AlloyImage, port: AlloyPort, memoryLimit: "512m", dataDir: "/var/lib/alloy/data", readyPath: "/-/ready", args: alloyArgs},
}

// Port returns the port a service listens on.
func (s Service) Port() string { return specs[s].port }

// Image returns the image a service runs.
func (s Service) Image() string { return specs[s].image }

// ContainerName returns the container of a service in a realm:
// <realm>-<service>. Two-part names never equal a cluster's
// <realm>-<cluster>-<type> resources.
func ContainerName(realm string, s Service) docker.ContainerName {
	return docker.ContainerName(realm + "-" + string(s))
}

// VolumeName returns the data volume of a service in a realm.
func VolumeName(realm string, s Service) docker.VolumeName {
	return docker.VolumeName(realm + "-" + string(s))
}

// JournalVolume returns the realm's journal volume, where each monitored
// node writes its journal into a directory of its own,
// <cluster>/<node>, and Alloy reads them.
func JournalVolume(realm string) docker.VolumeName {
	return docker.VolumeName(realm + "-journal")
}

// JournalDir is where Alloy mounts the journal volume.
const JournalDir = "/var/log/sind"

// NodeJournalDir is where a monitored node mounts its directory of the
// journal volume: journald writes a persistent journal there.
const NodeJournalDir = "/var/log/journal"

// JournalSubpath returns a node's directory in the journal volume.
func JournalSubpath(cluster, node string) string {
	return cluster + "/" + node
}

// LabelVersion is the label of the stack's containers that records the
// fingerprint of the stack sind created them with (see Fingerprint): its
// images, configuration and dashboards. A create that finds another
// fingerprint recreates the containers, keeping their volumes.
const LabelVersion = "sind.observability.version"

// DNSName returns the mesh DNS name of a service: <service>.<realm>.sind.
func DNSName(realm string, s Service) string {
	return string(s) + "." + realm + ".sind"
}
