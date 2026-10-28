// SPDX-License-Identifier: LGPL-3.0-or-later

package demo

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GSI-HPC/go-clikit/progress"
	"github.com/GSI-HPC/go-nodeset"

	"github.com/GSI-HPC/sind/internal/ui/activity"
	"github.com/GSI-HPC/sind/internal/ui/run"
	"github.com/GSI-HPC/sind/internal/ui/store"
	"github.com/GSI-HPC/sind/pkg/config"
	"github.com/GSI-HPC/sind/pkg/mesh"
	"github.com/GSI-HPC/sind/pkg/state"
)

// Sim plays the commands of the dialogs instead of running them. It is a
// run.Starter: Start makes a task as the real runner does, plays the command
// as the spans of a go-clikit Bus whose event log goes through the real
// reducer into the store, and changes the world as the command would change
// Docker.
type Sim struct {
	st  *store.Store
	w   *World
	clk clock
	// frozen refuses every command: the still demo plays nothing.
	frozen bool

	mu      sync.Mutex
	red     *activity.Reducer
	seq     int
	files   map[string][]byte
	running map[string]context.CancelFunc
	// busy holds the realms whose lock a command holds, with its command.
	busy map[string]string
	wg   sync.WaitGroup
}

// NewSim returns a simulator for the world, which writes to st.
func NewSim(st *store.Store, w *World, clk clock) *Sim {
	return &Sim{
		st: st, w: w, clk: clk, red: activity.NewReducer(200),
		files: map[string][]byte{}, running: map[string]context.CancelFunc{}, busy: map[string]string{},
	}
}

// SaveFile keeps a file of a command, as the dialogs save the config of a
// cluster before they run create cluster. The demo keeps it in memory.
func (s *Sim) SaveFile(path string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = bytes.Clone(data)
	return nil
}

// Feed reads lines of an event log, as the activity follower does, and
// sets the runs of the store.
func (s *Sim) Feed(log []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range bytes.Split(bytes.TrimSuffix(log, []byte("\n")), []byte("\n")) {
		s.red.Take(line)
	}
	s.st.SetRuns(s.red.Runs(), s.red.Changed()...)
}

// logSink is the writer of a run's event log.
type logSink struct{ s *Sim }

func (l logSink) Write(b []byte) (int, error) {
	l.s.Feed(b)
	return len(b), nil
}

// plan is a command to play.
type plan struct {
	title string
	// target is what the command works on, as sind names it.
	target string
	realm  string
	work   func(ctx context.Context, p play) error
	// done is what a command that succeeded leaves besides its nodes.
	done func()
	// silent says that the command writes no event log and takes no lock,
	// as sind exec.
	silent bool
}

// emitter returns the function that applies what a script does to the
// world, and publishes it.
func (s *Sim) emitter(realm string, configure func(*clusterState)) func(string, node) {
	return func(op string, n node) {
		s.w.apply(realm, op, n)
		if op == opCreate && configure != nil {
			s.w.configure(realm, n.cluster, configure)
		}
		s.w.Publish(s.st)
	}
}

// Start implements run.Starter.
func (s *Sim) Start(spec run.Spec) (string, error) {
	if s.frozen {
		return "", errors.New("the still demo runs no commands")
	}
	if len(spec.Argv) == 0 || spec.Argv[0] != "sind" {
		return "", errors.New("not a sind command")
	}
	p, err := s.plan(spec.Argv[1:])
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.seq++
	n := s.seq
	s.mu.Unlock()
	tc := uiTrace(n)
	t := store.Task{
		ID: fmt.Sprintf("%016x", 0x7a5c000000000000+n), Action: spec.Action, Display: spec.Display, Argv: spec.Argv, Realm: spec.Realm, Cluster: spec.Cluster,
		Form: spec.Form, Fixes: spec.Fixes, ConfigPath: spec.ConfigPath, Config: spec.Config, Trace: traceHex(tc), Start: s.clk.Now(), Status: store.TaskRunning,
	}
	s.st.AddTask(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.running[t.ID] = cancel
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		s.exec(ctx, t, tc, runID(n), p)
	}()
	return t.ID, nil
}

// exec plays a command as the child of the runner would run it.
func (s *Sim) exec(ctx context.Context, t store.Task, tc progress.TraceContext, id string, p plan) {
	var err error
	if p.silent {
		// A command that writes no event log, such as sind exec, and takes
		// no lock.
		err = p.work(ctx, play{clk: s.clk, emit: s.emitter(p.realm, nil)})
	} else {
		log := progress.NewLog(logSink{s}, progress.LogOptions{Program: "sind", Version: version, RunID: id, AdvanceEvery: 250 * time.Millisecond, FlushEvery: 25 * time.Millisecond})
		bus := progress.NewBus(progress.BusOptions{Sinks: []progress.Sink{log}, Now: s.clk.Now, Trace: tc, Program: "sind"})
		cctx, command := progress.Start(progress.WithBus(ctx, bus), progress.KindCommand, p.title, targetOf(p.target)...)
		pl := play{clk: s.clk, emit: s.emitter(p.realm, nil)}
		err = s.locked(cctx, pl, p.realm, t.Display, func() error { return p.work(cctx, pl) })
		if err == nil && p.done != nil {
			p.done()
			s.w.Publish(s.st)
		}
		command.End(err)
		bus.Close()
		// The log writes to the simulator, which does not fail.
		_ = log.Close()
	}

	s.mu.Lock()
	delete(s.running, t.ID)
	s.mu.Unlock()
	end := s.clk.Now()
	s.st.UpdateTask(t.ID, func(t *store.Task) {
		t.End = &end
		switch {
		case err == nil:
			t.Status = store.TaskOK
		case errors.Is(err, context.Canceled):
			t.Status, t.Exit = store.TaskCanceled, run.ExitInterrupted
		default:
			t.Status, t.Exit, t.Err = store.TaskFailed, 1, err.Error()
			t.Output = "Error: " + err.Error() + "\n"
		}
	})
}

// locked runs work with the lock of the realm, as sind's commands do: a
// command that finds it held waits, and says whose it is.
func (s *Sim) locked(ctx context.Context, p play, realm, who string, work func() error) error {
	s.mu.Lock()
	holder, held := s.busy[realm]
	s.mu.Unlock()
	if held {
		_, wait := progress.Start(ctx, progress.KindWait, "realm lock", progress.Message(holder))
		for held {
			if err := p.clk.Sleep(ctx, 500*time.Millisecond); err != nil {
				wait.End(stopped)
				return stopped
			}
			s.mu.Lock()
			_, held = s.busy[realm]
			s.mu.Unlock()
		}
		wait.End(nil)
	}
	s.mu.Lock()
	s.busy[realm] = who
	n := len(s.busy)
	s.mu.Unlock()
	s.st.SetLock(realm, &state.LockHolder{Command: who, Host: "demo", PID: 4200 + n, Since: s.clk.Now()})
	defer func() {
		s.mu.Lock()
		delete(s.busy, realm)
		s.mu.Unlock()
		s.st.SetLock(realm, nil)
	}()
	return work()
}

// Cancel implements run.Starter: it ends the command as Ctrl-C does.
func (s *Sim) Cancel(id string) error {
	s.mu.Lock()
	cancel, ok := s.running[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("task %s: %w", id, run.ErrNotRunning)
	}
	s.st.UpdateTask(id, func(t *store.Task) { t.Canceling = true })
	cancel()
	return nil
}

// Running returns how many commands run.
func (s *Sim) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

// Shutdown implements run.Starter: it interrupts the commands and waits for
// them up to grace.
func (s *Sim) Shutdown(grace time.Duration) {
	s.mu.Lock()
	for _, cancel := range s.running {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
	}
}

// ready is how long the nodes of a new cluster take to get ready: the first
// controller is quick, the workers come one after the other.
func ready(role string, i int) time.Duration {
	switch role {
	case "controller":
		return 14 * time.Second
	case "worker":
		return time.Duration(19+3*(i%6)) * time.Second
	default:
		return time.Duration(15+i) * time.Second
	}
}

// valueFlags are the flags of the commands the dialogs build that take a
// value as a word of their own.
var valueFlags = []string{"--config", "--data", "--wait", "--count", "--cpus", "--memory"}

// positional returns the words of args that are no flags and no flag
// values.
func positional(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch {
		case slices.Contains(valueFlags, args[i]):
			i++
		case !strings.HasPrefix(args[i], "-"):
			out = append(out, args[i])
		}
	}
	return out
}

// flagValue returns the value of the flag, or "".
func flagValue(args []string, name string) string {
	if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// clusterWord returns the cluster the words name, the default cluster for
// none.
func clusterWord(words []string) string {
	if len(words) > 0 {
		return words[0]
	}
	return config.DefaultClusterName
}

// scontrol plays what the scontrol update of a drain or a resume, which
// the power dialog runs through sind exec with args, does to the nodes it
// names: "[CLUSTER] -- scontrol update NodeName=a,b State=DRAIN
// Reason=why". It does nothing to any other command.
func scontrol(p play, args []string) {
	i := slices.Index(args, "--")
	if i < 0 || len(args) < i+5 || args[i+1] != "scontrol" || args[i+2] != "update" {
		return
	}
	n := node{cluster: clusterWord(positional(args[:i]))}
	names := ""
	op := ""
	for _, a := range args[i+3:] {
		k, v, _ := strings.Cut(a, "=")
		switch strings.ToLower(k) {
		case "nodename":
			names = v
		case "state":
			op = map[string]string{"drain": opDrain, "resume": opResume}[strings.ToLower(v)]
		case "reason":
			n.reason = v
		}
	}
	if op == "" {
		return
	}
	for _, name := range strings.Split(names, ",") {
		n.name = name
		p.do(op, n)
	}
}

// systemctl plays what the restart of a service, which the restart dialog
// runs through sind ssh with args, does to the node it names:
// "NODE[.CLUSTER] -- systemctl restart UNIT". It does nothing to any other
// command.
func systemctl(p play, args []string) {
	i := slices.Index(args, "--")
	if i != 1 || len(args) != 5 || args[2] != "systemctl" || args[3] != "restart" {
		return
	}
	short, cl, _ := strings.Cut(args[0], ".")
	p.do(opRestart, node{name: short, cluster: cmp.Or(cl, config.DefaultClusterName), unit: args[4]})
}

// plan reads the command line of a command that a dialog built and makes
// the script that plays it. A command it does not know plays as one step.
func (s *Sim) plan(argv []string) (plan, error) {
	realm := mesh.DefaultRealm
	if len(argv) >= 2 && argv[0] == "--realm" {
		realm, argv = argv[1], argv[2:]
	}
	if len(argv) > 0 && argv[0] == "exec" {
		// sind exec runs a command in a node, such as the scontrol of a
		// drain, and draws no progress.
		return plan{title: "exec", realm: realm, silent: true, work: func(ctx context.Context, p play) error {
			if err := p.clk.Sleep(ctx, 600*time.Millisecond); err != nil {
				return err
			}
			scontrol(p, argv[1:])
			return nil
		}}, nil
	}
	if len(argv) > 0 && argv[0] == "ssh" {
		// sind ssh runs a command in a node, such as the systemctl restart
		// of a service, and draws no progress either.
		return plan{title: "ssh", realm: realm, silent: true, work: func(ctx context.Context, p play) error {
			if err := p.clk.Sleep(ctx, 900*time.Millisecond); err != nil {
				return err
			}
			systemctl(p, argv[1:])
			return nil
		}}, nil
	}
	if len(argv) < 2 {
		return plan{title: strings.Join(argv, " "), realm: realm, work: func(ctx context.Context, p play) error {
			return p.generic(ctx, "run")
		}}, nil
	}
	title := argv[0] + " " + argv[1]
	args := argv[2:]
	words := positional(args)
	p := plan{title: title, realm: realm}
	switch title {
	case "create cluster":
		return s.planCreateCluster(p, args)
	case "create worker":
		p.target = clusterWord(words)
		p.work = s.planCreateWorker(realm, p.target, args)
	case "delete cluster":
		p.target = clusterWord(words)
		p.work = s.planDeleteCluster(realm, p.target)
	case "delete realm":
		p.work = s.planDeleteRealm(realm)
	case "delete worker":
		p.work = s.planNodes(realm, words, func(ctx context.Context, pl play, nodes []node) error { return pl.deleteWorkers(ctx, nodes) })
	case "power shutdown", "power on", "power reboot", "power cycle", "power cut", "power freeze", "power unfreeze":
		op := argv[1]
		p.work = s.planNodes(realm, words, func(ctx context.Context, pl play, nodes []node) error { return pl.power(ctx, op, nodes) })
	case "connect container", "disconnect container":
		p.work = planAttach(argv[0], words)
	default:
		p.work = func(ctx context.Context, pl play) error { return pl.generic(ctx, argv[1]) }
	}
	return p, nil
}

// planCreateCluster plays create cluster from the config the dialog saved.
func (s *Sim) planCreateCluster(p plan, args []string) (plan, error) {
	path := flagValue(args, "--config")
	s.mu.Lock()
	data, ok := s.files[path]
	s.mu.Unlock()
	if !ok {
		return plan{}, fmt.Errorf("config %s: not saved", path)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return plan{}, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.ApplyDefaults()
	p.target = cfg.Name
	if cfg.Realm != "" {
		p.realm = cfg.Realm
	}
	spec := createSpec{image: cfg.Defaults.Image, mesh: true}
	if strings.HasSuffix(spec.image, ":broken") {
		spec.pullFail = fmt.Errorf("pull access denied for %s, repository does not exist or may require 'docker login'", strings.TrimSuffix(spec.image, ":broken"))
	}
	workers := 0
	for _, n := range cfg.Nodes {
		role := string(n.Role)
		switch n.Role {
		case config.RoleWorker:
			for range max(n.Count, 1) {
				spec.nodes = append(spec.nodes, node{name: "worker-" + strconv.Itoa(workers), cluster: cfg.Name, role: role, ready: ready(role, workers)})
				workers++
			}
		default:
			spec.nodes = append(spec.nodes, node{name: role, cluster: cfg.Name, role: role, ready: ready(role, 0)})
			if n.BackupController {
				spec.nodes = append(spec.nodes, node{name: "controller-backup", cluster: cfg.Name, role: role, ready: ready(role, 1)})
			}
		}
	}
	slurm := slurmNew
	if strings.Contains(spec.image, slurmOld) {
		slurm = slurmOld
	}
	data2 := flagValue(args, "--data")
	switch ds := cfg.Storage.DataStorage; {
	case ds.Type == config.StorageVolume:
		data2 = "volume"
	case ds.HostPath != "":
		data2 = ds.HostPath
	}
	users := make([]string, len(cfg.Users))
	for i, u := range cfg.Users {
		users[i] = u.Name
	}
	configure := func(c *clusterState) {
		c.slurm, c.monitoring, c.users = slurm, cfg.Monitoring, users
		c.volume, c.dataPath = data2 == "volume", data2
		if c.volume {
			c.dataPath = p.realm + "-" + cfg.Name + "-data"
		}
	}
	realm := p.realm
	p.work = func(ctx context.Context, pl play) error {
		pl.emit = s.emitter(realm, configure)
		return pl.create(ctx, spec)
	}
	if cfg.Monitoring {
		p.done = func() { s.w.apply(realm, opMonitor, node{}) }
	}
	return p, nil
}

// planCreateWorker plays create worker: the next workers of the cluster.
func (s *Sim) planCreateWorker(realm, name string, args []string) func(context.Context, play) error {
	count := 1
	if n, err := strconv.Atoi(flagValue(args, "--count")); err == nil && n > 0 {
		count = n
	}
	return func(ctx context.Context, p play) error {
		next := 0
		for _, n := range s.w.nodesOf(realm, name) {
			if i, ok := strings.CutPrefix(n.name, "worker-"); ok {
				if v, err := strconv.Atoi(i); err == nil && v >= next {
					next = v + 1
				}
			}
		}
		var nodes []node
		for i := range count {
			nodes = append(nodes, node{name: "worker-" + strconv.Itoa(next+i), cluster: name, role: "worker", ready: ready("worker", i)})
		}
		return p.create(ctx, createSpec{nodes: nodes})
	}
}

// planDeleteCluster plays delete cluster.
func (s *Sim) planDeleteCluster(realm, name string) func(context.Context, play) error {
	return func(ctx context.Context, p play) error {
		return p.remove(ctx, s.w.nodesOf(realm, name), true, len(s.w.clustersOf(realm)) == 1)
	}
}

// planDeleteRealm plays delete realm: every cluster of the realm goes.
func (s *Sim) planDeleteRealm(realm string) func(context.Context, play) error {
	return func(ctx context.Context, p play) error {
		var nodes []node
		for _, c := range s.w.clustersOf(realm) {
			nodes = append(nodes, s.w.nodesOf(realm, c)...)
		}
		return p.remove(ctx, nodes, true, true)
	}
}

// planNodes plays a command on the nodes its words name.
func (s *Sim) planNodes(realm string, words []string, do func(context.Context, play, []node) error) func(context.Context, play) error {
	return func(ctx context.Context, p play) error {
		var nodes []node
		for _, w := range words {
			set, err := nodeset.Parse(w)
			if err != nil {
				return fmt.Errorf("node %s: %w", w, err)
			}
			for _, name := range set.Expand() {
				short, cl, _ := strings.Cut(name, ".")
				if cl == "" {
					cl = config.DefaultClusterName
				}
				i := slices.IndexFunc(s.w.nodesOf(realm, cl), func(n node) bool { return n.name == short })
				if i < 0 {
					return fmt.Errorf("no node %s in cluster %s", short, cl)
				}
				nodes = append(nodes, s.w.nodesOf(realm, cl)[i])
			}
		}
		return do(ctx, p, nodes)
	}
}

// planAttach plays connect container and disconnect container.
func planAttach(verb string, words []string) func(context.Context, play) error {
	op := opConnect
	if verb == "disconnect" {
		op = opDisconn
	}
	name := ""
	if len(words) > 0 {
		name = words[0]
	}
	return func(ctx context.Context, p play) error {
		return p.attach(ctx, op, node{name: name})
	}
}
