//go:build windows

package clipimg

import (
	"strings"
	"testing"
)

func TestWindowsInvocationPassesPathByEnv(t *testing.T) {
	fakeTools(t)
	path := `C:\Temp\deck images\deck-clip-x.png`

	inv, ok := clipboardInvocation(path)
	if !ok {
		t.Fatal("clipboardInvocation not ok")
	}
	if inv.name != "powershell.exe" {
		t.Fatalf("name = %q", inv.name)
	}
	sta, cmd := -1, -1
	for i, a := range inv.args {
		switch a {
		case "-STA":
			sta = i
		case "-Command":
			cmd = i
		}
	}
	if sta < 0 || cmd < 0 || sta > cmd {
		t.Fatalf("args %v: want -STA before -Command", inv.args)
	}
	if len(inv.args) != cmd+2 || !strings.Contains(inv.args[cmd+1], "Clipboard]::GetImage") {
		t.Fatalf("script missing from args %v", inv.args)
	}
	script := inv.args[cmd+1]
	if strings.Contains(script, path) {
		t.Fatal("path spliced into the script")
	}
	if len(inv.env) != 1 || inv.env[0] != "DECK_CLIP_PATH="+path {
		t.Fatalf("env = %v", inv.env)
	}
}

func TestWindowsNoImageExitCode(t *testing.T) {
	if !noImage(3) {
		t.Fatal("exit 3 should mean no image")
	}
	if noImage(1) {
		t.Fatal("exit 1 should be a tool failure, not no image")
	}
}
