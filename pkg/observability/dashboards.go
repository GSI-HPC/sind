// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import "encoding/json"

// The dashboards are built here rather than kept as exported JSON: they
// share their variables, links and label scheme, and a Grafana export
// would carry thousands of lines of defaults. Grafana fills in what a
// panel leaves out and migrates the schema version on load.

// dashboardSchemaVersion is the Grafana dashboard schema the dashboards
// are written against.
const dashboardSchemaVersion = 39

// dashboardTag is the tag of every sind dashboard, which their link menu
// lists.
const dashboardTag = "sind"

// Dashboard UIDs, which links between the dashboards use.
const (
	uidCluster = "sind-slurm-cluster"
	uidNodes   = "sind-nodes"
	uidHealth  = "sind-service-health"
	uidUsers   = "sind-users-accounts"
	uidLogs    = "sind-logs"
)

// dashboard is a Grafana dashboard.
type dashboard struct {
	uid, title, description string
	vars                    []variable
	panels                  []panel
}

// variable is a dashboard template variable.
type variable struct {
	name, label string
	loki        bool   // a Loki label's values, else a Prometheus label's
	metric      string // the series (Prometheus) or stream selector (Loki) the values come from
	labelName   string
	multi       bool // several values and All, else exactly one
	textbox     bool // a free-text filter instead of label values
}

// panel is a dashboard panel. A panel with no queries and no type is a
// row header.
type panel struct {
	kind, title, description string
	w, h                     int
	unit                     string
	queries                  []query
	stack                    bool // stack the series of a time series
	mappings                 []any
	thresholds               []any
	links                    []any
	options                  map[string]any
}

// query is a panel's query.
type query struct {
	expr, legend string
	loki         bool
	instant      bool // the latest value only, for stats and tables
}

// rowHeight is the height of a row header.
const rowHeight = 1

// gridWidth is Grafana's grid width.
const gridWidth = 24

// datasource returns the datasource reference of a query.
func datasource(loki bool) map[string]any {
	if loki {
		return map[string]any{"type": "loki", "uid": LokiUID}
	}
	return map[string]any{"type": "prometheus", "uid": PrometheusUID}
}

// json returns the dashboard as Grafana's JSON model: panels laid out left
// to right, wrapping at the grid width, a row header on a line of its own.
func (d dashboard) json() []byte {
	var (
		panels []any
		x, y   int
		lineH  int
	)
	for i, p := range d.panels {
		if p.kind == "row" {
			if x > 0 {
				y += lineH
				x, lineH = 0, 0
			}
			panels = append(panels, map[string]any{
				"id": i + 1, "type": "row", "title": p.title, "collapsed": false, "panels": []any{},
				"gridPos": map[string]any{"x": 0, "y": y, "w": gridWidth, "h": rowHeight},
			})
			y += rowHeight
			continue
		}
		if x+p.w > gridWidth {
			y += lineH
			x, lineH = 0, 0
		}
		panels = append(panels, p.json(i+1, x, y))
		x += p.w
		lineH = max(lineH, p.h)
	}

	vars := make([]any, 0, len(d.vars))
	for _, v := range d.vars {
		vars = append(vars, v.json())
	}

	model := map[string]any{
		"uid":           d.uid,
		"title":         d.title,
		"description":   d.description,
		"tags":          []string{dashboardTag},
		"schemaVersion": dashboardSchemaVersion,
		"editable":      true,
		"graphTooltip":  1,
		"refresh":       "30s",
		"time":          map[string]any{"from": "now-1h", "to": "now"},
		"timezone":      "browser",
		"templating":    map[string]any{"list": vars},
		"annotations":   map[string]any{"list": []any{}},
		"links": []any{map[string]any{
			"title": "sind", "type": "dashboards", "tags": []string{dashboardTag},
			"asDropdown": true, "includeVars": true, "keepTime": true,
		}},
		"panels": panels,
	}
	out, _ := json.MarshalIndent(model, "", "  ") // maps of strings, numbers and slices always marshal
	return append(out, '\n')
}

// json returns a panel's JSON model at a grid position.
func (p panel) json(id, x, y int) map[string]any {
	targets := make([]any, 0, len(p.queries))
	loki := false
	for i, q := range p.queries {
		loki = q.loki
		t := map[string]any{
			"refId":      string(rune('A' + i)),
			"datasource": datasource(q.loki),
			"expr":       q.expr,
		}
		if q.legend != "" {
			t["legendFormat"] = q.legend
		}
		if q.instant {
			t["instant"] = true
			t["range"] = false
			if !q.loki {
				t["format"] = "table"
			}
		} else if q.loki && p.kind == "logs" {
			t["queryType"] = "range"
		}
		targets = append(targets, t)
	}

	defaults := map[string]any{}
	if p.unit != "" {
		defaults["unit"] = p.unit
	}
	if p.mappings != nil {
		defaults["mappings"] = p.mappings
	}
	if p.thresholds != nil {
		defaults["thresholds"] = map[string]any{"mode": "absolute", "steps": p.thresholds}
		defaults["color"] = map[string]any{"mode": "thresholds"}
	}
	if p.links != nil {
		defaults["links"] = p.links
	}
	if p.kind == "timeseries" {
		custom := map[string]any{"fillOpacity": 10, "showPoints": "never"}
		if p.stack {
			custom["stacking"] = map[string]any{"mode": "normal"}
			custom["fillOpacity"] = 40
		}
		defaults["custom"] = custom
	}

	m := map[string]any{
		"id":          id,
		"type":        p.kind,
		"title":       p.title,
		"datasource":  datasource(loki),
		"gridPos":     map[string]any{"x": x, "y": y, "w": p.w, "h": p.h},
		"targets":     targets,
		"fieldConfig": map[string]any{"defaults": defaults, "overrides": []any{}},
	}
	if p.description != "" {
		m["description"] = p.description
	}
	if p.options != nil {
		m["options"] = p.options
	}
	return m
}

// json returns a variable's JSON model.
func (v variable) json() map[string]any {
	m := map[string]any{"name": v.name, "label": v.label}
	if v.textbox {
		m["type"] = "textbox"
		m["query"] = ""
		m["current"] = map[string]any{"text": "", "value": ""}
		return m
	}
	m["type"] = "query"
	m["refresh"] = 2 // on time range change, so new clusters and nodes show up
	m["sort"] = 1
	m["datasource"] = datasource(v.loki)
	if v.loki {
		m["query"] = map[string]any{"type": 1, "label": v.labelName, "stream": v.metric, "refId": "LokiVariableQueryEditor-VariableQuery"}
	} else {
		m["query"] = map[string]any{"query": "label_values(" + v.metric + ", " + v.labelName + ")", "refId": "PrometheusVariableQueryEditor-VariableQuery"}
		m["definition"] = "label_values(" + v.metric + ", " + v.labelName + ")"
	}
	if v.multi {
		m["multi"] = true
		m["includeAll"] = true
		m["allValue"] = ".*"
		m["current"] = map[string]any{"text": []string{"All"}, "value": []string{"$__all"}}
	}
	return m
}

// Selectors the panels share. Every query is scoped to the realm and
// cluster variables; realm is the one stack's realm, but it keeps the
// queries right when a dashboard is copied to another Grafana.
const (
	selCluster = `realm="$realm", cluster="$cluster"`
	selNodes   = selCluster + `, node=~"$node"`
)

// sel returns a series selector of a metric with extra matchers.
func sel(metric, matchers string) string {
	return metric + "{" + matchers + "}"
}

// realmVar and clusterVar are the variables every dashboard starts with.
func realmVar() variable {
	return variable{name: "realm", label: "Realm", metric: "up", labelName: "realm"}
}

func clusterVar(metric string) variable {
	return variable{name: "cluster", label: "Cluster", metric: metric + `{realm="$realm"}`, labelName: "cluster"}
}

func nodeVar(metric string) variable {
	return variable{name: "node", label: "Node", metric: metric + `{realm="$realm", cluster="$cluster"}`, labelName: "node", multi: true}
}

// logsLink links a panel's series to the Logs dashboard, filtered to the
// series' node and the panel's time range.
func logsLink(nodeLabel string) []any {
	return []any{map[string]any{
		"title": "Logs of ${__field.labels." + nodeLabel + "}",
		"url": "/d/" + uidLogs + "/logs?var-realm=${realm}&var-cluster=${cluster}&var-node=${__field.labels." + nodeLabel +
			"}&${__url_time_range}",
	}}
}

// upDown maps 1 and 0 to up and down, green and red.
var upDown = []any{map[string]any{"type": "value", "options": map[string]any{
	"0": map[string]any{"text": "down", "color": "red"},
	"1": map[string]any{"text": "up", "color": "green"},
}}}

// redFrom colours a value green below and red from a threshold.
func redFrom(n float64) []any {
	return []any{map[string]any{"color": "green", "value": nil}, map[string]any{"color": "red", "value": n}}
}

// helpers for the common panel kinds.

func row(title string) panel { return panel{kind: "row", title: title} }

func stat(title, expr, unit string, w int) panel {
	return panel{kind: "stat", title: title, w: w, h: 4, unit: unit, queries: []query{{expr: expr, instant: true}},
		options: map[string]any{"reduceOptions": map[string]any{"calcs": []string{"lastNotNull"}}, "graphMode": "none", "colorMode": "value"}}
}

func series(title, unit string, w int, queries ...query) panel {
	return panel{kind: "timeseries", title: title, w: w, h: 8, unit: unit, queries: queries}
}

func pq(expr, legend string) query { return query{expr: expr, legend: legend} }

func logs(title, expr string, w, h int) panel {
	return panel{kind: "logs", title: title, w: w, h: h, queries: []query{{expr: expr, loki: true}},
		options: map[string]any{"showTime": true, "wrapLogMessage": true, "sortOrder": "Descending", "enableLogDetails": true, "dedupStrategy": "none"}}
}

// rate returns the per-second rate of a counter over Grafana's rate
// interval.
func rate(s string) string { return "rate(" + s + "[$__rate_interval])" }

// slurmJobStates are the job states the dashboards plot, with Slurm's
// metric suffix.
var slurmJobStates = []string{"running", "pending", "completing", "suspended", "hold"}

// slurmNodeStates are the node states and flags the dashboards plot.
var slurmNodeStates = []string{"idle", "mixed", "alloc", "completing", "down", "drain", "noresp"}

// clusterDashboard is the home dashboard: the cluster as Slurm sees it.
func clusterDashboard() dashboard {
	var jobs, nodes []query
	for _, s := range slurmJobStates {
		jobs = append(jobs, pq(sel("slurm_jobs_"+s, selCluster), s))
	}
	for _, s := range slurmNodeStates {
		nodes = append(nodes, pq(sel("slurm_nodes_"+s, selCluster), s))
	}
	jobsPanel := series("Jobs by state", "short", 12, jobs...)
	jobsPanel.stack = true
	part := func(metric string) string { return sel(metric, selCluster) }
	memAlloc := series("Memory allocated", "mbytes", 12,
		pq(`sum(`+sel("slurm_node_memory_alloc_bytes", selCluster)+`)`, "allocated"),
		pq(`sum(`+sel("slurm_node_memory_effective_bytes", selCluster)+`)`, "allocatable"))
	memAlloc.description = memoryMiB
	return dashboard{
		uid: uidCluster, title: "Slurm cluster",
		description: "The cluster as slurmctld reports it through its OpenMetrics endpoints.",
		vars:        []variable{realmVar(), clusterVar("slurm_nodes")},
		panels: []panel{
			{
				kind: "stat", title: "Controller in control", w: 4, h: 4,
				description: "The controller whose slurmctld answers the metrics scrape. A standby slurmctld redirects it, so its scrape is down.",
				queries:     []query{{expr: `max by (node) (up{` + selCluster + `, job="slurm_nodes"}) == 1`, legend: "{{node}}", instant: true}},
				options:     map[string]any{"reduceOptions": map[string]any{"calcs": []string{"lastNotNull"}, "fields": "/^node$/"}, "graphMode": "none", "colorMode": "none"},
			},
			stat("Nodes", sel("slurm_nodes", selCluster), "short", 4),
			stat("Idle nodes", sel("slurm_nodes_idle", selCluster), "short", 4),
			withThresholds(stat("Down or draining nodes", sel("slurm_nodes_down", selCluster)+" + "+sel("slurm_nodes_drain", selCluster), "short", 4), redFrom(1)),
			stat("Running jobs", sel("slurm_jobs_running", selCluster), "short", 4),
			stat("Pending jobs", sel("slurm_jobs_pending", selCluster), "short", 4),

			row("Jobs and nodes"),
			jobsPanel,
			series("Nodes by state", "short", 12, nodes...),
			series("CPUs allocated", "short", 12,
				pq(`sum(`+sel("slurm_node_cpus_alloc", selCluster)+`)`, "allocated"),
				pq(`sum(`+sel("slurm_node_cpus_effective", selCluster)+`)`, "allocatable")),
			memAlloc,
			row("Partitions"),
			series("Pending jobs per partition", "short", 8, pq(part("slurm_partition_jobs_pending"), "{{partition}}")),
			series("Running jobs per partition", "short", 8, pq(part("slurm_partition_jobs_running"), "{{partition}}")),
			series("Allocated CPUs per partition", "percentunit", 8,
				pq(part("slurm_partition_nodes_cpus_alloc")+" / "+part("slurm_partition_nodes_cpus_efctv"), "{{partition}}")),

			row("Scheduler"),
			series("Scheduling cycle", "µs", 8,
				pq(sel("slurm_schedule_cycle_last", selCluster), "main, last"),
				pq(sel("slurm_bf_cycle_last", selCluster), "backfill, last")),
			series("Scheduler queues", "short", 8,
				pq(sel("slurm_schedule_queue_len", selCluster), "main queue"),
				pq(sel("slurm_bf_queue_len", selCluster), "backfill queue"),
				pq(sel("slurm_agent_queue_size", selCluster), "RPC retry queue"),
				pq(sel("slurm_slurmdbd_queue_size", selCluster), "slurmdbd queue")),
			series("slurmctld threads", "short", 8,
				pq(sel("slurm_server_thread_cnt", selCluster), "server"),
				pq(sel("slurm_agent_thread_cnt", selCluster), "agent")),
		},
	}
}

// withThresholds sets a panel's thresholds.
func withThresholds(p panel, steps []any) panel {
	p.thresholds = steps
	return p
}

// memoryMiB explains the unit of Slurm's memory metrics: slurmctld reports
// RealMemory and allocations in MiB, as slurm.conf states them, whatever
// the _bytes in their names says.
const memoryMiB = "Slurm reports memory in MiB, as slurm.conf states RealMemory."

// nodesDashboard puts Slurm's per-node allocation next to what each node's
// container and units really use.
func nodesDashboard() dashboard {
	// node is a target label of the scrape, so Slurm's own node label
	// becomes exported_node; the node's own cgroup has no cgroup label.
	nodeCgroup := selNodes + `, cgroup=""`
	units := selNodes + `, unit!=""`
	cpu := series("CPU use", "short", 12, pq(`sum by (node) (`+rate(sel("container_cpu_usage_seconds_total", nodeCgroup))+`)`, "{{node}}"))
	cpu.description = "CPU seconds per second of each node's container."
	cpu.links = logsLink("node")
	mem := series("Memory use", "bytes", 12, pq(`sum by (node) (`+sel("container_memory_working_set_bytes", nodeCgroup)+`)`, "{{node}}"))
	mem.links = logsLink("node")
	slurmCPU := series("CPUs allocated by Slurm", "short", 12,
		pq(sel("slurm_node_cpus_alloc", selCluster+`, exported_node=~"$node"`), "{{exported_node}} allocated"),
		pq(sel("slurm_node_cpus", selCluster+`, exported_node=~"$node"`), "{{exported_node}} total"))
	slurmCPU.links = logsLink("exported_node")
	slurmMem := series("Memory allocated by Slurm", "mbytes", 12,
		pq(sel("slurm_node_memory_alloc_bytes", selCluster+`, exported_node=~"$node"`), "{{exported_node}} allocated"),
		pq(sel("slurm_node_memory_bytes", selCluster+`, exported_node=~"$node"`), "{{exported_node}} total"))
	slurmMem.links = logsLink("exported_node")
	slurmMem.description = memoryMiB
	return dashboard{
		uid: uidNodes, title: "Nodes",
		description: "Each node's resource use, from its container's cgroups, next to what Slurm has allocated on it. Slurm reports allocation per node and states per cluster, not per node.",
		vars:        []variable{realmVar(), clusterVar("container_memory_working_set_bytes"), nodeVar("container_memory_working_set_bytes")},
		panels: []panel{
			row("Containers"),
			cpu, mem,
			series("Processes", "short", 12, pq(`sum by (node) (`+sel("container_processes", nodeCgroup)+`)`, "{{node}}")),
			series("Disk I/O", "Bps", 12,
				pq(`sum by (node) (`+rate(sel("container_fs_reads_bytes_total", nodeCgroup))+`)`, "{{node}} read"),
				pq(`sum by (node) (`+rate(sel("container_fs_writes_bytes_total", nodeCgroup))+`)`, "{{node}} write")),
			row("Slurm"),
			slurmCPU, slurmMem,
			row("Units"),
			series("CPU use per unit", "short", 12, pq(`sum by (node, unit) (`+rate(sel("container_cpu_usage_seconds_total", units))+`)`, "{{node}} {{unit}}")),
			series("Memory use per unit", "bytes", 12, pq(`sum by (node, unit) (`+sel("container_memory_working_set_bytes", units)+`)`, "{{node}} {{unit}}")),
		},
	}
}

// healthDashboard shows the Slurm daemons' probes and the errors in the
// logs.
func healthDashboard() dashboard {
	matrix := panel{
		kind: "state-timeline", title: "Slurm daemons", w: 24, h: 8, mappings: upDown,
		description: "slurmctld's /livez and /readyz, slurmd's /readyz, and a TCP connect to slurmdbd, every 15 s.",
		queries:     []query{pq(sel("probe_success", selNodes), "{{node}} {{service}} {{check}}")},
		links:       logsLink("node"),
		options:     map[string]any{"showValue": "never", "mergeValues": true, "rowHeight": 0.8},
	}
	inControl := panel{
		kind: "state-timeline", title: "Controller in control", w: 12, h: 6, mappings: upDown,
		description: "Whether each controller's slurmctld answers the metrics scrape. A standby redirects it, so only the controller in control is up.",
		queries:     []query{pq(`max by (node) (up{`+selCluster+`, job="slurm_nodes"})`, "{{node}}")},
		options:     map[string]any{"showValue": "never", "mergeValues": true},
	}
	levels := `level=~"emerg|alert|crit|err|warning"`
	rates := series("Errors and warnings per unit", "short", 12, query{
		expr:   `sum by (node, unit, level) (count_over_time({` + selNodes + `, ` + levels + `} [$__auto]))`,
		legend: "{{node}} {{unit}} {{level}}", loki: true,
	})
	return dashboard{
		uid: uidHealth, title: "Service health",
		description: "Slurm daemon probes and the latest errors and warnings, Slurm, MariaDB and systemd alike.",
		vars:        []variable{realmVar(), clusterVar("probe_success"), nodeVar("probe_success")},
		panels: []panel{
			matrix,
			inControl,
			series("Probe duration", "s", 12, pq(sel("probe_duration_seconds", selNodes), "{{node}} {{service}} {{check}}")),
			rates,
			logs("Latest errors and warnings", `{`+selNodes+`, `+levels+`}`, 12, 8),
		},
	}
}

// usersDashboard shows the jobs per user and account.
func usersDashboard() dashboard {
	var panels []panel
	for _, by := range []struct{ prefix, label, title string }{
		{"slurm_user", "username", "user"},
		{"slurm_account", "account", "account"},
	} {
		legend := "{{" + by.label + "}}"
		running := series("Running jobs per "+by.title, "short", 8, pq(sel(by.prefix+"_jobs_running", selCluster), legend))
		pending := series("Pending jobs per "+by.title, "short", 8, pq(sel(by.prefix+"_jobs_pending", selCluster), legend))
		cpus := series("CPUs allocated per "+by.title, "short", 8, pq(sel(by.prefix+"_jobs_cpus_alloc", selCluster), legend))
		for _, p := range []*panel{&running, &pending, &cpus} {
			p.stack = true
		}
		panels = append(panels, row("Per "+by.title), running, pending, cpus)
	}
	jobCgroup := selCluster + `, cgroup=~".*/job_[0-9]+"`
	panels = append(panels,
		row("Per job"),
		series("CPU use per job", "short", 12, pq(`sum by (job_id) (`+rate(sel("container_cpu_usage_seconds_total", jobCgroup))+`)`, "job {{job_id}}")),
		series("Memory use per job", "bytes", 12, pq(`sum by (job_id) (`+sel("container_memory_working_set_bytes", jobCgroup)+`)`, "job {{job_id}}")),
	)
	return dashboard{
		uid: uidUsers, title: "Users and accounts",
		description: "Jobs per user and account, from slurmctld, and each running job's resource use, from its cgroup on the worker nodes. Users are the names slurmctld resolves for job UIDs.",
		vars:        []variable{realmVar(), clusterVar("slurm_nodes")},
		panels:      panels,
	}
}

// logsDashboard is the journal browser the other dashboards link into.
func logsDashboard() dashboard {
	stream := `{` + selNodes + `, unit=~"$unit", level=~"$level"}`
	filter := ` |~ "(?i)$search"`
	volume := series("Log volume", "short", 24,
		query{expr: `sum by (level) (count_over_time(` + stream + filter + ` [$__auto]))`, legend: "{{level}}", loki: true})
	volume.h = 6
	volume.stack = true
	lokiVar := func(name, label, labelName string) variable {
		return variable{name: name, label: label, loki: true, metric: `{realm="$realm", cluster="$cluster"}`, labelName: labelName, multi: true}
	}
	return dashboard{
		uid: uidLogs, title: "Logs",
		description: "The nodes' journals: every unit, Slurm daemons, MariaDB and systemd, with the level Slurm and MariaDB put in their messages.",
		vars: []variable{
			{name: "realm", label: "Realm", loki: true, metric: `{realm=~".+"}`, labelName: "realm"},
			{name: "cluster", label: "Cluster", loki: true, metric: `{realm="$realm"}`, labelName: "cluster"},
			lokiVar("node", "Node", "node"),
			lokiVar("unit", "Unit", "unit"),
			lokiVar("level", "Level", "level"),
			{name: "search", label: "Search", textbox: true},
		},
		panels: []panel{
			volume,
			logs("Journal", stream+filter, 24, 20),
		},
	}
}

// dashboards are the sind dashboards, by file name.
var dashboards = map[string]func() dashboard{
	"slurm-cluster.json":  clusterDashboard,
	"nodes.json":          nodesDashboard,
	"service-health.json": healthDashboard,
	"users-accounts.json": usersDashboard,
	"logs.json":           logsDashboard,
}

// Dashboards returns the sind dashboards' JSON models by file name.
func Dashboards() map[string][]byte {
	out := make(map[string][]byte, len(dashboards))
	for name, d := range dashboards {
		out[name] = d().json()
	}
	return out
}
