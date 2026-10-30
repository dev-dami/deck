package agent

import (
	"strings"
	"testing"
)

func TestKeepsRaw(t *testing.T) {
	for _, k := range []Kind{KindAssistantText, KindReasoning, KindToolCall, KindToolResult, KindUsage, KindTurnDone, KindRestarting, KindRestarted, KindCompacted} {
		if KeepsRaw(k) {
			t.Errorf("KeepsRaw(%s) = true, want false", k)
		}
	}
	for _, k := range []Kind{KindError, "something_new"} {
		if !KeepsRaw(k) {
			t.Errorf("KeepsRaw(%s) = false, want true", k)
		}
	}
}

func TestApproxContextAndCapsOf(t *testing.T) {
	for src, want := range map[string]bool{
		ContextCLI: false, ContextDerived: false, "": false,
		ContextEstimated: true, ContextFallback: true,
	} {
		if ApproxContext(src) != want {
			t.Errorf("ApproxContext(%q) = %v", src, !want)
		}
	}
	if c := CapsOf(nil); c != (ContextCaps{}) {
		t.Errorf("an adapter without caps claims nothing, got %+v", c)
	}
}

// TestRefLines covers the line each attachment goes out with: numbered per
// kind, with the label unless it only repeats the place.
func TestRefLines(t *testing.T) {
	got := RefLines([]Image{
		{MediaType: "image/png", Label: "Image 1"},
		{MediaType: "video/mp4", Label: "the crash"},
		{MediaType: "image/jpeg", Label: "  login\npage   error "},
		{MediaType: "image/png"},
		{MediaType: "video/webm", Label: "video 2"},
	})
	want := []string{"[Image 1]", "[Video 1: the crash]", "[Image 2: login page error]", "[Image 3]", "[Video 2]"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if n := len([]rune(CleanLabel(strings.Repeat("é", 80)))); n != MaxLabelRunes {
		t.Errorf("a long label keeps %d runes", n)
	}
}
