package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/empowill/djinn"
	"github.com/empowill/djinn/gen/go/demo/v1/demov1connect"
	djinnv1 "github.com/empowill/djinn/gen/go/djinn/v1"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/backup"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/demo"
	"github.com/empowill/djinn/internal/gate"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/link"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/render"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
)

// readMachine reads the machine for the scheduler and the gates; nil reads this machine, the disk of its data folder
// and its GPUs. Tests replace it.
var readMachine func() (machine.Snapshot, error)

// runUp serves the interface and the API, then opens a native window on them, or with --browser prints the
// URL to open. On macOS and Linux the window goes through the Wails asset server and the command line through a
// Unix socket: no port is open. On Windows, and with --browser, everything goes through a loopback HTTP server
// guarded by a token. Either way the server writes its address in the data directory for the command line. It
// returns restart true when it stopped to restart on a newer binary (see update.go): the caller starts it, once
// everything here is closed.
func runUp(args []string) (restart bool, err error) {
	var (
		browser                                   bool
		enlightenWorkers                          bool
		profiles                                  bool
		port, maxWorkers, workerCPU, workerMemory int
		workerMemoryGuard                         float64
		term, termDir                             string
	)
	flags := cli.Up.FlagSet(map[string]any{
		"browser": &browser, "port": &port, "terminal": &term, "terminal-dir": &termDir, "workers": &maxWorkers,
		"worker-cpu": &workerCPU, "worker-memory": &workerMemory,
		"worker-memory-guard": &workerMemoryGuard,
		"enlighten-workers":   &enlightenWorkers,
		"pprof":               &profiles,
	})
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	var enlightenSet bool
	if flagSet(flags, "enlighten-workers") {
		enlightenSet = true
	} else if v := os.Getenv("DJINN_ENLIGHTEN_WORKERS"); v != "" {
		on, err := onOff(v)
		if err != nil {
			return false, fmt.Errorf("DJINN_ENLIGHTEN_WORKERS: %w", err)
		}
		enlightenSet, enlightenWorkers = true, on
	}
	if v := os.Getenv("DJINN_PPROF"); v != "" && !flagSet(flags, "pprof") {
		on, err := onOff(v)
		if err != nil {
			return false, fmt.Errorf("DJINN_PPROF: %w", err)
		}
		profiles = on
	}
	if flags.NArg() > 0 {
		return false, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if maxWorkers == 0 && os.Getenv("DJINN_WORKERS") != "" {
		if _, err := fmt.Sscan(os.Getenv("DJINN_WORKERS"), &maxWorkers); err != nil {
			return false, fmt.Errorf("DJINN_WORKERS: %w", err)
		}
	}
	if maxWorkers < 0 || maxWorkers > 16 {
		return false, fmt.Errorf("--workers %d: expected 1 to 16, or 0 to decide from the machine", maxWorkers)
	}
	if workerCPU == 0 && os.Getenv("DJINN_WORKER_CPU") != "" {
		if _, err := fmt.Sscan(os.Getenv("DJINN_WORKER_CPU"), &workerCPU); err != nil {
			return false, fmt.Errorf("DJINN_WORKER_CPU: %w", err)
		}
	}
	if most := 100 * runtime.NumCPU(); workerCPU < 0 || workerCPU > most {
		return false, fmt.Errorf("--worker-cpu %d: expected 1 to %d (percent of one core), or 0 for no cap", workerCPU, most)
	}
	if workerMemory == 0 && os.Getenv("DJINN_WORKER_MEMORY") != "" {
		if _, err := fmt.Sscan(os.Getenv("DJINN_WORKER_MEMORY"), &workerMemory); err != nil {
			return false, fmt.Errorf("DJINN_WORKER_MEMORY: %w", err)
		}
	}
	if workerMemory < 0 {
		return false, fmt.Errorf("--worker-memory %d: expected MiB, or 0 for no cap", workerMemory)
	}
	if workerMemoryGuard == 0 && os.Getenv("DJINN_WORKER_MEMORY_GUARD") != "" {
		if _, err := fmt.Sscan(os.Getenv("DJINN_WORKER_MEMORY_GUARD"), &workerMemoryGuard); err != nil {
			return false, fmt.Errorf("DJINN_WORKER_MEMORY_GUARD: %w", err)
		}
	}
	if workerMemoryGuard < 0 {
		return false, fmt.Errorf("--worker-memory-guard %g: expected a positive factor (e.g. 2 to 3), or 0 to turn off", workerMemoryGuard)
	}
	home, err := ui.Home()
	if err != nil {
		return false, err
	}
	// One djinn per data directory: a second djinn up brings the first one's window to the front instead.
	if addr, err := server.ReadAddr(home); err == nil {
		if cli.Alive(addr) {
			return false, showRunning(addr, flags)
		}
		fmt.Fprintln(os.Stderr, "djinn: the last djinn up did not stop cleanly; starting again")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, filepath.Join(home, store.File), plan.Entities()...)
	if err != nil {
		return false, fmt.Errorf("open the database: %w", err)
	}
	defer db.Close()
	// The workers stop before the database closes: deferred calls run last first.
	savedNotch := machine.ReadNotch(home)
	policy := machine.NotchPolicy(savedNotch)
	policy.Workers = maxWorkers
	policy.WorkerMemory = uint64(workerMemory) << 20
	policy.WorkerMemoryGuard = workerMemoryGuard
	read := readMachine
	if read == nil {
		read = machine.Reader(home)
	}
	monitor := machine.NewMonitor(policy, read)
	opts := []harness.Option{
		harness.WithCapacity(monitor.Capacity),
		harness.WithMemory(monitor.Available, policy),
		harness.WithTotal(monitor.Total),
		// Started from the Finder or a desktop menu, Djinn has a bare PATH (fromLauncher): before each start, its PATH
		// gains the folders of the agents installed meanwhile.
		harness.WithBeforeStart(beforeStart()),
	}
	if machine.NotMeasured == "" {
		opts = append(opts, harness.WithMeasure(5*time.Second, machine.ReadWorker))
	}
	if enlightenSet {
		opts = append(opts, harness.WithEnlightenWorkers(enlightenWorkers))
	}
	if scopes := workerScopes(ctx, os.Stderr, workerCPU, policy.WorkerMemory, policy.WorkerMemoryGuard); scopes != nil {
		opts = append(opts, harness.WithScopes(scopes))
	}
	// The integration of finished work runs its gen, setup and checks under the gates, as djinn gate run does.
	var gates *gate.Gates
	opts = append(opts, harness.WithGates(func(ctx context.Context, name, taskID, what, dir string, waiting func(string)) (func(), error) {
		held, err := gates.Take(ctx, gate.Request{Name: name, TaskID: taskID, What: what, Dir: dir}, waiting)
		if err != nil {
			return nil, err
		}
		return held.Give, nil
	}))
	// A batch committed in a project that names an install command: the window proposes to install it. The
	// integration starts once the window's service is there.
	var uiSvc *ui.Service
	opts = append(opts, harness.WithBuilt(func(b harness.Built) { uiSvc.SetBuild(buildOf(b)) }), harness.WithVersion(version))
	workers := harness.New(db, home, harness.Providers(), opts...)
	// What the workers hold is left out of the memory available: the memory Djinn may commit counts it back in.
	monitor.SetWorkerMemory(workers.Resident)
	defer workers.Close()
	if err := workers.Recover(ctx); err != nil {
		return false, err
	}
	workers.Schedule()
	gates = gate.New(monitor, machine.NewPeaks(db), workers)
	workers.HeldGates(gates.Held, gates.Waiting)
	workers.GatesOutside(gates.Outside)
	gates.Freed(workers.Wake)
	gates.ProjectResolver(func(taskID, dir string) string {
		_, name, _ := machine.Project(ctx, db, taskID, dir)
		return name
	})
	seriesRec := machine.NewSeriesRecorder(monitor, machine.ReadWorker)
	seriesRec.Sample()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				seriesRec.Sample()
			}
		}
	}()
	if machine.NotMeasured == "" {
		// A gate's holder is a process of its own, in no worker's scope: read from its processes.
		gates.Measure(5*time.Second, func(pid int) (machine.Group, error) { return machine.ReadWorker(pid, "") })
	}
	if termDir != "" {
		if termDir, err = filepath.Abs(termDir); err != nil {
			return false, err
		}
	}
	// The terminals hang up before the workers stop: the lead may be driving them. The note of the leads follows
	// them until djinn up stops, and is left as it is before they hang up: a crash, or an error, keeps it.
	leadNotes := newLeadNote(home, os.Stderr)
	// The window's terminal opens in the first project, never in the home folder, where djinn starts from a menu.
	var leadPrompted atomic.Pointer[func()] // what follows the choice the lead shows, once the window's services run
	var leadChanged atomic.Pointer[func()]  // what tells the window that the lead started or ended, once they run
	terminals := terminal.NewManager(terminal.Config{
		Command: terminal.ShellCommand(term), Dir: termDir, BeforeStart: beforeStart(),
		// A lead that ends shows beside Lead in the window, with its exit code, until Lead starts it again.
		Changed: func() {
			leadNotes.update()
			if f := leadChanged.Load(); f != nil {
				(*f)()
			}
		},
		Folder: func() (string, error) { return plan.FirstProjectFolder(ctx, db) },
		// A choice the lead's agent shows (an approval, a folder to trust) shows in the window's attention bar, and
		// as a notification.
		Prompted: func(t *terminal.Terminal) {
			if f := leadPrompted.Load(); f != nil && t.Name == plan.LeadTerminal {
				(*f)()
			}
		},
	})
	leadNotes.terms = terminals
	defer terminals.Close()
	defer leadNotes.freeze()
	if !hasWindow && !browser {
		fmt.Fprintln(os.Stderr, "djinn: this build has no native window, serving the browser instead")
		browser = true
	}
	transport := server.TransportFor(runtime.GOOS)
	if browser {
		transport = server.HTTP
	}
	if uiSvc, err = ui.New(version); err != nil {
		return false, err
	}
	leadNotes.ui = uiSvc
	uiSvc.Shown = func(wishID, terminal string) {
		leadNotes.update()
	}
	uiSvc.Known = func(ctx context.Context, wishID string) bool {
		_, err := store.Get[*planv1.Wish](ctx, db, wishID)
		return !errors.Is(err, store.ErrNotFound)
	}
	// A djinn:// link shows the tilasm's wish, or the wish.
	uiSvc.Linked = func(ctx context.Context, l link.Link) (string, error) { return plan.Linked(ctx, db, l) }
	registerLinks(os.Stderr)
	// The lead a wish kept, before the one lead, is the lead from now on.
	lead := carryLead(ctx, db, os.Stderr)
	// After an update, the terminals that ran before it run again; after a crash, the leads.
	uiSvc.SetNotResumed(resumeTerminals(home, terminals, uiSvc, lead.GetSessionId(), os.Stderr))
	// Then the lead, unless a lead came back already.
	leadFailed := resumeLead(ctx, db, home, terminals, uiSvc, os.Stderr)
	updates, err := newUpdater(version, uiSvc, terminals, leadNotes, stop)
	if err != nil {
		return false, err
	}
	defer func() { restart = err == nil && updates.Restarting() }()
	if updates != nil {
		uiSvc.Restart = updates.restart
		// The newer Djinn waiting is named as a build: where it comes from, what it holds against this one.
		updates.describe = func(ctx context.Context, v string) *uiv1.Build {
			if b, ok := workers.DescribeVersion(ctx, v); ok {
				return buildOf(b)
			}
			return nil
		}
		if commit, dirty, ok := localBuild(version); ok {
			updates.fit = func(ctx context.Context, tag string) (harness.ReleaseFit, error) {
				return workers.Release(ctx, commit, dirty, tag)
			}
		}
		go updates.run(ctx)
	}
	uiSvc.Install = func(ctx context.Context, b *uiv1.Build, progress func(uiv1.InstallStep, string)) (bool, error) {
		steps := map[harness.InstallStep]uiv1.InstallStep{
			harness.InstallWaiting:   uiv1.InstallStep_INSTALL_STEP_WAITING,
			harness.InstallPreparing: uiv1.InstallStep_INSTALL_STEP_PREPARING,
			harness.InstallBuilding:  uiv1.InstallStep_INSTALL_STEP_BUILDING,
		}
		report := func(step harness.InstallStep, waiting string) { progress(steps[step], waiting) }
		if _, err := workers.Install(ctx, b.GetWishId(), b.GetProjectId(), b.GetSha(), report); err != nil {
			return false, err
		}
		return updates != nil && updates.check(ctx) != "", nil
	}
	workers.Integrate()
	raise := make(chan struct{}, 1)
	var url string // In browser mode, the page to open, token included.
	uiSvc.Window = !browser
	if !browser {
		uiSvc.ChooseFolder = chooseFolder
	}
	uiSvc.Raise = func() {
		if browser {
			fmt.Println("djinn: open", url)
			return
		}
		select {
		case raise <- struct{}{}:
		default: // Already asked.
		}
	}
	// The window takes a global shortcut that brings it forward on what waits for you.
	shortcuts := &ui.Shortcuts{
		Home: home, Store: db, Show: func(wishID string) { leads{terminals, uiSvc}.Show(wishID, "") },
	}
	if !browser {
		uiSvc.Shortcuts = shortcuts
	}
	// The pages of the synced wishes follow every change, until djinn up stops.
	pages := plan.NewPages(db, home, version)
	go pages.Run(ctx)
	// The http server's origin and token, once it listens: a tilasm's local address, for a browser or an agent.
	var httpOrigin, httpToken string
	tilasmURL := func(id string) string {
		if httpToken == "" {
			return ""
		}
		return server.TilasmAddress(httpOrigin, httpToken, id)
	}
	showTilasm := func(wishID, tilasmID string) bool {
		if uiSvc.Raise != nil {
			uiSvc.Raise()
		}
		uiSvc.PresentTilasm(wishID, tilasmID)
		return uiSvc.Window
	}
	var broadcastLoad func(djinnv1.LoadNotch)
	var changeLead func()
	svc := services(db, home, workers, terminals, uiSvc, pages,
		plan.WithShowTilasm(showTilasm), plan.WithTilasmURL(tilasmURL),
		plan.WithLoad(func() djinnv1.LoadNotch { return monitor.Policy().Notch }),
		plan.WithLoadBroadcaster(&broadcastLoad), plan.WithLeadBroadcaster(&changeLead), plan.WithLeadFailed(leadFailed),
	)
	loadPrefix, loadHandler := machine.LoadHandler(home, monitor, workers.SetPolicy, func(n djinnv1.LoadNotch) {
		if broadcastLoad != nil {
			broadcastLoad(n)
		}
	}, workers.WorkerMemory, machine.WithLoadRunningWorkers(workers.Running),
		machine.WithLoadPausedWorkers(workers.Paused),
		machine.WithLoadWorkerForecast(workers.WorkerForecast),
		machine.WithLoadStreamer(func(stream func(context.Context, func(*djinnv1.LoadServiceWatchResponse) error) error) {
			uiSvc.WatchLoad = stream
		}),
	)
	svc[loadPrefix] = loadHandler
	machinePrefix, machineHandler := machine.Handler(monitor, workers.Running, workers.Uses,
		machine.WithSeriesRecorder(seriesRec),
		machine.WithSeriesWorkers(workers.RunsSeries),
		machine.WithSeriesIntervals(gates.Intervals),
	)
	svc[machinePrefix] = machineHandler
	gatePrefix, gateHandler := gate.Handler(gates)
	svc[gatePrefix] = gateHandler
	costsPrefix, costsHandler := machine.CostsHandler(db)
	svc[costsPrefix] = costsHandler
	backupPrefix, backupHandler := backup.Handler(db, home, version)
	svc[backupPrefix] = backupHandler
	if profiles {
		svc[server.ProfilePath] = server.Profiles()
	}
	h := server.Handler(djinn.UI(), djinn.Docs(), svc)
	h = server.WorkerContext(h, func(ctx context.Context, peerPID int, token string) context.Context {
		if caller := workers.Caller(peerPID, token); caller != nil {
			return plan.WithCaller(ctx, caller)
		}
		return ctx
	})

	var ln net.Listener
	var addr string
	switch transport {
	case server.Wails:
		if ln, err = server.ListenUnix(filepath.Join(home, server.SocketFile)); err != nil {
			return false, err
		}
		addr = "unix://" + ln.Addr().String()
	case server.HTTP:
		if ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			return false, err
		}
		origin := "http://" + ln.Addr().String()
		token := server.NewToken()
		httpOrigin, httpToken = origin, token // Before the server serves: its handlers read them.
		url = origin + "/?token=" + token
		addr = url
		h = server.Guard(h, token, origin)
	}
	remove, err := server.WriteAddr(home, addr)
	if err != nil {
		ln.Close()
		return false, err
	}
	defer remove()
	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx, ln, h) }()
	// The window tells what waits for the developer in an active wish by system notifications: questions, tasks
	// that wait or failed, wishes ready to review. A click shows the wish there; an answer from one goes through the
	// server, as the window's would.
	notices := &ui.Notices{
		Store: db, Language: render.SystemLanguage(),
		Show:   func(wishID, target string) { leads{terminals, uiSvc}.ShowAt(wishID, "", target) },
		Answer: func(ctx context.Context, id string, choice planv1.Choice) error { return answer(ctx, addr, id, choice) },
	}
	uiSvc.Notices = notices
	go notices.Run(ctx)
	prompted := func() {
		changeLead()
		notices.LeadPrompt(leads{terminals, uiSvc}.Prompt(plan.LeadTerminal))
	}
	leadPrompted.Store(&prompted)
	// The window learns that the lead's terminal started or ended, not that another terminal did.
	var leadSeen struct {
		sync.Mutex
		t      *terminal.Terminal
		exited bool
	}
	leadChange := func() {
		t := terminals.Latest(plan.LeadTerminal)
		exited := t != nil && t.Exited()
		leadSeen.Lock()
		same := leadSeen.t == t && leadSeen.exited == exited
		leadSeen.t, leadSeen.exited = t, exited
		leadSeen.Unlock()
		if !same {
			changeLead()
		}
	}
	leadChanged.Store(&leadChange)

	// A link macOS gives the app, clicked anywhere: the window shows it, or says it does not know it.
	openLinks := func(raw string) {
		if _, err := uiSvc.OpenLink(ctx, connect.NewRequest(&uiv1.UiServiceOpenLinkRequest{Url: raw})); err != nil {
			log.Printf("djinn: open %s: %v", raw, err)
		}
	}
	switch {
	case browser:
		fmt.Println("djinn: open", url)
		err = <-served
	default:
		if transport == server.Wails {
			err = openWindow(ctx, "", server.WholeWrites(h), raise, notices, shortcuts, openLinks)
		} else {
			err = openWindow(ctx, url, nil, raise, notices, shortcuts, openLinks)
		}
		stop() // The window is closed: stop the server too.
		err = errors.Join(err, <-served)
	}
	// Stopped as asked (Quit, Ctrl+Q, a signal): the leads are not reopened at the next start, unless it restarts.
	leadNotes.quit()
	return false, err
}

// services returns the Connect services, by path prefix: the window's, the plan's on the database and the data
// folder home, the tasks' on the harness, and the terminals'.
func services(
	db *store.Store, home string, h *harness.Harness, terminals *terminal.Manager, uiSvc *ui.Service, pages *plan.Pages,
	more ...plan.Option,
) map[string]http.Handler {
	demoPrefix, demoHandler := demov1connect.NewDemoServiceHandler(demo.Service{})
	uiPrefix, uiHandler := uiv1connect.NewUiServiceHandler(uiSvc)
	language := render.SystemLanguage()
	out := plan.Handlers(db, append([]plan.Option{plan.WithAnswered(h.Answered), plan.WithEnlightened(h.Enlightened),
		plan.WithLeads(leads{terminals, uiSvc}),
		plan.WithPages(pages), plan.WithLanguage(language), plan.WithWatchers(h.SpawnWatcher), plan.WithHome(home),
		plan.WithWorkers(h), plan.WithPusher(h), plan.WithFillModels(h.FillModels), plan.WithClock(h.Now)}, more...)...)
	// A watcher wakes the lead of its wish, as an answer does; its done line offers to grant a wish made from a
	// template.
	wishes := &plan.Wishes{Store: db, Leads: leads{terminals, uiSvc}, Language: language, Workers: h}
	h.TellLeads(wishes.Tell)
	// The lead's events wait in its queue, which djinn lead watch hands out (decision Q191); without a watch, one
	// line says they wait.
	h.NotifyLead(wishes.Notify)
	wishes.RemindLead()
	h.OnWatched(wishes.Watched)
	// The inbox sources of the projects' skills run until djinn up stops; what they print waits for a click.
	h.RunSources((&plan.Inbox{Wishes: wishes}).Receive)
	out[demoPrefix] = demoHandler
	out[uiPrefix] = uiHandler
	taskPrefix, taskHandler := harness.Handler(h)
	out[taskPrefix] = taskHandler
	planPrefix, planHandler := harness.PlanHandler(h)
	out[planPrefix] = planHandler
	terminalPrefix, terminalHandler := terminal.Handler(terminals)
	out[terminalPrefix] = terminalHandler
	return out
}

// leads runs the leads of the wishes in the terminals of the window, and shows them there.
type leads struct {
	terminals *terminal.Manager
	ui        *ui.Service
}

func (l leads) Open(name, line, dir, exclusive string) ([]string, string, bool, error) {
	command := terminal.ShellCommand(line)
	if command == nil {
		command = terminal.Shell()
	}
	t, attached, err := l.terminals.OpenExclusive(name, command, dir, 0, 0, exclusive)
	if err != nil {
		return nil, "", false, err
	}
	return t.Command, t.Dir, attached, nil
}

func (l leads) Say(name, line string) error { return l.terminals.Say(name, line) }

func (l leads) SayText(name, text string) (bool, error) { return l.terminals.SayText(name, text) }

func (l leads) Running(name string) bool { return l.terminals.IsRunning(name) }

// Exit is how the lead's program ended on its own: a lead closed on request (its tab's ×, another agent, Djinn
// stopping) is no news.
func (l leads) Exit(name string) *planv1.LeadExit {
	t := l.terminals.Latest(name)
	if t == nil || t.HungUp() {
		return nil
	}
	if _, _, exited, code := t.State(); exited {
		return &planv1.LeadExit{Code: int32(code)}
	}
	return nil
}

func (l leads) Command(name string) []string {
	if t := l.terminals.Lookup(name); t != nil {
		return t.Command
	}
	return nil
}

func (l leads) Close(name string) { l.terminals.Hangup(name) }

func (l leads) Prompt(name string) *planv1.LeadPrompt {
	t := l.terminals.Lookup(name)
	if t == nil {
		return nil
	}
	p := t.Prompt()
	if p == nil {
		return nil
	}
	return &planv1.LeadPrompt{Title: p.Title, Lines: p.Lines, Options: p.Options}
}

func (l leads) Choose(name, title string, lines, options []string, index int) error {
	t := l.terminals.Lookup(name)
	if t == nil {
		return plan.ErrNoChoice
	}
	err := t.Choose(title, lines, options, index)
	if errors.Is(err, terminal.ErrNoPrompt) || errors.Is(err, terminal.ErrExited) {
		return plan.ErrNoChoice
	}
	return err
}

func (l leads) Show(wishID, name string) { l.ShowAt(wishID, name, "") }

// ShowAt is Show with an element of the wish in view (UiServiceWatchShowResponse.target).
func (l leads) ShowAt(wishID, name, target string) {
	if l.ui.Raise != nil {
		l.ui.Raise()
	}
	l.ui.PresentAt(wishID, name, target)
}

// answer answers a question through the djinn that answers at addr.
func answer(ctx context.Context, addr, questionID string, choice planv1.Choice) error {
	client, base, err := cli.Dial(addr)
	if err != nil {
		return err
	}
	_, err = planv1connect.NewQuestionServiceClient(client, base).Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: questionID}}, Choice: choice,
		By: planv1.Closer_CLOSER_DEVELOPER,
	}))
	return err
}

// flagSet tells whether the command line set the flag name.
func flagSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// onOff reads on or off, and the other words strconv.ParseBool reads.
func onOff(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%q: expected on or off", v)
	}
	return on, nil
}

// showRunning asks the djinn that answers at addr to bring its window to the front, and says so.
func showRunning(addr string, flags *flag.FlagSet) error {
	client, base, err := cli.Dial(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := uiv1connect.NewUiServiceClient(client, base).Show(ctx, connect.NewRequest(&uiv1.UiServiceShowRequest{}))
	if err != nil {
		return fmt.Errorf("djinn is already running, but does not answer: %w", err)
	}
	ignored := ""
	flags.Visit(func(f *flag.Flag) { ignored += " --" + f.Name })
	if ignored != "" {
		ignored = " (ignored:" + ignored + ")"
	}
	if res.Msg.GetWindow() {
		fmt.Println("djinn is already running: its window is in front" + ignored)
		return nil
	}
	fmt.Println("djinn is already running: open " + addr + ignored)
	return nil
}

// registerLinks makes this djinn the handler of djinn:// links for the user, on Windows, when there is none: a release
// installed by hand has no installer to do it. A development build never does: it must not take the links of the
// Djinn you use. Elsewhere the install registers them (tools/icons, tools/macapp).
func registerLinks(w io.Writer) {
	if ui.Develop {
		return
	}
	if ok, err := link.Registered(); err != nil || ok {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		err = link.Register(exe)
	}
	if err != nil {
		fmt.Fprintf(w, "djinn: djinn:// links will not open Djinn: %v\n", err)
	}
}

// workerScopes are the systemd scopes the workers run in, one each, their CPU capped at cpu percent of a core and
// their memory at memory bytes (0: uncapped) or dynamically guarded (guard > 0); nil where there are none: djinn up
// says why, once, and workers run in their process group.
func workerScopes(ctx context.Context, w io.Writer, cpu int, memory uint64, guard float64) *machine.Scopes {
	scopes, notes, err := machine.ProbeScopes(ctx, cpu, memory)
	if err != nil {
		uncapped := ""
		if cpu > 0 || memory > 0 || guard > 0 {
			uncapped = ", uncapped"
		}
		fmt.Fprintf(w, "djinn: %v; workers run in their process group%s\n", err, uncapped)
		return nil
	}
	if guard > 0 && !scopes.MemoryController && memory == 0 {
		notes = append(notes, fmt.Errorf("%w: systemd does not give the memory controller to your user (%s)", machine.ErrNoMemoryLimit, machine.Delegate).Error())
	}
	for _, note := range notes {
		fmt.Fprintf(w, "djinn: %s\n", note)
	}
	var caps []string
	if scopes.CPU > 0 {
		caps = append(caps, fmt.Sprintf("its CPU capped at %d%% of a core", scopes.CPU))
	}
	if scopes.Memory > 0 {
		caps = append(caps, fmt.Sprintf("its memory at %d MiB", scopes.Memory>>20))
	}
	if guard > 0 && (scopes.MemoryController || memory > 0) {
		caps = append(caps, fmt.Sprintf("its memory guarded at %gx provider peak", guard))
	}
	if len(caps) > 0 {
		fmt.Fprintf(w, "djinn: each worker runs in a systemd scope of its own, %s\n", strings.Join(caps, ", "))
	}
	return scopes
}

// buildOf is a build committed, as the window proposes it.
func buildOf(b harness.Built) *uiv1.Build {
	summaries := make([]*uiv1.TaskSummary, len(b.Summaries))
	for i, s := range b.Summaries {
		summaries[i] = &uiv1.TaskSummary{
			Code:    s.Code,
			Title:   s.Title,
			Summary: s.Summary,
		}
	}
	return &uiv1.Build{
		WishId: b.WishID, WishTitle: b.WishTitle, ProjectId: b.ProjectID, Project: b.Project, Branch: b.Branch, Sha: b.Sha,
		Tasks: b.Tasks, Changes: b.Changes, Summaries: summaries, Install: b.Install,
		Base: b.Base, Running: b.Running, ChangesCount: int32(b.ChangesCount), Lacks: b.Lacks, LacksCount: int32(b.LacksCount),
	}
}
