//go:build !windows

package changes

import "os/exec"

func hide(*exec.Cmd) {}
