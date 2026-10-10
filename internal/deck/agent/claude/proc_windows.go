//go:build windows

package claude

import (
	"os/exec"
	"strconv"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	// CREATE_NO_WINDOW: never flash a console window for the child.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
}

// killTree kills the process and all of its descendants.
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	k := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	_ = k.Run()
	_ = cmd.Process.Kill()
}
