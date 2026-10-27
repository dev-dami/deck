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
