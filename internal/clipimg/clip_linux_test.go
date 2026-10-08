//go:build linux

package clipimg

import (
	"errors"
	"testing"
)

// onlyLinuxTools makes just the named tools look installed.
func onlyLinuxTools(t *testing.T, names ...string) {
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

func TestLinuxPicksWaylandUnderWayland(t *testing.T) {
	onlyLinuxTools(t, "wl-paste", "xclip")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	inv, ok := clipboardInvocation("/tmp/x.png")
	if !ok || inv.name != "wl-paste" || !inv.toStdout {
		t.Fatalf("got %+v ok=%v", inv, ok)
	}
	if len(inv.args) != 2 || inv.args[0] != "--type" || inv.args[1] != "image/png" {
		t.Fatalf("args = %v", inv.args)
	}
}

func TestLinuxPicksXclipUnderX11(t *testing.T) {
	onlyLinuxTools(t, "wl-paste", "xclip")
	t.Setenv("WAYLAND_DISPLAY", "")
	inv, ok := clipboardInvocation("/tmp/x.png")
	if !ok || inv.name != "xclip" || !inv.toStdout {
		t.Fatalf("got %+v ok=%v", inv, ok)
	}
	want := []string{"-selection", "clipboard", "-t", "image/png", "-o"}
	if len(inv.args) != len(want) {
		t.Fatalf("args = %v", inv.args)
	}
	for i := range want {
		if inv.args[i] != want[i] {
			t.Fatalf("args = %v", inv.args)
		}
	}
}

func TestLinuxSingleToolFallback(t *testing.T) {
	onlyLinuxTools(t, "wl-paste")
	t.Setenv("WAYLAND_DISPLAY", "")
	inv, ok := clipboardInvocation("/tmp/x.png")
	if !ok || inv.name != "wl-paste" {
		t.Fatalf("got %+v ok=%v", inv, ok)
	}
}

func TestLinuxNoTool(t *testing.T) {
	onlyLinuxTools(t)
	if _, ok := clipboardInvocation("/tmp/x.png"); ok {
		t.Fatal("expected no tool")
	}
}

func TestLinuxAnyFailureIsNoImage(t *testing.T) {
	if !noImage(1) {
		t.Fatal("exit 1 should mean no image")
	}
}
