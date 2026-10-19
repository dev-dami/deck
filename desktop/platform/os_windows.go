package platform

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// detach keeps a console program (cmd, code.cmd) from flashing a window of
// its own; terminals open theirs anyway.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// LaunchAtLogin reports whether the app starts when the user signs in.
func LaunchAtLogin() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("Deck")
	return err == nil
}

// SetLaunchAtLogin adds or removes the app from the user's sign-in items.
func SetLaunchAtLogin(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("Deck"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("Deck", `"`+exe+`"`)
}
