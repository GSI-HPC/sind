// SPDX-License-Identifier: LGPL-3.0-or-later

package observability

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grafanaDashboard is the part of a dashboard's JSON model the tests read.
type grafanaDashboard struct {
	UID        string   `json:"uid"`
	Title      string   `json:"title"`
	Tags       []string `json:"tags"`
	Templating struct {
		List []struct {
			Name       string         `json:"name"`
			Type       string         `json:"type"`
			Datasource map[string]any `json:"datasource"`
		} `json:"list"`
	} `json:"templating"`
	Panels []struct {
		ID         int                      `json:"id"`
		Type       string                   `json:"type"`
		Title      string                   `json:"title"`
		Datasource map[string]string        `json:"datasource"`
		GridPos    struct{ X, Y, W, H int } `json:"gridPos"`
		Targets    []struct {
			RefID      string            `json:"refId"`
			Expr       string            `json:"expr"`
			Datasource map[string]string `json:"datasource"`
		} `json:"targets"`
		FieldConfig struct {
			Defaults struct {
				Links []struct {
					URL string `json:"url"`
				} `json:"links"`
			} `json:"defaults"`
		} `json:"fieldConfig"`
	} `json:"panels"`
}

// variableRef matches a dashboard variable in a query or link.
var variableRef = regexp.MustCompile(`\$\{?([a-z_]+)`)

// builtinVariables are Grafana's own variables.
var builtinVariables = []string{"__rate_interval", "__auto", "__url_time_range", "__field", "__all"}

func TestDashboards(t *testing.T) {
	all := Dashboards()
	require.Len(t, all, 5)
	uids := map[string]bool{}
	for name, raw := range all {
		t.Run(name, func(t *testing.T) {
			var d grafanaDashboard
			require.NoError(t, json.Unmarshal(raw, &d))
			assert.True(t, strings.HasPrefix(d.UID, "sind-"))
			assert.False(t, uids[d.UID], "UID %s used twice", d.UID)
			uids[d.UID] = true
			assert.Equal(t, []string{dashboardTag}, d.Tags)
			assert.NotEmpty(t, d.Title)

			vars := map[string]bool{}
			for _, v := range d.Templating.List {
				vars[v.Name] = true
			}
			assert.True(t, vars["realm"] && vars["cluster"], "every dashboard has realm and cluster")

			ids := map[int]bool{}
			for _, p := range d.Panels {
				assert.False(t, ids[p.ID], "panel ID %d used twice", p.ID)
				ids[p.ID] = true
				assert.LessOrEqual(t, p.GridPos.X+p.GridPos.W, gridWidth, p.Title)
				assert.Positive(t, p.GridPos.W, p.Title)
				if p.Type == "row" {
					continue
				}
				require.NotEmpty(t, p.Targets, p.Title)
				for _, q := range p.Targets {
					uid := q.Datasource["uid"]
					assert.Contains(t, []string{PrometheusUID, LokiUID}, uid, p.Title)
					assert.Equal(t, uid, p.Datasource["uid"], "%s: a panel queries one datasource", p.Title)
					assert.Contains(t, q.Expr, `realm="$realm"`, p.Title)
					assert.Contains(t, q.Expr, `cluster="$cluster"`, p.Title)
					refs := q.Expr
					for _, l := range p.FieldConfig.Defaults.Links {
						refs += " " + l.URL
					}
					for _, m := range variableRef.FindAllStringSubmatch(refs, -1) {
						assert.True(t, vars[m[1]] || slices.Contains(builtinVariables, m[1]), "%s: unknown variable %s", p.Title, m[1])
					}
				}
			}
		})
	}
}

func TestDashboards_Layout(t *testing.T) {
	// Panels fill a line left to right and wrap; a row header starts a
	// line of its own.
	d := dashboard{uid: "x", panels: []panel{
		stat("a", "e", "", 16),
		stat("b", "e", "", 16),
		row("r"),
		stat("c", "e", "", 8),
		row("s"),
	}}
	var model grafanaDashboard
	require.NoError(t, json.Unmarshal(d.json(), &model))
	pos := func(i int) [2]int { return [2]int{model.Panels[i].GridPos.X, model.Panels[i].GridPos.Y} }
	assert.Equal(t, [2]int{0, 0}, pos(0))
	assert.Equal(t, [2]int{0, 4}, pos(1))
	assert.Equal(t, [2]int{0, 8}, pos(2))
	assert.Equal(t, [2]int{0, 9}, pos(3))
	assert.Equal(t, [2]int{0, 13}, pos(4))
}

func TestDashboards_Home(t *testing.T) {
	var d grafanaDashboard
	require.NoError(t, json.Unmarshal(Dashboards()["slurm-cluster.json"], &d))
	assert.Equal(t, uidCluster, d.UID)
	assert.True(t, strings.HasSuffix(homeDashboard, "/slurm-cluster.json"))
}

func TestDashboards_LinksToLogs(t *testing.T) {
	var d grafanaDashboard
	require.NoError(t, json.Unmarshal(Dashboards()["nodes.json"], &d))
	var links []string
	for _, p := range d.Panels {
		for _, l := range p.FieldConfig.Defaults.Links {
			links = append(links, l.URL)
		}
	}
	assert.Contains(t, links, "/d/sind-logs/logs?var-realm=${realm}&var-cluster=${cluster}&var-node=${__field.labels.node}&${__url_time_range}")
	assert.Contains(t, links, "/d/sind-logs/logs?var-realm=${realm}&var-cluster=${cluster}&var-node=${__field.labels.exported_node}&${__url_time_range}")
}
