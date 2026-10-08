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
	uiv1 "github.com/empowill/djinn/gen/go/ui/v1"
	"github.com/empowill/djinn/gen/go/ui/v1/uiv1connect"
	"github.com/empowill/djinn/internal/backup"
	"github.com/empowill/djinn/internal/cli"
	"github.com/empowill/djinn/internal/demo"
	"github.com/empowill/djinn/internal/gate"
	"github.com/empowill/djinn/internal/harness"
	"github.com/empowill/djinn/internal/machine"
	"github.com/empowill/djinn/internal/plan"
	"github.com/empowill/djinn/internal/server"
	"github.com/empowill/djinn/internal/store"
	"github.com/empowill/djinn/internal/terminal"
	"github.com/empowill/djinn/internal/ui"
)

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
	termDir := flags.String("terminal-dir", "", "working directory of the terminal; empty is the home directory")
	maxWorkers := flags.Int("workers", 0, "most workers at once, 1 to 16; 0 decides from the machine "+
		"(one per 2 cores and per 2 GiB of memory); default $DJINN_WORKERS")
	warmWorkers := flags.Bool("warm-workers", false, "keep a claude loaded and waiting for the next task of each "+
		"project of the active wishes, in the slots the running workers leave: it starts at once, and costs memory "+
		"while it waits (about 300 MB each, supposed), no token")
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
	monitor := machine.NewMonitor(policy, nil)
	opts := []harness.Option{harness.WithCapacity(monitor.Capacity)}
	if *warmWorkers {
		opts = append(opts, harness.WithWarm())
	}
	workers := harness.New(db, home, harness.Providers(), opts...)
	defer workers.Close()
	if err := workers.Recover(ctx); err != nil {
		return false, err
	}
	workers.Schedule()
	gates := gate.New(monitor.Pressure, workers)
	if *termDir != "" {
		if *termDir, err = filepath.Abs(*termDir); err != nil {
			return false, err
		}
	}
	// The terminals hang up before the workers stop: the lead may be driving them.
	terminals := terminal.NewManager(terminal.Config{Command: terminal.ShellCommand(*term), Dir: *termDir})
	defer terminals.Close()
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
	// After an update, the terminals that ran before it run again.
	uiSvc.SetNotResumed(resumeTerminals(home, terminals, uiSvc, os.Stderr))
	updates, err := newUpdater(version, home, uiSvc, terminals, stop)
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
	// The pages of the synced wishes follow every change, until djinn up stops.
	pages := plan.NewPages(db, home, version)
	go pages.Run(ctx)
	svc := services(db, workers, terminals, uiSvc, pages)
	machinePrefix, machineHandler := machine.Handler(monitor, workers.Running)
	svc[machinePrefix] = machineHandler
	gatePrefix, gateHandler := gate.Handler(gates)
	svc[gatePrefix] = gateHandler
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

	switch {
	case *browser:
		fmt.Println("djinn: open", url)
		return false, <-served
	case transport == server.Wails:
		err = openWindow(ctx, "", h, raise)
	default:
		err = openWindow(ctx, url, nil, raise)
	}
	stop() // The window is closed: stop the server too.
	return false, errors.Join(err, <-served)
}

// services returns the Connect services, by path prefix: the window's, the plan's on the database, the tasks' on
// the harness, and the terminals'.
func services(
	db *store.Store, h *harness.Harness, terminals *terminal.Manager, uiSvc *ui.Service, pages *plan.Pages,
) map[string]http.Handler {
	demoPrefix, demoHandler := demov1connect.NewDemoServiceHandler(demo.Service{})
	uiPrefix, uiHandler := uiv1connect.NewUiServiceHandler(uiSvc)
	out := plan.Handlers(db, plan.WithAnswered(h.Answered), plan.WithLeads(leads{terminals, uiSvc}), plan.WithPages(pages))
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

func (l leads) Show(wishID, name string) {
	if l.ui.Raise != nil {
		l.ui.Raise()
	}
	l.ui.Present(wishID, name)
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
