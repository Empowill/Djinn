package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"connectrpc.com/connect"

	"github.com/empowill/djinn"
	"github.com/empowill/djinn/gen/go/demo/v1/demov1connect"
	planv1 "github.com/empowill/djinn/gen/go/plan/v1"
	"github.com/empowill/djinn/gen/go/plan/v1/planv1connect"
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/backup"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/demo"
	"github.com/empowill/djinn/internal/gate"
	"github.com/empowill/djinn/internal/harness"
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
	flags := flag.NewFlagSet("up", flag.ContinueOnError)
	browser := flags.Bool("browser", false, "print the URL to open in a browser instead of opening a window")
	port := flags.Int("port", 0, "port of the loopback HTTP server (with --browser, and on Windows); 0 picks a free one")
	term := flags.String("terminal", "", "command the terminal of the window runs, through the user's shell, "+
		"e.g. \"claude --resume <session>\"; empty runs the shell itself")
	termDir := flags.String("terminal-dir", "", "working directory of the terminal; empty is the folder of the first "+
		"project, as the window lists them (never the home folder: with no project, the window asks to create one)")
	maxWorkers := flags.Int("workers", 0, "most workers at once, 1 to 16; 0 decides from the machine "+
		"(one per 2 cores and per 2 GiB of memory); default $DJINN_WORKERS")
	warmWorkers := flags.Bool("warm-workers", false, "keep a claude loaded and waiting for the next task of each "+
		"project of the active wishes, in the slots the running workers leave: it starts at once, and costs memory "+
		"while it waits (about 300 MB each, supposed), no token")
	workerCPU := flags.Int("worker-cpu", 0, "cap each worker's CPU at this percent of one core (150 is a core and a "+
		"half), in a systemd user scope of its own; Linux with systemd only, and only where systemd gives your user the "+
		"cpu controller, else workers run uncapped and djinn says why; 0 caps nothing; default $DJINN_WORKER_CPU")
	if err := flags.Parse(args); err != nil {
		return false, err
	}
	if flags.NArg() > 0 {
		return false, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *maxWorkers == 0 && os.Getenv("DJINN_WORKERS") != "" {
		if _, err := fmt.Sscan(os.Getenv("DJINN_WORKERS"), maxWorkers); err != nil {
			return false, fmt.Errorf("DJINN_WORKERS: %w", err)
		}
	}
	if *maxWorkers < 0 || *maxWorkers > 16 {
		return false, fmt.Errorf("--workers %d: expected 1 to 16, or 0 to decide from the machine", *maxWorkers)
	}
	if *workerCPU == 0 && os.Getenv("DJINN_WORKER_CPU") != "" {
		if _, err := fmt.Sscan(os.Getenv("DJINN_WORKER_CPU"), workerCPU); err != nil {
			return false, fmt.Errorf("DJINN_WORKER_CPU: %w", err)
		}
	}
	if most := 100 * runtime.NumCPU(); *workerCPU < 0 || *workerCPU > most {
		return false, fmt.Errorf("--worker-cpu %d: expected 1 to %d (percent of one core), or 0 for no cap", *workerCPU, most)
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
	policy := machine.DefaultPolicy()
	policy.Workers = *maxWorkers
	read := readMachine
	if read == nil {
		read = machine.Reader(home)
	}
	monitor := machine.NewMonitor(policy, read)
	opts := []harness.Option{harness.WithCapacity(monitor.Capacity)}
	if *warmWorkers {
		opts = append(opts, harness.WithWarm())
	}
	if *workerCPU > 0 {
		if prefix, err := machine.CPULimit(ctx, *workerCPU); err != nil {
			fmt.Fprintf(os.Stderr, "djinn: %v; workers run uncapped\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "djinn: each worker runs in a systemd scope, its CPU capped at %d%% of a core\n", *workerCPU)
			opts = append(opts, harness.WithPrefix(prefix))
		}
	}
	workers := harness.New(db, home, harness.Providers(), opts...)
	defer workers.Close()
	if err := workers.Recover(ctx); err != nil {
		return false, err
	}
	workers.Schedule()
	gates := gate.New(monitor.Pressure, workers)
	workers.HeldGates(gates.Held, gates.Waiting)
	if *termDir != "" {
		if *termDir, err = filepath.Abs(*termDir); err != nil {
			return false, err
		}
	}
	// The terminals hang up before the workers stop: the lead may be driving them. The note of the leads follows
	// them until djinn up stops, and is left as it is before they hang up: a crash, or an error, keeps it.
	leadNotes := newLeadNote(home, os.Stderr)
	// The window's terminal opens in the first project, never in the home folder, where djinn starts from a menu.
	terminals := terminal.NewManager(terminal.Config{
		Command: terminal.ShellCommand(*term), Dir: *termDir, Changed: leadNotes.update,
		Folder: func() (string, error) { return plan.FirstProjectFolder(ctx, db) },
	})
	leadNotes.terms = terminals
	defer terminals.Close()
	defer leadNotes.freeze()
	if !hasWindow && !*browser {
		fmt.Fprintln(os.Stderr, "djinn: this build has no native window, serving the browser instead")
		*browser = true
	}
	transport := server.TransportFor(runtime.GOOS)
	if *browser {
		transport = server.HTTP
	}
	uiSvc, err := ui.New(version)
	if err != nil {
		return false, err
	}
	// After an update, the terminals that ran before it run again; after a crash, the leads.
	uiSvc.SetNotResumed(resumeTerminals(home, terminals, uiSvc, os.Stderr))
	// Then the lead of the first active wish, unless a lead came back already.
	resumeFirstLead(ctx, db, terminals, uiSvc, os.Stderr)
	updates, err := newUpdater(version, uiSvc, terminals, leadNotes, stop)
	if err != nil {
		return false, err
	}
	defer func() { restart = err == nil && updates.Restarting() }()
	if updates != nil {
		uiSvc.Restart = updates.restart
		go updates.run(ctx)
	}
	raise := make(chan struct{}, 1)
	var url string // In browser mode, the page to open, token included.
	uiSvc.Window = !*browser
	if !*browser {
		uiSvc.ChooseFolder = chooseFolder
	}
	uiSvc.Raise = func() {
		if *browser {
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
	if !*browser {
		uiSvc.Shortcuts = shortcuts
	}
	// The pages of the synced wishes follow every change, until djinn up stops.
	pages := plan.NewPages(db, home, version)
	go pages.Run(ctx)
	svc := services(db, home, workers, terminals, uiSvc, pages)
	machinePrefix, machineHandler := machine.Handler(monitor, workers.Running)
	svc[machinePrefix] = machineHandler
	gatePrefix, gateHandler := gate.Handler(gates)
	svc[gatePrefix] = gateHandler
	costsPrefix, costsHandler := machine.CostsHandler(db)
	svc[costsPrefix] = costsHandler
	backupPrefix, backupHandler := backup.Handler(db, home, version)
	svc[backupPrefix] = backupHandler
	h := server.Handler(djinn.UI(), svc)

	var ln net.Listener
	var addr string
	switch transport {
	case server.Wails:
		if ln, err = server.ListenUnix(filepath.Join(home, server.SocketFile)); err != nil {
			return false, err
		}
		addr = "unix://" + ln.Addr().String()
	case server.HTTP:
		if ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port)); err != nil {
			return false, err
		}
		origin := "http://" + ln.Addr().String()
		token := server.NewToken()
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
	// The window shows each question asked in an active wish as a system notification. An answer from one goes
	// through the server, as the window's would.
	notices := &ui.Notices{
		Store: db, Language: render.SystemLanguage(),
		Show:   func(wishID string) { leads{terminals, uiSvc}.Show(wishID, "") },
		Answer: func(ctx context.Context, id string, choice planv1.Choice) error { return answer(ctx, addr, id, choice) },
	}
	go notices.Run(ctx)

	switch {
	case *browser:
		fmt.Println("djinn: open", url)
		err = <-served
	default:
		if transport == server.Wails {
			err = openWindow(ctx, "", h, raise, notices, shortcuts)
		} else {
			err = openWindow(ctx, url, nil, raise, notices, shortcuts)
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
) map[string]http.Handler {
	demoPrefix, demoHandler := demov1connect.NewDemoServiceHandler(demo.Service{})
	uiPrefix, uiHandler := uiv1connect.NewUiServiceHandler(uiSvc)
	language := render.SystemLanguage()
	out := plan.Handlers(db, plan.WithAnswered(h.Answered), plan.WithLeads(leads{terminals, uiSvc}), plan.WithPages(pages),
		plan.WithLanguage(language), plan.WithWatchers(h.SpawnWatcher), plan.WithHome(home),
		plan.WithWorkers(h))
	// A watcher wakes the lead of its wish, as an answer does; its done line offers to grant a wish made from a
	// template.
	wishes := &plan.Wishes{Store: db, Leads: leads{terminals, uiSvc}, Language: language, Workers: h}
	h.TellLeads(wishes.Tell)
	h.OnWatched(wishes.Watched)
	// The inbox sources of the projects' skills run until djinn up stops; what they print waits for a click.
	h.RunSources((&plan.Inbox{Wishes: wishes}).Receive)
	out[demoPrefix] = demoHandler
	out[uiPrefix] = uiHandler
	taskPrefix, taskHandler := harness.Handler(h)
	out[taskPrefix] = taskHandler
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

func (l leads) Show(wishID, name string) {
	if l.ui.Raise != nil {
		l.ui.Raise()
	}
	l.ui.Present(wishID, name)
}

// answer answers a question through the djinn that answers at addr.
func answer(ctx context.Context, addr, questionID string, choice planv1.Choice) error {
	client, base, err := cli.Dial(addr)
	if err != nil {
		return err
	}
	_, err = planv1connect.NewQuestionServiceClient(client, base).Answer(ctx, connect.NewRequest(&planv1.QuestionServiceAnswerRequest{
		Question: &planv1.QuestionRef{Ref: &planv1.QuestionRef_Id{Id: questionID}}, Choice: choice,
	}))
	return err
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
