package agent

import "testing"

func TestKeepsRaw(t *testing.T) {
	for _, k := range []Kind{KindAssistantText, KindReasoning, KindToolCall, KindToolResult, KindUsage, KindTurnDone} {
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
