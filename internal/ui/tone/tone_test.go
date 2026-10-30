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
		{"claude", Hex(Dark.Claude), "✻"},
		{"codex", Hex(Dark.Codex), ">_"},
		{"opencode", Hex(Dark.Opencode), "▯"},
		{"other", Hex(Dark.Muted), "▌"},
		{"", Hex(Dark.Muted), "▌"},
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

// The tints are the desktop app's (styles.css, --agent-* under the Apple
// and Liquid Glass palettes), so an agent reads the same in both apps.
func TestTintsAreTheDesktops(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		p    Palette
		want []string
	}{
		{Dark, []string{"#ff9f5a", "#a1a1a6", "#64d2ff", "#bf5af2", "#30d158"}},
		{Light, []string{"#c4581b", "#6e6e73", "#0077a8", "#8944ab", "#248a3d"}},
	} {
		got := []string{Hex(c.p.Claude), Hex(c.p.Codex), Hex(c.p.Opencode), Hex(c.p.Antigravity), Hex(c.p.Pi)}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %s, want %s", Kinds[i], got[i], c.want[i])
			}
		}
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"claude": "claude", "codex-2": "codex", "Codex1": "codex", "claude/review-pr": "claude",
		"pi": "pi", "ada": "", "": "",
	} {
		if got := KindOf(in); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", in, got, want)
		}
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
	if got := NameFromHex(Hex(Dark.Signal)); got != "signal" {
		t.Errorf("NameFromHex(signal) = %q", got)
	}
	if got := NameFromHex("#0A84FF"); got != "signal" {
		t.Errorf("NameFromHex upper case = %q", got)
	}
	// A name of the earlier palette still resolves.
	if got := NameFromHex("#5FD7AF"); got != "mint" {
		t.Errorf("NameFromHex(mint) = %q", got)
	}
	if got := NameFromHex("#123456"); got != "" {
		t.Errorf("NameFromHex(unknown) = %q", got)
	}
}

func TestByName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"signal", "Signal", "SIGNAL"} {
		hex, ok := ByName(name)
		if !ok || hex != Hex(Dark.Signal) {
			t.Errorf("ByName(%q) = %q, %v", name, hex, ok)
		}
	}
	if hex, ok := ByName("mint"); !ok || hex != "#5fd7af" {
		t.Errorf("ByName(mint) = %q, %v", hex, ok)
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
