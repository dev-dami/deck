package workspace

import "testing"

func TestAgentLineContext(t *testing.T) {
	if _, _, ok := (AgentLine{ContextTokens: 10}).ContextPct(); ok {
		t.Fatal("no window, no percentage")
	}
	if pct, approx, ok := (AgentLine{ContextTokens: 50, ContextWindow: 200, ContextSource: "fallback"}).ContextPct(); !ok || pct != 25 || !approx {
		t.Fatalf("pct %d approx %v ok %v", pct, approx, ok)
	}

	b, c, d, f, ok := AgentLine{ContextTokens: 120, ContextWindow: 200, ContextBaseline: 20, DeckTokens: 30}.ContextParts()
	if !ok || b != 20 || c != 70 || d != 30 || f != 80 {
		t.Fatalf("parts %d/%d/%d/%d", b, c, d, f)
	}
	// Estimates larger than the fill are cut to it; the parts still add up.
	b, c, d, f, _ = AgentLine{ContextTokens: 100, ContextWindow: 200, ContextBaseline: 80, DeckTokens: 50}.ContextParts()
	if b != 80 || c != 0 || d != 20 || f != 100 {
		t.Fatalf("clamped parts %d/%d/%d/%d", b, c, d, f)
	}
	// A fill past the window (a stale window) shows full, not negative free.
	if _, _, _, f, _ = (AgentLine{ContextTokens: 300, ContextWindow: 200}).ContextParts(); f != 0 {
		t.Fatalf("free %d", f)
	}
}
