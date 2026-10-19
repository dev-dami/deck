//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// detach lets a child outlive this process by giving it its own session.
func detach(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
