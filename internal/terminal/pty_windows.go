//go:build windows

package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winProc is a program attached to a Windows pseudo-console (ConPTY, Windows 10 1809 and later), in a Job Object
// that holds it and every process it starts.
type winProc struct {
	out     windows.Handle // the console's output, read by one goroutine at a time; 0 once closed
	process windows.Handle

	cmu     sync.Mutex
	console windows.Handle // 0 once closed: the handle is a pointer that ClosePseudoConsole frees

	wmu sync.Mutex
	in  windows.Handle // the console's input; 0 once closed

	jmu    sync.Mutex
	job    windows.Handle // 0 once closed
	killed atomic.Bool

	once sync.Once
}

func start(command []string, dir string, env []string, cols, rows int) (proc, error) {
	path, err := exec.LookPath(command[0])
	if err != nil {
		return nil, err
	}
	p := &winProc{}
	var inRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &p.in, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&p.out, &outWrite, nil, 0); err != nil {
		closeHandles(inRead, p.in)
		return nil, err
	}
	err = windows.CreatePseudoConsole(coord(cols, rows), inRead, outWrite, 0, &p.console)
	// The console holds copies of its own ends: the output reads EOF once the console is closed.
	closeHandles(inRead, outWrite)
	if err != nil {
		closeHandles(p.in, p.out)
		return nil, err
	}
	if err := p.spawn(path, command, dir, env); err != nil {
		closeHandles(p.out) // First: closing the console may wait for its output to be read.
		p.close()
		return nil, err
	}
	return p, nil
}

// spawn starts the program suspended on the console, puts it in a new job, then lets it run: whatever it starts
// joins the job before it can start anything.
func (p *winProc) spawn(path string, command []string, dir string, env []string) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself, a pointer of the system's: read its bits as one.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&p.console)),
		unsafe.Sizeof(p.console)); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command))
	if err != nil {
		return err
	}
	folder, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	block, err := envBlock(env)
	if err != nil {
		return err
	}
	if p.job, err = windows.CreateJobObject(nil, nil); err != nil {
		return err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// No standard handles: the program finds the console instead of those djinn was started with.
	si.Flags = windows.STARTF_USESTDHANDLES
	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(name, line, nil, nil, false, flags, block, folder, &si.StartupInfo, &pi); err != nil {
		return err
	}
	defer windows.CloseHandle(pi.Thread) //nolint:errcheck
	if err := windows.AssignProcessToJobObject(p.job, pi.Process); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		closeHandles(pi.Process)
		return fmt.Errorf("job object: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateJobObject(p.job, 1)
		closeHandles(pi.Process)
		return err
	}
	p.process = pi.Process
	return nil
}

func (p *winProc) Read(b []byte) (int, error) {
	if p.out == 0 {
		return 0, os.ErrClosed
	}
	var n uint32
	err := windows.ReadFile(p.out, b, &n, nil)
	if err != nil {
		// The console closed, or the read failed: the output ends either way.
		closeHandles(p.out)
		p.out = 0
		if errors.Is(err, windows.ERROR_BROKEN_PIPE) {
			err = io.EOF
		}
	}
	return int(n), err
}

func (p *winProc) Write(b []byte) (int, error) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.in == 0 {
		return 0, os.ErrClosed
	}
	var n uint32
	err := windows.WriteFile(p.in, b, &n, nil)
	return int(n), err
}

func (p *winProc) resize(cols, rows int) error {
	p.cmu.Lock()
	defer p.cmu.Unlock()
	if p.console == 0 {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(p.console, coord(cols, rows))
}

// wait waits for the program itself: exec.Cmd.Wait does not work with a pseudo-console. The console keeps its
// output open while it lives, so it is closed as soon as no process of the job is left to hold it: the output that
// remains comes, then EOF.
func (p *winProc) wait() int {
	code := -1
	if _, err := windows.WaitForSingleObject(p.process, windows.INFINITE); err == nil {
		var c uint32
		if windows.GetExitCodeProcess(p.process, &c) == nil && !p.killed.Load() {
			code = int(c)
		}
	}
	if p.active() == 0 {
		p.closeConsole()
	}
	return code
}

// accounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type accounting struct {
	TotalUserTime, TotalKernelTime                       int64
	ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime   int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses uint32
	TotalTerminatedProcesses                             uint32
}

// active counts the processes of the job still running; -1 when unknown.
func (p *winProc) active() int {
	p.jmu.Lock()
	defer p.jmu.Unlock()
	var acct accounting
	if p.job == 0 || windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&acct)), uint32(unsafe.Sizeof(acct)), nil) != nil {
		return -1
	}
	return int(acct.ActiveProcesses)
}

// hangup closes the pseudo-console, which sends its programs CTRL_CLOSE_EVENT; aside, as closing may wait for them,
// and the delay before kill starts now.
func (p *winProc) hangup() { go p.closeConsole() }

// kill ends the program and every process it started, which the job holds.
func (p *winProc) kill() {
	p.jmu.Lock()
	defer p.jmu.Unlock()
	if p.job != 0 {
		p.killed.Store(true)
		_ = windows.TerminateJobObject(p.job, 1)
	}
}

// close closes the console, its input and the job, which has no kill-on-close limit: a program the terminal started
// apart, a window, keeps running. The output is closed by the read that finds it ended.
func (p *winProc) close() {
	p.once.Do(func() {
		p.closeConsole()
		p.wmu.Lock()
		closeHandles(p.in)
		p.in = 0
		p.wmu.Unlock()
		p.jmu.Lock()
		closeHandles(p.job, p.process)
		p.job = 0
		p.jmu.Unlock()
	})
}

// closeConsole closes the pseudo-console once. Before Windows 11 24H2 it returns only once its output is read:
// the terminal reads it all along.
func (p *winProc) closeConsole() {
	p.cmu.Lock()
	console := p.console
	p.console = 0
	p.cmu.Unlock()
	if console != 0 {
		windows.ClosePseudoConsole(console)
	}
}

func coord(cols, rows int) windows.Coord { return windows.Coord{X: int16(cols), Y: int16(rows)} }

func closeHandles(hs ...windows.Handle) {
	for _, h := range hs {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
	}
}

// envBlock is env as CreateProcess takes it: each variable ended by a NUL, then a NUL. The last of a name wins,
// whatever its case, and SYSTEMROOT is always there, as with os/exec.
func envBlock(env []string) (*uint16, error) {
	last := map[string]int{}
	for i, kv := range env {
		if strings.IndexByte(kv, 0) >= 0 {
			return nil, fmt.Errorf("environment variable with a NUL: %q", kv)
		}
		last[strings.ToUpper(envName(kv))] = i
	}
	if _, ok := last["SYSTEMROOT"]; !ok {
		env = append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
		last["SYSTEMROOT"] = len(env) - 1
	}
	var block []uint16
	for i, kv := range env {
		if last[strings.ToUpper(envName(kv))] == i {
			block = append(append(block, utf16.Encode([]rune(kv))...), 0)
		}
	}
	block = append(block, 0)
	return &block[0], nil
}

// envName is the name of a variable; Windows' hidden ones start with '=' (=C:=C:\dir).
func envName(kv string) string {
	if kv == "" {
		return kv
	}
	if i := strings.IndexByte(kv[1:], '='); i >= 0 {
		return kv[:i+1]
	}
	return kv
}

// Shell is PowerShell when found, else the command interpreter.
func Shell() []string {
	if _, err := exec.LookPath("powershell.exe"); err == nil {
		return []string{"powershell.exe"}
	}
	return []string{comspec()}
}

// ShellCommand returns the command line that runs line through the command interpreter; nil for an empty line.
func ShellCommand(line string) []string {
	if line == "" {
		return nil
	}
	return []string{comspec(), "/c", line}
}

func comspec() string {
	if c := os.Getenv("COMSPEC"); c != "" && strings.HasSuffix(strings.ToLower(c), ".exe") {
		return c
	}
	return "cmd.exe"
}
