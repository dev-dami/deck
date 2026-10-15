//go:build !windows

package backend

import "os/exec"

func hideWindow(*exec.Cmd) {}
