//go:build windows

package rpc

import "errors"

// errNoPTY says this system cannot run a terminal.
var errNoPTY = errors.New("terminals are not supported on Windows yet")

func startPTY(string, uint16, uint16) (pty, error) { return nil, errNoPTY }
