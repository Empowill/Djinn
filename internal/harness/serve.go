package harness

// An ifrit's serves (T51): the processes an ifrit runs through Djinn rather than in the background of its shell, a dev
// server or a stack (djinn serve run). Djinn starts each in the ifrit's worktree, or the folder given, through the
// shell, in a process group of its own, in a systemd scope of its own named after the ifrit where Djinn has scopes;
// its output goes to a log in the data folder (servesDir). djinn serve run returns once its process lives and each
// port it declares answers: the ifrit's turn never waits on a serve. Every servePoll Djinn reads the ports its
// processes listen on (listeners) and checks those it declares (a port Docker publishes belongs to no process of
// it). A serve stops when its ifrit's run ends (done, stopped, out, or Djinn quitting), or by djinn serve stop: its
// stop command first, for serveStopLimit at most, for what lives outside its processes, like containers; then its
// process group asked to stop, killed Grace later, with its scope; then a probe connection to each of its ports, the
// lead told of one that still answers. A serve stopped with its ifrit starts again when the ifrit is lit again
// (Serve.relight), before its agent. Its process is recorded in run/ like any child: one a dead Djinn left is stopped
// at the next start, and its stop command run (sweep). What a serve may run follows the watchers' rule (serveAllowed).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	"github.com/empowill/djinn/internal/logx"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/store"
)

const (
	// servesDir is the folder of the data folder where the serves' logs go: serves/<task id>/<name>.log. Backups leave
	// it out.
	servesDir = "serves"
	// serveReady is how long djinn serve run waits for the ports a serve declares to answer: past it, the serve is
	// returned starting, and the next readings say once they answer.
	serveReady = 2 * time.Minute
	// serveSettle is how long djinn serve run watches a serve that declares no port before it returns: a command that
	// fails at once (a typo, a missing program) fails the call.
	serveSettle = 500 * time.Millisecond
	// serveStopLimit is how long a serve's stop command may run.
	serveStopLimit = time.Minute
	// servePoll is how often Djinn reads the ports of a running serve.
	servePoll = 5 * time.Second
	// methodServe is journaled as a serve starts, stops, or its ports change; the request is the task.
	methodServe = "harness/serve"
)

// ServeHandler returns the Connect handler of ServeService on h, and its path prefix.
func ServeHandler(h *Harness) (string, http.Handler) {
	return planv1connect.NewServeServiceHandler(&Serves{h: h}, connect.WithInterceptors(plan.WorkerGuard, plan.Validate))
}

// Serves implements ServeService.
type Serves struct {
	planv1connect.UnimplementedServeServiceHandler
	h *Harness
}

// ifritServes are the serves of an ifrit's run.
type ifritServes struct {
	r      *run                // the run they belong to
	closed bool                // the run ends: no serve starts any more
	live   map[string]*serving // by name, case ignored: running, ended by itself, or stopped by djinn serve stop
}

// serving is a serve Djinn runs for an ifrit.
type serving struct {
	r          *run
	code, name string // the ifrit's code; the serve's name
	dir        string // where it runs
	stop       string // its stop command; empty for none
	p          *process
	agent      int // the ifrit's agent, whose ports are never the serve's

	logPath string // its log file
	logMu   sync.Mutex
	log     io.WriteCloser // its log file open; nil once closed
	logged  chan struct{}  // closed once its output is all written

	giveOnce sync.Once
	give     func() // gives its gate back; nil without one

	quit chan struct{} // closed as Djinn stops it
	once sync.Once     // stops it once

	// Guarded by Harness.servesMu.
	serve *planv1.Serve // where it stands, as last recorded: replaced, never changed
	text  string        // what its stop said, once stopped
}

// serveParams is what a serve starts with.
type serveParams struct {
	name, command, directory, gate, stop string
	ports                                []*planv1.Port // declared
}

// Run starts a serve for the ifrit that asks: only an ifrit's worker runs one (WorkerGuard points the request at its
// own task).
func (s *Serves) Run(ctx context.Context, req *connect.Request[planv1.ServeServiceRunRequest]) (*connect.Response[planv1.ServeServiceRunResponse], error) {
	h, m := s.h, req.Msg
	if caller := plan.CallerFromContext(ctx); caller == nil || caller.TaskID != m.GetTaskId() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only an ifrit runs a serve, from its own worker"))
	}
	task, err := h.ifritTask(ctx, m.GetTaskId())
	if err != nil {
		return nil, err
	}
	r := h.litRun(task.GetId())
	if r == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("ifrit %s is not lit: it runs serves while it is", task.GetCode()))
	}
	ports, err := declaredPorts(m.GetPort())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ctx, cancel := context.WithTimeout(ctx, serveReady)
	defer cancel()
	sv, err := h.runServe(ctx, r, serveParams{
		name: m.GetName(), command: m.GetCommand(), directory: m.GetDirectory(), gate: m.GetGate(), stop: m.GetStop(), ports: ports,
	}, task.GetWorktree(), h.noteServe(r))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.ServeServiceRunResponse{Serve: sv}), nil
}

// List lists an ifrit's serves: as stored, those running as they stand now.
func (s *Serves) List(ctx context.Context, req *connect.Request[planv1.ServeServiceListRequest]) (*connect.Response[planv1.ServeServiceListResponse], error) {
	task, err := s.h.ifritTask(ctx, req.Msg.GetTaskId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&planv1.ServeServiceListResponse{Serves: s.h.servesOf(task)}), nil
}

// Stop stops a serve for good: it does not start again when its ifrit is lit again.
func (s *Serves) Stop(ctx context.Context, req *connect.Request[planv1.ServeServiceStopRequest]) (*connect.Response[planv1.ServeServiceStopResponse], error) {
	h, m := s.h, req.Msg
	task, err := h.ifritTask(ctx, m.GetTaskId())
	if err != nil {
		return nil, err
	}
	if sv := h.liveServe(task.GetId(), m.GetName()); sv != nil {
		final, text := h.stopServing(sv, "djinn serve stop", true)
		noteFinal(sv.r, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text, Serve: final})
		return connect.NewResponse(&planv1.ServeServiceStopResponse{Serve: final}), nil
	}
	// Not running: stopped with its ifrit, which is not lit. It no longer starts again when it is.
	var out *planv1.Serve
	err = h.store.Tx(ctx, func(tx *store.Tx) error {
		t, err := store.Get[*planv1.Task](ctx, tx, task.GetId())
		if err != nil {
			return err
		}
		at := serveAt(t.GetIfrit().GetServes(), m.GetName())
		if at < 0 {
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s has no serve %s", t.GetCode(), m.GetName()))
		}
		out = proto.CloneOf(t.GetIfrit().GetServes()[at])
		if !out.GetRelight() {
			return nil
		}
		if h.litRun(t.GetId()) != nil {
			return connect.NewError(connect.CodeUnavailable, fmt.Errorf("%s was lit meanwhile: stop the serve again", t.GetCode()))
		}
		out.Relight = false
		setServeIn(t, out)
		if err := tx.Journal(plan.CallerActor(ctx), req.Spec().Procedure, m); err != nil {
			return err
		}
		return tx.Put(t)
	})
	if err != nil {
		return nil, plan.Status(err)
	}
	h.notify()
	return connect.NewResponse(&planv1.ServeServiceStopResponse{Serve: out}), nil
}

// Logs prints the last lines a serve wrote, its stop command's included.
func (s *Serves) Logs(ctx context.Context, req *connect.Request[planv1.ServeServiceLogsRequest]) (*connect.Response[planv1.ServeServiceLogsResponse], error) {
	h, m := s.h, req.Msg
	task, err := h.ifritTask(ctx, m.GetTaskId())
	if err != nil {
		return nil, err
	}
	if serveAt(h.servesOf(task), m.GetName()) < 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("%s has no serve %s", task.GetCode(), m.GetName()))
	}
	last := int(m.GetLast())
	if last == 0 {
		last = 100
	}
	lines, err := tailLines(serveLog(h.home, task.GetId(), m.GetName()), last)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&planv1.ServeServiceLogsResponse{Lines: lines}), nil
}

// ifritTask is the task id, which must be an ifrit.
func (h *Harness) ifritTask(ctx context.Context, id string) (*planv1.Task, error) {
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("which ifrit? --task-id, or $DJINN_TASK_ID in its worker"))
	}
	task, err := store.Get[*planv1.Task](ctx, h.store, id)
	if err != nil {
		return nil, plan.Status(err)
	}
	if task.GetKind() != planv1.TaskKind_TASK_KIND_IFRIT {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s is no ifrit: only an ifrit runs serves", task.GetCode()))
	}
	return task, nil
}

// litRun is the run of the ifrit id while it is lit and goes on: nil otherwise.
func (h *Harness) litRun(id string) *run {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.runs[id]
	if r == nil || !r.ifrit || r.worker == nil || r.final || r.stopping {
		return nil
	}
	return r
}

// declaredPorts reads the ports a serve declares: 8080=api, or 8080.
func declaredPorts(specs []string) ([]*planv1.Port, error) {
	var ports []*planv1.Port
	for _, spec := range specs {
		number, label, _ := strings.Cut(spec, "=")
		n, err := strconv.ParseUint(number, 10, 32)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("--port %s: a port is a number from 1 to 65535, its name after =, like 8080=api", spec)
		}
		if slices.ContainsFunc(ports, func(p *planv1.Port) bool { return p.GetNumber() == uint32(n) }) {
			return nil, fmt.Errorf("--port %s: port %d is declared twice", spec, n)
		}
		ports = append(ports, &planv1.Port{Number: uint32(n), Label: label, Declared: true})
	}
	return ports, nil
}

// runServe starts the serve p of the ifrit of r, in its worktree unless p names a folder, and returns it once its
// process lives and the ports it declares answer, or once ctx ends: it is starting then. A serve of its name is
// stopped first. say records each change of it in the task.
func (h *Harness) runServe(ctx context.Context, r *run, p serveParams, worktree string, say func(Event)) (*planv1.Serve, error) {
	task := r.task
	if err := h.serveAllowed(ctx, task, p.command, p.stop); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	dir := cmpDir(p.directory, worktree)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("serve %s: no folder %s to run in", p.name, dir))
	}
	if old := h.liveServe(r.id, p.name); old != nil {
		final, text := h.stopServing(old, "started again", false)
		say(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text, Serve: final})
	}
	s := &serving{r: r, code: r.code, name: p.name, dir: dir, stop: p.stop, quit: make(chan struct{}), logged: make(chan struct{})}
	h.mu.Lock()
	s.agent = r.pid
	h.mu.Unlock()
	if p.gate != "" && h.gates != nil {
		give, err := h.gates(ctx, p.gate, r.id, p.command, dir, false, nil)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("serve %s: gate %s: %w", p.name, p.gate, err))
		}
		s.give = give
	}
	if err := h.startServing(s, p.command, task.GetId()); err != nil {
		s.giveGate()
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("serve %s: %w", p.name, err))
	}
	now := h.now()
	sv := &planv1.Serve{
		Name: p.name, Command: p.command, Directory: p.directory, Gate: p.gate, Stop: p.stop, Pid: int32(s.p.cmd.Process.Pid),
		State: planv1.ServeState_SERVE_STATE_STARTING, StartTime: timestamppb.New(now), Ports: p.ports, Relight: true,
	}
	if !h.addServing(r, s, sv) {
		h.stopServing(s, "its ifrit ended as it started", false)
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("ifrit %s ended: serve %s did not start", task.GetCode(), p.name))
	}
	where := ""
	if p.gate != "" {
		where = ", holding gate " + p.gate
	}
	say(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Serve: sv,
		Text: fmt.Sprintf("serve %s started: %s, pid %d, in %s%s", p.name, p.command, sv.GetPid(), dir, where)})
	ready, err := h.waitReady(ctx, s)
	if err != nil {
		final, text := h.stopServing(s, err.Error(), false)
		say(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text, Serve: final})
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("serve %s %w; its last lines:\n%s", p.name, err,
			strings.Join(s.lastLines(20), "\n")))
	}
	if ready != nil {
		say(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Serve: ready, Text: runsText(ready)})
		sv = ready
	}
	go h.watchServe(s)
	return proto.CloneOf(sv), nil
}

// cmpDir is dir, or else worktree.
func cmpDir(dir, worktree string) string {
	if dir != "" {
		return dir
	}
	return worktree
}

// startServing starts the process of s, command run through the shell, its output written to its log.
func (h *Harness) startServing(s *serving, command, taskID string) error {
	var log io.WriteCloser = nopWriteCloser{io.Discard}
	if h.home != "" {
		s.logPath = serveLog(h.home, taskID, s.name)
		if err := os.MkdirAll(filepath.Dir(s.logPath), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		log = f
	}
	s.log = log
	s.logLine(fmt.Sprintf("== %s started: %s", h.now().Format(time.RFC3339), command))
	var scope func() machine.Scope
	if h.scopes != nil {
		scope = func() machine.Scope { return h.scopes.NewWithMemory(s.code+"-"+s.name, 0) }
	}
	track := func(pid int, cgroup, mark string) func() {
		return h.children.trackServe(pid, cgroup, mark, s.code, s.stop, s.dir)
	}
	args := shellArgs(command)
	p, err := startCommand(s.dir, args[0], args[1:], nil, scope, Grace, true, false, track)
	if err != nil {
		s.closeLog()
		return err
	}
	s.p = p
	go func() {
		defer logx.Crash("serve output", "task", s.code, "serve", s.name)
		defer close(s.logged)
		for l := range p.lines {
			s.logLine(l.text)
		}
	}()
	return nil
}

// shellArgs runs command through the system's shell.
func shellArgs(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", command}
	}
	return []string{"/bin/sh", "-c", command}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// logLine writes a line to the log of s, while it is open.
func (s *serving) logLine(line string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.log != nil {
		_, _ = io.WriteString(s.log, line+"\n")
	}
}

// closeLog closes the log of s.
func (s *serving) closeLog() {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.log != nil {
		_ = s.log.Close()
		s.log = nil
	}
}

// lastLines are the last n lines of its log.
func (s *serving) lastLines(n int) []string {
	select {
	case <-s.logged: // All its output is written.
	case <-time.After(time.Second):
	}
	lines, _ := tailLines(s.logPath, n)
	return lines
}

// giveGate gives the gate of s back, once.
func (s *serving) giveGate() {
	s.giveOnce.Do(func() {
		if s.give != nil {
			s.give()
		}
	})
}

// serveLog is the log of the serve name of the task taskID in the data folder home.
func serveLog(home, taskID, name string) string {
	return filepath.Join(home, servesDir, taskID, strings.ToLower(name)+".log")
}

// tailLines reads the last n lines of the file path, oldest first: from its last 4 MiB at most.
func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const most = 4 << 20
	if info, err := f.Stat(); err == nil && info.Size() > most {
		if _, err := f.Seek(-most, io.SeekEnd); err != nil {
			return nil, err
		}
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), most)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > 2*n {
			lines = slices.Clone(lines[len(lines)-n:])
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, sc.Err()
}

// waitReady waits for s to be ready: each port it declares answers, or, declaring none, it still runs serveSettle
// after it started. It returns the serve running, nil once ctx ended first (still starting), or why it is not: its
// process ended.
func (h *Harness) waitReady(ctx context.Context, s *serving) (*planv1.Serve, error) {
	settle := time.NewTimer(serveSettle)
	defer settle.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	settled := false
	for {
		select {
		case <-s.p.done:
			return nil, fmt.Errorf("ended as it started: %s", exitText(s.p.res))
		case <-ctx.Done():
			return nil, nil
		case <-settle.C:
			settled = true
		case <-tick.C:
		}
		sv := h.readServe(s)
		declared := slices.ContainsFunc(sv.GetPorts(), (*planv1.Port).GetDeclared)
		if (declared || settled) && sv.GetState() == planv1.ServeState_SERVE_STATE_RUNNING {
			select {
			case <-s.p.done: // It ended meanwhile.
				continue
			default:
			}
			h.setServing(s, sv)
			return sv, nil
		}
	}
}

// exitText says how a process ended.
func exitText(res Result) string {
	if res.Err != nil {
		return res.Err.Error()
	}
	return fmt.Sprintf("exit code %d", res.ExitCode)
}

// readServe is s as its ports stand now: those its processes listen on, those it declares, and whether these answer.
// Running once every port it declares answers.
func (h *Harness) readServe(s *serving) *planv1.Serve {
	h.servesMu.Lock()
	sv := proto.CloneOf(s.serve)
	h.servesMu.Unlock()
	found := listeners(servePIDs(s.p), []int{os.Getpid(), s.agent})
	sv.Ports = mergePorts(sv.GetPorts(), found)
	sv.State = planv1.ServeState_SERVE_STATE_RUNNING
	for _, p := range sv.GetPorts() {
		if p.GetDeclared() && !p.GetOpen() {
			sv.State = planv1.ServeState_SERVE_STATE_STARTING
		}
	}
	return sv
}

// listener is a TCP port a process listens on.
type listener struct {
	address string // 127.0.0.1, ::1, 0.0.0.0 or ::
	port    uint32
}

// keptAddress tells whether a port listening on ip is shown: on the loopback, or on every interface.
func keptAddress(ip net.IP) bool { return ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) }

// mergePorts are the ports of a serve as they stand now: those it declares, open when one answers on 127.0.0.1,
// then those its processes listen on (found), by number. A port both declared and found keeps its name.
func mergePorts(was []*planv1.Port, found []listener) []*planv1.Port {
	var ports []*planv1.Port
	for _, p := range was {
		if !p.GetDeclared() {
			continue
		}
		p = proto.CloneOf(p)
		p.Address, p.Open = "", false
		if at := slices.IndexFunc(found, func(l listener) bool { return l.port == p.GetNumber() }); at >= 0 {
			p.Address, p.Open = found[at].address, true
		} else {
			p.Open = portAnswers("127.0.0.1", p.GetNumber())
		}
		ports = append(ports, p)
	}
	slices.SortFunc(found, func(a, b listener) int { return int(a.port) - int(b.port) })
	for _, l := range found {
		if slices.ContainsFunc(ports, func(p *planv1.Port) bool { return p.GetNumber() == l.port }) {
			continue
		}
		ports = append(ports, &planv1.Port{Number: l.port, Address: l.address, Open: true})
	}
	return ports
}

// portAnswers tells whether a connection to port on address opens, within half a second.
func portAnswers(address string, port uint32) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(address, strconv.Itoa(int(port))), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// runsText says that the serve sv runs, and where.
func runsText(sv *planv1.Serve) string {
	if len(sv.GetPorts()) == 0 {
		return fmt.Sprintf("serve %s runs, on no port yet", sv.GetName())
	}
	return fmt.Sprintf("serve %s runs: %s", sv.GetName(), portsText(sv.GetPorts()))
}

// portsText lists ports as links: http://localhost:5173/ (front), and those that do not answer.
func portsText(ports []*planv1.Port) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		s := fmt.Sprintf("http://localhost:%d/", p.GetNumber())
		if p.GetLabel() != "" {
			s += " (" + p.GetLabel() + ")"
		}
		if !p.GetOpen() {
			s += ", not answering"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// watchServe follows the running serve s until Djinn stops it: every servePoll its ports, and its end, should its
// process end by itself.
func (h *Harness) watchServe(s *serving) {
	defer logx.Crash("serve watch", "task", s.code, "serve", s.name)
	tick := time.NewTicker(servePoll)
	defer tick.Stop()
	say := h.noteServe(s.r)
	for {
		select {
		case <-s.quit:
			return
		case <-s.p.done:
			select {
			case <-s.quit:
				return
			default:
			}
			s.giveGate() // No gate for a serve that no longer runs.
			h.servesMu.Lock()
			sv := proto.CloneOf(s.serve)
			h.servesMu.Unlock()
			sv.State, sv.Pid = planv1.ServeState_SERVE_STATE_EXITED, 0
			for _, p := range sv.Ports {
				p.Open = false
			}
			h.setServing(s, sv)
			last := s.lastLines(1)
			text := fmt.Sprintf("serve %s ended by itself: %s", s.name, exitText(s.p.res))
			if len(last) > 0 {
				text += "; its last line: " + clipRunes(last[0], 200)
			}
			say(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text, Serve: sv})
			return
		case <-tick.C:
			h.servesMu.Lock()
			was := s.serve
			h.servesMu.Unlock()
			sv := h.readServe(s)
			if proto.Equal(sv, was) {
				continue
			}
			text := runsText(sv)
			if sv.GetState() == planv1.ServeState_SERVE_STATE_STARTING {
				text = fmt.Sprintf("serve %s starts: %s", s.name, portsText(sv.GetPorts()))
			}
			h.setServing(s, sv)
			say(Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text, Serve: sv})
		}
	}
}

// stopServing stops s, once: its stop command, then its processes and its scope, then its gate; a probe then tells
// whether its ports still answer, the lead told of those. forget says that it does not start again when its ifrit is
// lit again. It returns it stopped, and what its stop says.
func (h *Harness) stopServing(s *serving, why string, forget bool) (*planv1.Serve, string) {
	s.once.Do(func() {
		close(s.quit)
		exited := false // Its process ended by itself before.
		select {
		case <-s.p.done:
			exited = true
		default:
		}
		var notes []string
		if s.stop != "" {
			s.logLine(fmt.Sprintf("== %s stop command: %s", h.now().Format(time.RFC3339), s.stop))
			ctx, cancel := context.WithTimeout(context.Background(), serveStopLimit)
			_, code, err := runTracked(ctx, s.dir, shellArgs(s.stop), h.children.tracker(s.code, "serve stop"), s.logLine, false)
			late := ctx.Err() != nil
			cancel()
			switch {
			case late:
				notes = append(notes, fmt.Sprintf("its stop command ran %s and was killed", serveStopLimit))
			case err != nil:
				notes = append(notes, "its stop command failed: "+err.Error())
			case code != 0:
				notes = append(notes, fmt.Sprintf("its stop command exited %d", code))
			default:
				notes = append(notes, "its stop command ran")
			}
		}
		s.p.Stop()
		select {
		case <-s.p.done:
		case <-time.After(2*Grace + time.Second):
			notes = append(notes, "its processes would not end")
		}
		s.giveGate()
		select {
		case <-s.logged:
		case <-time.After(time.Second):
		}
		s.logLine(fmt.Sprintf("== %s stopped: %s", h.now().Format(time.RFC3339), why))
		s.closeLog()
		h.servesMu.Lock()
		sv := proto.CloneOf(s.serve)
		h.servesMu.Unlock()
		var still []string
		for _, p := range sv.GetPorts() {
			p.Open = portAnswers("127.0.0.1", p.GetNumber())
			if p.Open {
				still = append(still, strconv.Itoa(int(p.GetNumber())))
			}
		}
		sv.State = planv1.ServeState_SERVE_STATE_STOPPED
		if exited {
			sv.State = planv1.ServeState_SERVE_STATE_EXITED
		}
		sv.Pid = 0
		if forget {
			sv.Relight = false
		}
		text := fmt.Sprintf("serve %s stopped: %s", s.name, why)
		if len(notes) > 0 {
			text += "; " + strings.Join(notes, "; ")
		}
		if len(still) > 0 {
			line := fmt.Sprintf("%s %s still %s after %s's serve %s stopped: something outside its processes holds %s",
				plural(len(still), "port"), strings.Join(still, ", "), plural3(len(still), "answers", "answer"), s.code, s.name,
				plural3(len(still), "it", "them"))
			text += "; " + line
			if err := h.toLead(context.Background(), planv1.LeadEventKind_LEAD_EVENT_KIND_MOVE, s.r.wish, s.code, line); err != nil {
				logx.L().Warn("tell the lead a port still answers", "task", s.code, "err", err)
			}
		}
		h.servesMu.Lock()
		s.serve, s.text = sv, text
		h.servesMu.Unlock()
	})
	h.servesMu.Lock()
	defer h.servesMu.Unlock()
	return s.serve, s.text
}

// plural3 is one or other, as n is 1 or not.
func plural3(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}

// setServing records sv as where s stands.
func (h *Harness) setServing(s *serving, sv *planv1.Serve) {
	h.servesMu.Lock()
	s.serve = sv
	h.servesMu.Unlock()
}

// openServes makes the serves of the run r, an ifrit's, unless r has them already (its worker started again).
func (h *Harness) openServes(r *run) {
	h.servesMu.Lock()
	defer h.servesMu.Unlock()
	if h.serving == nil {
		h.serving = map[string]*ifritServes{}
	}
	if is := h.serving[r.id]; is == nil || is.r != r {
		h.serving[r.id] = &ifritServes{r: r, live: map[string]*serving{}}
	}
}

// addServing records s, the serve sv, among the serves of r: false once r ends.
func (h *Harness) addServing(r *run, s *serving, sv *planv1.Serve) bool {
	h.servesMu.Lock()
	defer h.servesMu.Unlock()
	is := h.serving[r.id]
	if is == nil || is.r != r || is.closed {
		s.serve = sv
		return false
	}
	s.serve = sv
	is.live[strings.ToLower(s.name)] = s
	return true
}

// liveServe is the serve name of the ifrit taskID's run: running, or stopped since the run started; nil without one.
func (h *Harness) liveServe(taskID, name string) *serving {
	h.servesMu.Lock()
	defer h.servesMu.Unlock()
	if is := h.serving[taskID]; is != nil && !is.closed {
		return is.live[strings.ToLower(name)]
	}
	return nil
}

// servesOf are the serves of the ifrit task: as stored, those of its run as they stand now, in the order they first
// started.
func (h *Harness) servesOf(task *planv1.Task) []*planv1.Serve {
	out := make([]*planv1.Serve, 0, len(task.GetIfrit().GetServes()))
	for _, sv := range task.GetIfrit().GetServes() {
		out = append(out, proto.CloneOf(sv))
	}
	h.servesMu.Lock()
	defer h.servesMu.Unlock()
	is := h.serving[task.GetId()]
	if is == nil {
		return out
	}
	live := make([]*serving, 0, len(is.live))
	for _, s := range is.live {
		live = append(live, s)
	}
	slices.SortFunc(live, func(a, b *serving) int {
		return a.serve.GetStartTime().AsTime().Compare(b.serve.GetStartTime().AsTime())
	})
	for _, s := range live {
		if at := serveAt(out, s.name); at >= 0 {
			out[at] = proto.CloneOf(s.serve)
		} else {
			out = append(out, proto.CloneOf(s.serve))
		}
	}
	return out
}

// serveAt is where the serve name is in serves, case ignored; -1 when it is not.
func serveAt(serves []*planv1.Serve, name string) int {
	return slices.IndexFunc(serves, func(sv *planv1.Serve) bool { return strings.EqualFold(sv.GetName(), name) })
}

// setServeIn sets sv among the serves of the ifrit t, in the place of the one of its name, or last. Replaced, never
// changed: a copy of the task taken before shares them.
func setServeIn(t *planv1.Task, sv *planv1.Serve) {
	ifrit := &planv1.TaskIfrit{}
	if t.GetIfrit() != nil {
		ifrit = proto.CloneOf(t.GetIfrit())
	}
	if at := serveAt(ifrit.Serves, sv.GetName()); at >= 0 {
		ifrit.Serves[at] = proto.CloneOf(sv)
	} else {
		ifrit.Serves = append(ifrit.Serves, proto.CloneOf(sv))
	}
	t.Ifrit = ifrit
}

// noteServe is how a serve of r that runs apart from its pump records a change: through the pump, which owns the
// task, for as long as the run goes on.
func (h *Harness) noteServe(r *run) func(Event) {
	return func(ev Event) { noteFinal(r, ev) }
}

// noteFinal hands ev to the pump of r, unless r has ended.
func noteFinal(r *run, ev Event) {
	select {
	case r.notes <- ev:
	case <-r.done:
	}
}

// note writes an event from outside the worker, the pump's or the end's: the serve it carries set in the task, unless
// a later change of that serve was recorded since (applyServe).
func (h *Harness) note(r *run, ev Event) {
	if ev.Serve != nil {
		h.applyServe(r, ev)
		return
	}
	h.write(r, actorHarness, methodEvent, nil, ev)
}

// applyServe sets the serve of ev in the task of r and writes both, by the one that owns the task: unless the serve
// stands otherwise since (a later change, or the run's end), when ev is stale and dropped; nor when the task has it
// as it is already.
func (h *Harness) applyServe(r *run, ev Event) {
	h.servesMu.Lock()
	is := h.serving[r.id]
	var cur *serving
	if is != nil && is.r == r {
		cur = is.live[strings.ToLower(ev.Serve.GetName())]
	}
	stale := cur == nil || cur.serve != ev.Serve
	h.servesMu.Unlock()
	if stale {
		return
	}
	if at := serveAt(r.task.GetIfrit().GetServes(), ev.Serve.GetName()); at >= 0 && proto.Equal(r.task.GetIfrit().GetServes()[at], ev.Serve) {
		return
	}
	setServeIn(r.task, ev.Serve)
	h.write(r, actorHarness, methodServe, r.task, ev)
}

// endServes stops the serves of r, an ifrit's run that ends, each at once beside the others, and records them stopped,
// ready to start again when the ifrit is lit again. The end calls it, as it owns the task.
func (h *Harness) endServes(r *run) {
	h.servesMu.Lock()
	is := h.serving[r.id]
	if is == nil || is.r != r {
		h.servesMu.Unlock()
		return
	}
	is.closed = true
	live := make([]*serving, 0, len(is.live))
	for _, s := range is.live {
		live = append(live, s)
	}
	h.servesMu.Unlock()
	slices.SortFunc(live, func(a, b *serving) int { return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)) })
	why := "its ifrit ended"
	if h.ctx.Err() != nil {
		why = "Djinn quits"
	}
	var wg sync.WaitGroup
	for _, s := range live {
		wg.Go(func() { h.stopServing(s, why, false) })
	}
	wg.Wait()
	for _, s := range live {
		h.servesMu.Lock()
		final, text := s.serve, s.text
		h.servesMu.Unlock()
		h.applyServe(r, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS, Text: text, Serve: final})
	}
	h.servesMu.Lock()
	if h.serving[r.id] == is {
		delete(h.serving, r.id)
	}
	h.servesMu.Unlock()
}

// relightServes starts again the serves of the ifrit of r that stopped with it, as its worker starts in worktree,
// before its agent: each as djinn serve run would, waiting serveReady at most. The one that starts the worker calls
// it, as it owns the task.
func (h *Harness) relightServes(r *run, worktree string) {
	h.openServes(r)
	for _, sv := range r.task.GetIfrit().GetServes() {
		if !sv.GetRelight() || h.liveServe(r.id, sv.GetName()) != nil || h.ctx.Err() != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(h.ctx, serveReady)
		_, err := h.runServe(ctx, r, serveParams{
			name: sv.GetName(), command: sv.GetCommand(), directory: sv.GetDirectory(), gate: sv.GetGate(), stop: sv.GetStop(),
			ports: declaredOf(sv.GetPorts()),
		}, worktree, func(ev Event) { h.applyServe(r, ev) })
		cancel()
		if err != nil {
			h.write(r, actorHarness, methodEvent, nil, Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: fmt.Sprintf("serve %s not started again: %s", sv.GetName(), connectMessage(err))})
		}
	}
}

// declaredOf are the ports of ports that were declared, as they were.
func declaredOf(ports []*planv1.Port) []*planv1.Port {
	var out []*planv1.Port
	for _, p := range ports {
		if p.GetDeclared() {
			out = append(out, &planv1.Port{Number: p.GetNumber(), Label: p.GetLabel(), Declared: true})
		}
	}
	return out
}

// connectMessage is the message of err without its Connect code.
func connectMessage(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Message()
	}
	return err.Error()
}

// recoverServes records stopped the serves a previous djinn up left running: they died with it, or its next start
// stopped them (sweep), and run their stop commands. They start again when their ifrit is lit again. It changes the
// tasks in place, so that the rest of Recover sees them stopped.
func (h *Harness) recoverServes(ctx context.Context, tasks []*planv1.Task) error {
	for _, t := range tasks {
		changed := false
		for _, sv := range t.GetIfrit().GetServes() {
			if sv.GetState() != planv1.ServeState_SERVE_STATE_STARTING && sv.GetState() != planv1.ServeState_SERVE_STATE_RUNNING {
				continue
			}
			sv = proto.CloneOf(sv)
			sv.State, sv.Pid = planv1.ServeState_SERVE_STATE_STOPPED, 0
			for _, p := range sv.Ports {
				p.Open = false
			}
			setServeIn(t, sv)
			changed = true
		}
		if !changed {
			continue
		}
		err := h.store.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.Journal(actorHarness, methodRecover, t); err != nil {
				return err
			}
			if err := tx.Put(t); err != nil {
				return err
			}
			return putEvents(ctx, tx, h.newEvent(t.GetId(), Event{Kind: planv1.TaskEventKind_TASK_EVENT_KIND_STATUS,
				Text: "its serves stopped with the last djinn up; they start again when it is lit again"}))
		})
		if err != nil {
			return fmt.Errorf("recover the serves of %s: %w", t.GetCode(), err)
		}
	}
	return nil
}

// serveAllowed says why the ifrit t may not run command, or its stop command, as a serve: the watchers' rule. Where its
// worker's permissions list commands (the project's .agents/permissions.txtpb, a wish's allowance over it), each must
// be one of them, or one the developer allows in the project for themselves (djinn project allow --serve); the
// project's denied_commands win over both.
func (h *Harness) serveAllowed(ctx context.Context, t *planv1.Task, commands ...string) error {
	project, err := store.Get[*planv1.Project](ctx, h.store, t.GetProjectId())
	if err != nil {
		return fmt.Errorf("the ifrit's project: %w", err)
	}
	wish, err := store.Get[*planv1.Wish](ctx, h.store, t.GetWishId())
	if err != nil {
		return fmt.Errorf("the ifrit's wish: %w", err)
	}
	access, declared, err := decideAccess(h.home, project, t.GetProvider(), plan.AllowanceOf(wish, project.GetId()))
	if err != nil {
		return fmt.Errorf("project %s: %w", project.GetName(), err)
	}
	_, perms := accessSpec(access, declared)
	if perms == nil {
		return nil // The agent's own configuration decides what its worker runs; a serve runs as it would.
	}
	settings, _ := plan.LoadSettings(h.home, project)
	for _, c := range commands {
		if c == "" || commandAllowed(perms, c) {
			continue
		}
		if !matchesPrefix(c, perms.GetDeniedCommands()) && matchesPrefix(c, settings.ServeCommands) {
			continue
		}
		return serveRefusal(project, c, perms)
	}
	return nil
}

// serveRefusal says why a serve cannot run command in project under perms, and how it could.
func serveRefusal(project *planv1.Project, command string, perms *djinnv1.Permissions) error {
	name, file := project.GetName(), filepath.ToSlash(PermissionsFile)
	if matchesPrefix(command, perms.GetDeniedCommands()) {
		return fmt.Errorf("%s's %s denies %q, and its denied commands win over any allowance: a serve cannot run it",
			name, file, command)
	}
	return fmt.Errorf("%s's permissions do not list %q among the commands its workers run without review, and a serve "+
		"has no reviewer. List it in its %s, or allow it for yourself in your own settings, never in the repository: "+
		"djinn project allow %s --serve %s", name, command, file, shellQuote(name), shellQuote(watchPrefix(command)))
}
