//go:build !windows

package rpc

import (
	"errors"
	"os"
	"os/exec"

	creack "github.com/creack/pty"
)

// errNoPTY says this system cannot run a terminal.
var errNoPTY = errors.New("terminals are not supported on this system")

type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixPTY) Resize(cols, rows uint16) error {
	return creack.Setsize(p.f, &creack.Winsize{Cols: cols, Rows: rows})
}

func (p *unixPTY) Wait() int {
	err := p.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

func (p *unixPTY) Close() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.f.Close()
}

// startPTY starts the user's shell in dir on a new pseudo-terminal.
func startPTY(dir string, cols, rows uint16) (pty, error) {
	cmd := exec.Command(shell())
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	f, err := creack.StartWithSize(cmd, &creack.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

// shell is the user's login shell, or a POSIX one.
func shell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}
