//go:build windows

package rpc

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// errNoPTY says this system cannot run a terminal: ConPTY came with
// Windows 10 1809.
var errNoPTY = errors.New("terminals need Windows 10 version 1809 or later")

// conPTY is a shell on a Windows pseudo console. The shell reads in and
// writes out through two pipes; conhost turns its console calls into VT
// sequences on out.
type conPTY struct {
	in  *os.File // our end of the shell's input
	out *os.File // our end of the shell's output

	mu      sync.Mutex
	hpc     windows.Handle // 0 once closed
	process windows.Handle

	exited chan struct{}
	code   int
	once   sync.Once
}

func (p *conPTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *conPTY) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *conPTY) Resize(cols, rows uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hpc == 0 {
		return nil
	}
	return windows.ResizePseudoConsole(p.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

// closeConsole ends the pseudo console. Its output pipe then reaches EOF,
// which a shell's exit alone does not cause. It waits for conhost to
// flush, so the output must keep being read (pump does).
func (p *conPTY) closeConsole() {
	p.mu.Lock()
	hpc := p.hpc
	p.hpc = 0
	p.mu.Unlock()
	if hpc != 0 {
		windows.ClosePseudoConsole(hpc)
	}
}

// watch waits for the shell to end, keeps its exit code and closes the
// console so that Read ends.
func (p *conPTY) watch() {
	code := -1
	if ev, err := windows.WaitForSingleObject(p.process, windows.INFINITE); err == nil && ev == windows.WAIT_OBJECT_0 {
		var c uint32
		if windows.GetExitCodeProcess(p.process, &c) == nil {
			code = int(c)
		}
	}
	p.code = code
	close(p.exited)
	p.closeConsole()
}

func (p *conPTY) Wait() int {
	<-p.exited
	p.once.Do(func() {
		_ = p.out.Close()
		_ = windows.CloseHandle(p.process)
	})
	return p.code
}

func (p *conPTY) Close() error {
	select {
	case <-p.exited:
	default:
		_ = windows.TerminateProcess(p.process, 1)
	}
	return p.in.Close()
}

// startPTY starts a shell in dir on a new pseudo console.
func startPTY(dir string, cols, rows uint16) (pty, error) {
	if windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find() != nil {
		return nil, errNoPTY
	}
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		closeHandles(inR, inW)
		return nil, err
	}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &hpc); err != nil {
		closeHandles(inR, inW, outR, outW)
		return nil, err
	}
	// conhost holds its own copies of these ends now.
	closeHandles(inR, outW)
	in, out := os.NewFile(uintptr(inW), "conpty-in"), os.NewFile(uintptr(outR), "conpty-out")
	fail := func(err error) (pty, error) {
		windows.ClosePseudoConsole(hpc)
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	// The attribute's value is the HPCON itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		return fail(err)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	// With no std handles of its own the shell would inherit Deck's
	// redirected stdio (the app's pipes) instead of the pseudo console. As
	// node-pty does: say we pass them, and pass none.
	si.Flags = windows.STARTF_USESTDHANDLES

	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{shell()}))
	if err != nil {
		return fail(err)
	}
	wdir, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return fail(err)
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, flags, nil, wdir, &si.StartupInfo, &pi); err != nil {
		return fail(err)
	}
	_ = windows.CloseHandle(pi.Thread)
	p := &conPTY{in: in, out: out, hpc: hpc, process: pi.Process, exited: make(chan struct{})}
	go p.watch()
	return p, nil
}

func closeHandles(hs ...windows.Handle) {
	for _, h := range hs {
		_ = windows.CloseHandle(h)
	}
}

// shell is PowerShell 7 when it is installed, else Windows PowerShell,
// else the command prompt.
func shell() string {
	for _, sh := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p
		}
	}
	if sh := os.Getenv("ComSpec"); sh != "" {
		return sh
	}
	return "cmd.exe"
}
