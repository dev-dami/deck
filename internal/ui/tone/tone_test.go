package tone

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTintAndMark(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind string
		tint string
		mark string
	}{
		{"claude", Hex(Claude), "✻"},
		{"codex", Hex(Codex), ">_"},
		{"opencode", Hex(Opencode), "▯"},
		{"other", Hex(Muted), "▌"},
		{"", Hex(Muted), "▌"},
	}
	for _, c := range cases {
		if got := Hex(Tint(c.kind)); got != c.tint {
			t.Errorf("Tint(%q) = %s, want %s", c.kind, got, c.tint)
		}
		if got := Mark(c.kind); got != c.mark {
			t.Errorf("Mark(%q) = %q, want %q", c.kind, got, c.mark)
		}
	}
	if Hex(Tint("claude")) == Hex(Tint("codex")) {
		t.Error("claude and codex share a tint")
	}
}

func TestHex(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"#5fd7af", "#000000", "#ffffff", "#0e1211"} {
		if got := Hex(lipgloss.Color(in)); got != in {
			t.Errorf("Hex(%s) = %s", in, got)
		}
	}
	if got := Hex(lipgloss.Color("#5FD7AF")); got != "#5fd7af" {
		t.Errorf("Hex upper case = %s, want lower case", got)
	}
	if got := Hex(nil); got != "" {
		t.Errorf("Hex(nil) = %q", got)
	}
}

func TestNameFromHex(t *testing.T) {
	t.Parallel()
	if got := NameFromHex(Hex(Mint)); got != "mint" {
		t.Errorf("NameFromHex(mint) = %q", got)
	}
	if got := NameFromHex("#5FD7AF"); got != "mint" {
		t.Errorf("NameFromHex upper case = %q", got)
	}
	if got := NameFromHex("#123456"); got != "" {
		t.Errorf("NameFromHex(unknown) = %q", got)
	}
}

func TestByName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"mint", "Mint", "MINT"} {
		hex, ok := ByName(name)
		if !ok || hex != Hex(Mint) {
			t.Errorf("ByName(%q) = %q, %v", name, hex, ok)
		}
	}
	if _, ok := ByName("charple"); ok {
		t.Error("ByName found a colour that is not in the palette")
	}
}

func TestNamesRoundTrip(t *testing.T) {
	t.Parallel()
	names := Names()
	if len(names) == 0 {
		t.Fatal("no names")
	}
	for i, n := range names {
		if i > 0 && names[i-1] >= n {
			t.Errorf("names not sorted: %q before %q", names[i-1], n)
		}
		if _, ok := ByName(n); !ok {
			t.Errorf("ByName(%q) not found", n)
		}
	}
}
