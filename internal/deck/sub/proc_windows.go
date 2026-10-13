//go:build windows

package sub

import (
	"os/exec"
	"syscall"
)

// hide keeps git from flashing a console window.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
