// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"cmp"
	"context"

	"github.com/GSI-HPC/sind/internal/ui/collect"
	"github.com/GSI-HPC/sind/internal/ui/demo"
)

// demoServices returns the services of a demo: those of the registry, with
// the collectors and the runner of the actions replaced by the demo's. The
// demo fills the store itself and its simulator plays the commands, so no
// code of the real services knows of it: they are left out, or, as the
// routes need the state the runner's Setup makes, given a Setup of the
// demo's.
func demoServices(d *demo.Demo, registered []Service) []Service {
	out := make([]Service, 0, len(registered)+1)
	for _, svc := range registered {
		switch svc.Name {
		case "collect", "collect-activity":
			continue
		case "act":
			svc.Setup = func(env *Env) error {
				env.Put(actKey{}, &actState{
					starter: d.Starter(), home: demo.Home, configDir: demo.ConfigDir, writeFile: d.SaveFile,
					stat: demo.Stat, mkdirAll: d.MkdirAll,
				})
				return nil
			}
		}
		out = append(out, svc)
	}
	return append(out, Service{
		Name: "demo",
		Setup: func(env *Env) error {
			env.Put(ColRefreshKey, d.Refresh)
			env.Put(ColDoctorKey, d.RequestDoctor)
			// The demo reads no stacks, but its picker works.
			env.Put(ColMonitoringIntervalKey, collect.NewInterval(cmp.Or(env.Opts.MonitoringInterval, collect.MonitoringEvery)))
			d.Setup()
			return nil
		},
		Run: func(ctx context.Context, _ *Env) { d.Run(ctx) },
	})
}
