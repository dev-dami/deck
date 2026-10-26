package claude

import (
	"os"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func TestContextArgs(t *testing.T) {
	cases := []struct {
		name  string
		p     agent.ContextPolicy
		model string
		want  []string
	}{
		{"defaults", agent.ContextPolicy{}, "opus", nil},
		{"inherit", agent.ContextPolicy{InstructionFiles: agent.InstrFilesInherit}, "opus", nil},
		{"isolate", agent.ContextPolicy{IsolateGlobalConfig: true}, "opus", []string{"--setting-sources", "project,local"}},
		{"project-only", agent.ContextPolicy{InstructionFiles: agent.InstrFilesProjectOnly}, "", []string{"--setting-sources", "project,local"}},
		{"none", agent.ContextPolicy{InstructionFiles: agent.InstrFilesNone, IsolateGlobalConfig: true}, "", []string{"--setting-sources", ""}},
		{"compact 1M", agent.ContextPolicy{CompactAtPct: 80}, "claude-opus-5-5", []string{"--autocompact", "800000"}},
		{"compact 200k", agent.ContextPolicy{CompactAtPct: 80}, "claude-sonnet-4-5", []string{"--autocompact", "160000"}},
		{"compact below the CLI's least", agent.ContextPolicy{CompactAtPct: 20}, "claude-sonnet-4-5", []string{"--autocompact", "100000"}},
		{"compact, model unknown", agent.ContextPolicy{CompactAtPct: 80}, "", nil},
	}
	for _, c := range cases {
		got := contextArgs(c.p, c.model)
		if strings.Join(got, "|") != strings.Join(c.want, "|") || len(got) != len(c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// TestContextFlagsInHelp checks the flags the context policy passes against
// the help of the CLI they were checked with (testdata/help_context.txt, from
// claude 2.1.310): a flag Deck passes must be one the CLI documents.
func TestContextFlagsInHelp(t *testing.T) {
	b, err := os.ReadFile("testdata/help_context.txt")
	if err != nil {
		t.Fatal(err)
	}
	help := string(b)
	for _, p := range []agent.ContextPolicy{{IsolateGlobalConfig: true, CompactAtPct: 80}, {InstructionFiles: agent.InstrFilesNone}} {
		for _, a := range contextArgs(p, "opus") {
			if strings.HasPrefix(a, "--") && !strings.Contains(help, "  "+a+" ") {
				t.Errorf("%s is not in the CLI's help", a)
			}
		}
	}
	for _, want := range []string{"(user, project, local)", "100k–1M tokens", "--strict-mcp-config"} {
		if !strings.Contains(help, want) {
			t.Errorf("help no longer says %q", want)
		}
	}
}

// TestMCPPolicyArgs covers mcp: inherit, which lets the user's own MCP
// servers load next to Deck's: no --strict-mcp-config.
func TestMCPPolicyArgs(t *testing.T) {
	for _, c := range []struct {
		mcp    string
		strict bool
	}{{"", true}, {agent.MCPDeckOnly, true}, {agent.MCPInherit, false}} {
		r := &run{a: &adapter{}, cfgPath: "/tmp/deck-mcp.json", mcpName: "deck", mode: agent.ModeAsk,
			spec: agent.Spec{Context: agent.ContextPolicy{MCP: c.mcp, IsolateGlobalConfig: true}}}
		args := r.args("")
		if contains(args, "--strict-mcp-config") != c.strict {
			t.Errorf("mcp %q: strict = %v, want %v", c.mcp, !c.strict, c.strict)
		}
		if v, ok := argAfter(args, "--setting-sources"); !ok || v != "project,local" {
			t.Errorf("mcp %q: --setting-sources %q %v", c.mcp, v, ok)
		}
	}
	// The CLI default's window is known once it said which model it runs.
	r := &run{a: &adapter{}, mode: agent.ModeAsk, spec: agent.Spec{Context: agent.ContextPolicy{CompactAtPct: 50}}}
	if contains(r.args(""), "--autocompact") {
		t.Error("--autocompact before the model is known")
	}
	r.running = "claude-opus-5-5"
	if v, _ := argAfter(r.args(""), "--autocompact"); v != "500000" {
		t.Errorf("--autocompact %q after the CLI said its model", v)
	}
}
