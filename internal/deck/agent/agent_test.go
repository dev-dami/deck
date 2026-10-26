package agent

import "testing"

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
