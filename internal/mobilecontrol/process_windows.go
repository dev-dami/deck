package mobilecontrol

import (
	"os/exec"
	"strconv"
	"syscall"
)

func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200 | 0x08000000}
}
func stopChild(c *exec.Cmd) error {
	// The supervisor owns this live child handle. taskkill also closes Wrangler's
	// workerd subprocess; no PID loaded from a state file is ever passed here.
	kill := exec.Command("taskkill", "/PID", strconv.Itoa(c.Process.Pid), "/T", "/F")
	detach(kill)
	return kill.Run()
}
func forceChild(c *exec.Cmd) error { return stopChild(c) }
