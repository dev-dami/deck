package tui

import (
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/tui/theme"
)

func TestComposerEditing(t *testing.T) {
	var c composer
	c.Insert("hello world")
	if c.Value() != "hello world" || c.cur != 11 {
		t.Fatalf("%q %d", c.Value(), c.cur)
	}
	c.DeleteWordBack()
	if c.Value() != "hello " {
		t.Fatalf("word delete: %q", c.Value())
	}
	c.Left()
	c.Left()
	c.Insert("XY")
	if c.Value() != "hellXYo " {
		t.Fatalf("insert mid: %q", c.Value())
	}
	c.Backspace()
	c.Delete()
	if c.Value() != "hellX " {
		t.Fatalf("backspace/delete: %q", c.Value())
	}
	c.Home()
	c.DeleteToEnd()
	if c.Value() != "" {
		t.Fatalf("kill to end: %q", c.Value())
	}
	c.Insert("a\r\nb\tc\x07")
	if c.Value() != "a\nb    c" {
		t.Fatalf("normalize: %q", c.Value())
	}
}

func TestComposerGrowsToSixRowsThenScrolls(t *testing.T) {
	var c composer
	if c.Height(40) != 1 {
		t.Fatal("empty composer is one row")
	}
	for i := 0; i < 10; i++ {
		c.Insert("line\n")
	}
	if c.Height(40) != maxComposerRows {
		t.Fatalf("height = %d", c.Height(40))
	}
	rows := c.View(40, theme.GlyphBar+" ", "", true)
	if len(rows) != maxComposerRows {
		t.Fatalf("view rows = %d", len(rows))
	}
}

func TestComposerWrapsAtWidthWithoutLosingText(t *testing.T) {
	var c composer
	c.Insert(strings.Repeat("word ", 30) + "日本語日本語日本語")
	rows := c.View(30, "› ", "", true)
	for _, r := range rows {
		if sw(r) > 30 {
			t.Fatalf("row %q is %d wide", r, sw(r))
		}
	}
	if c.Height(30) < 3 {
		t.Fatalf("expected wrapping, height %d", c.Height(30))
	}
}

func TestComposerVerticalMovement(t *testing.T) {
	var c composer
	c.Insert("abcdef\nxy\nlonger line")
	if !c.MoveVertical(-1, 40) || c.cur != 9 { // end of "xy" row clamps to col 2... "abcdef\nxy" -> index 9
		t.Fatalf("up: cur=%d", c.cur)
	}
	if !c.MoveVertical(-1, 40) {
		t.Fatal("up to first row")
	}
	if c.MoveVertical(-1, 40) {
		t.Fatal("cannot move above the first row")
	}
	if !c.MoveVertical(1, 40) {
		t.Fatal("down")
	}
}

func TestComposerPlaceholderAndCursor(t *testing.T) {
	var c composer
	rows := c.View(40, "› ", "message claude…", true)
	if len(rows) != 1 || !strings.Contains(ansiStrip(rows[0]), "message claude…") {
		t.Fatalf("%q", rows)
	}
}
