//go:build !windows

package mobilecontrol

import (
	"os/exec"
	"syscall"
)

func detach(c *exec.Cmd)           { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func stopChild(c *exec.Cmd) error  { return syscall.Kill(-c.Process.Pid, syscall.SIGTERM) }
func forceChild(c *exec.Cmd) error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
