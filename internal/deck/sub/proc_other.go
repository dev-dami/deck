//go:build !windows

package sub

import "os/exec"

func hide(*exec.Cmd) {}
