//go:build !windows

package vsh

import (
	"os/exec"
	"syscall"
)

// killGroup starts the shim in its own process group and kills the whole
// group at the timeout, so its children stop too.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
