package backend

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps the engine from opening a console window.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
