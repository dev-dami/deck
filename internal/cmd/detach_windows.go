package cmd

import (
	"os/exec"
	"syscall"
)

// detach lets a child outlive this process and never opens a console window.
func detach(c *exec.Cmd) {
	const (
		detachedProcess       = 0x00000008
		createNewProcessGroup = 0x00000200
		createNoWindow        = 0x08000000
	)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup | createNoWindow}
}
