//go:build !windows

package gh

import "os/exec"

func hide(*exec.Cmd) {}
