//go:build !windows

package platform

import "os/exec"

func detach(*exec.Cmd) {}

// PickFolder is not implemented here yet; the app falls back to a typed
// path.
func PickFolder(uintptr, string) (string, error) { return "", ErrUnsupported }

// PickFiles is not implemented here yet.
func PickFiles(uintptr, string, ...Filter) ([]string, error) { return nil, ErrUnsupported }

// LaunchAtLogin is not implemented here yet.
func LaunchAtLogin() bool { return false }

// SetLaunchAtLogin is not implemented here yet.
func SetLaunchAtLogin(bool) error { return ErrUnsupported }
