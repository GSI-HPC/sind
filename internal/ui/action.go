// SPDX-License-Identifier: LGPL-3.0-or-later

package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/afero"

	"github.com/GSI-HPC/sind/internal/atomicfile"
	"github.com/GSI-HPC/sind/internal/ui/action"
	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/pkg/docker"
	"github.com/GSI-HPC/sind/pkg/slurm"
	"github.com/GSI-HPC/sind/pkg/state"
)

// Every change that sind ui makes is a sind command that a dialog shows and
// a child process runs (internal/ui/run). A dialog's form is posted twice:
// to /actions/ID/preview as it changes, which answers with the command it
// would run, and to /actions/ID/run, which builds the command again from the
// same fields and runs that. The browser never sends a command line, only
// field values, and the second build must give the command the first one
// showed (the field "shown"), or nothing runs.

// actKey is the key of the runner and its settings in Env.
type actKey struct{}

// actState is what the routes of the actions share.
type actState struct {
	starter run.Starter
	// home and configDir are the Context of the actions.
	home, configDir string
	// writeFile saves a file of a command, readable by its owner alone.
	writeFile func(path string, data []byte) error
	// stat looks at a directory that a command needs; mkdirAll makes it.
	stat     func(name string) (fs.FileInfo, error)
	mkdirAll func(path string) error
	// images knows the Slurm releases of images; nil knows none.
	images *actImages
	// workers knows the sizes of worker containers; nil knows none.
	workers *actWorkers
}

// The seams of Setup; tests replace them.
var (
	actExecutable  = os.Executable
	actStateDir    = state.Dir
	actActivityLog = activityLogForChildren
	actHome        = os.UserHomeDir
	actEnviron     = os.Environ
)

// actGrace is how long sind ui, when it stops, waits for the commands it
// started to roll back before it kills them.
const actGrace = 60 * time.Second

// activityLogForChildren returns the path of the default activity log, which
// it makes ready as sind does for its own commands; where that fails, the
// path alone.
func activityLogForChildren() (string, error) {
	if path, err := state.PrepareActivityLog(); err == nil {
		return path, nil
	}
	return state.ActivityLogPath()
}

// actSaveFile writes data to path for its owner alone, creating the
// directory for the owner alone too.
func actSaveFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(afero.NewOsFs(), path, data, 0o600)
}

// actMkdirAll makes a directory that a command needs, such as the data
// directory of a new cluster, as mkdir -p does.
func actMkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// actDockerImages returns the images of client, which runs docker.
func actDockerImages(client *docker.Client) *actImages {
	return newActImages(client.InspectImage, func(ctx context.Context, image string) (string, error) {
		return slurm.DiscoverVersion(ctx, client, image)
	})
}

func init() { defaultRegistry.AddService(actService()) }

// actService makes the runner of the actions. The commands run in the
// running sind binary; when sind ui stops, they get a SIGINT, and a minute
// to roll back.
func actService() Service {
	return Service{
		Name: "act",
		Setup: func(env *Env) error {
			exe, err := actExecutable()
			if err != nil {
				return fmt.Errorf("finding the sind executable: %w", err)
			}
			stateDir, err := actStateDir()
			if err != nil {
				return fmt.Errorf("finding the state directory: %w", err)
			}
			// Neither is needed to run a command: sind has its own defaults.
			logPath, _ := actActivityLog()
			home, _ := actHome()
			r := run.New(run.Config{
				Exec: env.Exec, Store: env.Store, Binary: exe, ActivityLog: logPath, Environ: actEnviron,
				Refresh: func(realm string) {
					if refresh, ok := env.Get("collect.refresh").(func(string)); ok {
						refresh(realm)
					}
				},
			})
			client := docker.NewClient(env.Exec)
			env.Put(actKey{}, &actState{
				starter: r, home: home, configDir: filepath.Join(stateDir, "ui", "configs"), writeFile: actSaveFile,
				stat: os.Stat, mkdirAll: actMkdirAll, images: actDockerImages(client), workers: newActWorkers(client.InspectContainer),
			})
			return nil
		},
		Run: func(ctx context.Context, env *Env) {
			<-ctx.Done()
			if st, ok := env.Get(actKey{}).(*actState); ok {
				st.starter.Shutdown(actGrace)
			}
		},
	}
}

func init() {
	for _, rt := range actRoutes() {
		defaultRegistry.AddRoute(rt)
	}
}

// actRoutes are the routes of the dialogs, the actions and the tasks they
// start.
func actRoutes() []Route {
	return []Route{
		{Pattern: "GET /dialogs/{action}", Handle: actDialogRoute},
		{Pattern: "POST /dialogs/{action}", Handle: actDialogRoute},
		{Pattern: "POST /actions/{action}/preview", Handle: actPreviewRoute},
		{Pattern: "POST /actions/{action}/run", Handle: actRunRoute},
		{Pattern: "POST /actions/{action}/check", Handle: actCheckRoute},
		{Pattern: "POST /tasks/{id}/cancel", Handle: actCancelRoute},
		{Pattern: "POST /tasks/{id}/retry", Handle: actRetryRoute},
	}
}

// actStateOf returns the state that Setup made, or fails the request.
func actStateOf(s *Server, w http.ResponseWriter, r *http.Request) (*actState, bool) {
	st, ok := s.Env().Get(actKey{}).(*actState)
	if !ok {
		s.Fail(w, r, errors.New("the runner of the actions is not set up"))
	}
	return st, ok
}

// actLookup returns the action of the request, or answers 404.
func actLookup(s *Server, w http.ResponseWriter, r *http.Request) (action.Action, bool) {
	a, ok := action.Lookup(r.PathValue("action"))
	if !ok {
		s.Fail(w, r, fmt.Errorf("action %s: %w", r.PathValue("action"), ErrNotFound))
	}
	return a, ok
}

// actCancelRoute asks a running task to stop, as Ctrl-C does: sind rolls
// back and ends with exit code 130. The dock and the run page show the rest.
func actCancelRoute(s *Server, w http.ResponseWriter, r *http.Request) {
	st, ok := actStateOf(s, w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, ok := s.Store().Snapshot().Task(id); !ok {
		s.Fail(w, r, fmt.Errorf("task %s: %w", id, ErrNotFound))
		return
	}
	switch err := st.starter.Cancel(id); {
	case errors.Is(err, run.ErrNotRunning):
		http.Error(w, "this command is not running", http.StatusConflict)
	case err != nil:
		s.Fail(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
