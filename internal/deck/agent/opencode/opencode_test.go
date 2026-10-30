package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/agent/acp/acptest"
)

// The test binary doubles as a scripted `opencode acp` (acptest).
func TestMain(m *testing.M) {
	acptest.Main()
	os.Exit(m.Run())
}

func TestStartAsksFirstAndRunsAPlanSession(t *testing.T) {
	log := filepath.Join(t.TempDir(), "acp.log")
	t.Setenv(acptest.EnvLog, log)
	t.Setenv(acptest.EnvDir, t.TempDir())
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"x","permission":{"read":"allow"}}`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := New("").Start(ctx, agent.Spec{ID: "opencode", Dir: t.TempDir(), Binary: os.Args[0], Model: "p/b", Mode: agent.ModePlan})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if ev := <-r.Events(); ev.Kind != agent.KindSessionStarted || ev.Text != "p/b" {
		t.Fatalf("first event = %+v", ev)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var env map[string]any
	_ = json.Unmarshal([]byte(strings.TrimPrefix(lines[0], "env ")), &env)
	perm, _ := env["permission"].(map[string]any)
	if env["theme"] != "x" || perm["read"] != "allow" || perm["edit"] != "ask" || perm["bash"] != "ask" {
		t.Errorf("inline config = %v", env)
	}
	if !strings.Contains(string(b), `"configId":"mode","value":"plan"`) {
		t.Errorf("plan mode runs OpenCode's plan agent:\n%s", b)
	}
	if New("").Name() != "opencode" || !New("").(agent.ContractSafe).ContractSafe() {
		t.Error("opencode is contract safe")
	}
}

// TestTakesVideo covers Deck's table of the OpenCode models sent video:
// Gemini chat models under any provider, nothing else.
func TestTakesVideo(t *testing.T) {
	for model, want := range map[string]bool{
		"google/gemini-3-pro":                   true,
		"google-vertex/gemini-2.5-flash":        true,
		"openrouter/google/gemini-2.5-pro":      true,
		"opencode/gemini-3-flash":               true,
		"google/gemini-embedding-001":           false,
		"google/gemini-2.5-flash-image-preview": false,
		"google/gemini-2.5-flash-preview-tts":   false,
		"anthropic/claude-opus-5-5":             false,
		"openai/gpt-5.6-sol":                    false,
		"google/gemma-3-27b":                    false,
		"":                                      false,
	} {
		if got := TakesVideo(model); got != want {
			t.Errorf("TakesVideo(%q) = %v, want %v", model, got, want)
		}
	}
	if !New("").(agent.VideoInput).TakesVideo("google/gemini-3-pro") {
		t.Error("the adapter answers from the table")
	}
}
