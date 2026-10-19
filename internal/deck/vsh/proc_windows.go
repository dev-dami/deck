//go:build windows

package vsh

import "os/exec"

// killGroup keeps the default cancel on Windows, which kills the shim only.
func killGroup(*exec.Cmd) {}
