//go:build darwin

package clipimg

import (
	"errors"
	"strings"
	"testing"
)

// onlyTools makes just the named tools look installed.
func onlyTools(t *testing.T, names ...string) {
	t.Helper()
	old := lookPath
	lookPath = func(name string) (string, error) {
		for _, n := range names {
			if n == name {
				return "/fake/" + name, nil
			}
		}
		return "", errors.New(name + ": not found")
	}
	t.Cleanup(func() { lookPath = old })
}

func TestDarwinPrefersPngpaste(t *testing.T) {
	onlyTools(t, "pngpaste", "osascript")
	inv, ok := clipboardInvocation("/tmp/x.png")
	if !ok || inv.name != "pngpaste" || len(inv.args) != 1 || inv.args[0] != "/tmp/x.png" {
		t.Fatalf("got %+v ok=%v", inv, ok)
	}
}

func TestDarwinFallsBackToOsascript(t *testing.T) {
	onlyTools(t, "osascript")
	inv, ok := clipboardInvocation("/tmp/x.png")
	if !ok || inv.name != "osascript" {
		t.Fatalf("got %+v ok=%v", inv, ok)
	}
	// The path is an argv item, never spliced into the script.
	if inv.args[len(inv.args)-1] != "/tmp/x.png" {
		t.Fatalf("args = %v", inv.args)
	}
	if strings.Contains(inv.args[1], "/tmp/x.png") {
		t.Fatal("path spliced into the AppleScript")
	}
}

func TestDarwinNoTool(t *testing.T) {
	onlyTools(t)
	if _, ok := clipboardInvocation("/tmp/x.png"); ok {
		t.Fatal("expected no tool")
	}
}

func TestDarwinAnyFailureIsNoImage(t *testing.T) {
	if !noImage(1) {
		t.Fatal("exit 1 should mean no image")
	}
}
