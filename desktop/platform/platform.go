// Package platform is the OS glue the app needs: file pickers, opening a
// folder in Explorer, an editor or a terminal, and launch at login.
package platform

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrCancelled is returned when the person closes a picker.
var ErrCancelled = errors.New("cancelled")

// ErrUnsupported is returned where this OS has no implementation yet.
var ErrUnsupported = errors.New("not supported on this system yet")

// Filter is one file-type choice of a picker, e.g. {"Images", "*.png;*.jpg"}.
type Filter struct{ Name, Pattern string }

func start(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Reveal opens dir in the system file manager.
func Reveal(dir string) error {
	switch runtime.GOOS {
	case "windows":
		return start(dir, "explorer.exe", dir)
	case "darwin":
		return start(dir, "open", dir)
	}
	return start(dir, "xdg-open", dir)
}

// OpenURL opens an http(s) link in the default browser; any other scheme
// is refused, since links come from agents' text.
func OpenURL(u string) error {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return errors.New("only web links open from the chat")
	}
	switch runtime.GOOS {
	case "windows":
		return start("", "rundll32.exe", "url.dll,FileProtocolHandler", u)
	case "darwin":
		return start("", "open", u)
	}
	return start("", "xdg-open", u)
}

// OpenEditor opens dir (or a file) in VS Code (or $VISUAL / $EDITOR when code is not
// installed).
func OpenEditor(dir string) error {
	if p, err := exec.LookPath("code"); err == nil {
		return start(dir, p, dir)
	}
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if e := os.Getenv(env); e != "" {
			return start(dir, e, dir)
		}
	}
	return errors.New("no editor found: install VS Code (the code command) or set EDITOR")
}

// OpenTerminal opens a terminal in dir.
func OpenTerminal(dir string) error {
	switch runtime.GOOS {
	case "windows":
		if p, err := exec.LookPath("wt.exe"); err == nil {
			return start(dir, p, "-d", dir)
		}
		return start(dir, "cmd.exe", "/c", "start", "cmd.exe", "/k")
	case "darwin":
		return start(dir, "open", "-a", "Terminal", dir)
	}
	for _, t := range []string{os.Getenv("TERMINAL"), "x-terminal-emulator", "gnome-terminal", "konsole", "xterm"} {
		if t == "" {
			continue
		}
		if p, err := exec.LookPath(t); err == nil {
			return start(dir, p)
		}
	}
	return errors.New("no terminal found: set TERMINAL")
}
