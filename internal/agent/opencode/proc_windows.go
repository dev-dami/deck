//go:build windows

package opencode

import (
	"os/exec"
	"strconv"
	"syscall"
)

// configureProc is a no-op on Windows; the tree is killed with taskkill.
func configureProc(cmd *exec.Cmd) {}

// killTree kills the process and all its descendants.
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	k := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = k.Run()
	_ = cmd.Process.Kill() // fallback if taskkill is missing or the pid was reused
}
