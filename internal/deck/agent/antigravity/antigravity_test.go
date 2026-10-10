package antigravity

import (
	"context"
	"slices"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

const modelsFixture = "gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
	"gemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)\n" +
	"gemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n" +
	"gemini-3.1-pro-high\tGemini 3.1 Pro (High)\n" +
	"gemini-3.1-pro-low\tGemini 3.1 Pro (Low)\n" +
	"claude-sonnet-4-6\tClaude Sonnet 4.6 (Thinking)\n" +
	"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n"

func fixtureAdapter() *adapter {
	return &adapter{modelsFn: func(context.Context) (string, error) { return modelsFixture, nil }}
}

func TestModelsGrouping(t *testing.T) {
	a := fixtureAdapter()
	list, err := a.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]agent.ModelInfo{}
	for _, m := range list {
		byID[m.ID] = m
	}
	flash, ok := byID["gemini-3.8-flash"]
	if !ok {
		t.Fatalf("no flash family in %v", list)
	}
	if flash.Name != "Gemini 3.8 Flash" {
		t.Fatalf("family name = %q", flash.Name)
	}
	var levels []string
	for _, e := range flash.Efforts {
		levels = append(levels, e.Level)
	}
	if !slices.Equal(levels, []string{"low", "medium", "high"}) {
		t.Fatalf("efforts = %v", levels)
	}
	if flash.DefaultEffort != "medium" {
		t.Fatalf("default effort = %q", flash.DefaultEffort)
	}
	pro := byID["gemini-3.1-pro"]
	if len(pro.Efforts) != 2 || pro.DefaultEffort != "high" {
		t.Fatalf("pro = %+v", pro)
	}
	// Lone suffixed slugs stay standalone with no efforts.
	if m := byID["gpt-oss-120b-medium"]; len(m.Efforts) != 0 {
		t.Fatalf("gpt-oss = %+v", m)
	}
	if m := byID["claude-sonnet-4-6"]; len(m.Efforts) != 0 || m.Name != "Claude Sonnet 4.6 (Thinking)" {
		t.Fatalf("sonnet = %+v", m)
	}
	if d := byID[""]; !d.Default {
		t.Fatalf("no default model in %v", list)
	}
}

func TestResolveSlug(t *testing.T) {
	a := fixtureAdapter()
	for be, want := range map[[2]string]string{
		{"gemini-3.8-flash", "high"}:  "gemini-3.8-flash-high",
		{"gemini-3.8-flash", ""}:      "gemini-3.8-flash-medium",
		{"gemini-3.1-pro", ""}:        "gemini-3.1-pro-high",
		{"claude-sonnet-4-6", ""}:     "claude-sonnet-4-6",
		{"claude-sonnet-4-6", "high"}: "claude-sonnet-4-6",
		{"custom-model", "low"}:       "custom-model-low",
		{"custom-model", ""}:          "custom-model",
		{"", "high"}:                  "",
	} {
		if got := a.resolveSlug(context.Background(), be[0], be[1]); got != want {
			t.Fatalf("resolveSlug(%q,%q) = %q, want %q", be[0], be[1], got, want)
		}
	}
}

func TestModelsFallback(t *testing.T) {
	a := &adapter{modelsFn: func(context.Context) (string, error) { return "", context.DeadlineExceeded }}
	list, err := a.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 || !list[0].Default {
		t.Fatalf("fallback = %+v", list)
	}
}

func argsOf(mode agent.Mode, slug, resume string) []string {
	r := &run{a: &adapter{}, slug: slug, mode: mode}
	return r.args(resume)
}

func hasArgs(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		match := true
		for j, w := range want {
			if args[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestArgsModes(t *testing.T) {
	ask := argsOf(agent.ModeAsk, "", "")
	if !hasArgs(ask, "--input-format", "stream-json") || !hasArgs(ask, "--output-format", "stream-json") {
		t.Fatalf("ask = %v", ask)
	}
	for _, f := range []string{"--mode", "--dangerously-skip-permissions", "--model", "--conversation"} {
		if slices.Contains(ask, f) {
			t.Fatalf("ask should not contain %s: %v", f, ask)
		}
	}
	plan := argsOf(agent.ModePlan, "gemini-3.8-flash-medium", "conv-1")
	if !hasArgs(plan, "--mode", "plan") || !hasArgs(plan, "--model", "gemini-3.8-flash-medium") || !hasArgs(plan, "--conversation", "conv-1") {
		t.Fatalf("plan = %v", plan)
	}
	yolo := argsOf(agent.ModeYolo, "", "")
	if !hasArgs(yolo, "--mode", "accept-edits", "--dangerously-skip-permissions") {
		t.Fatalf("yolo = %v", yolo)
	}
}

func TestMcpAddArgs(t *testing.T) {
	var gotPath string
	var gotArgs []string
	a := &adapter{mcpRunFn: func(_ context.Context, path string, args ...string) error {
		gotPath, gotArgs = path, args
		return nil
	}}
	srv := &agent.MCPServer{
		Name: "deck", Command: "/opt/deck", Args: []string{"mcp-approve", "--addr", "127.0.0.1:9", "--agent", "x"},
		Env: map[string]string{"B_TOKEN": "b", "A_TOKEN": "a"},
	}
	name, err := a.mcpAdd(context.Background(), "agy", srv, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if name != "deck-antigravity" {
		t.Fatalf("name = %q", name)
	}
	want := []string{
		"mcp", "add", "--env", "A_TOKEN=a", "--env", "B_TOKEN=b",
		"deck-antigravity", "/opt/deck", "mcp-approve", "--addr", "127.0.0.1:9", "--agent", "x",
	}
	if gotPath != "agy" || !slices.Equal(gotArgs, want) {
		t.Fatalf("mcp add = %s %v, want %v", gotPath, gotArgs, want)
	}
}

func TestMcpServerName(t *testing.T) {
	for in, want := range map[string]string{
		"antigravity":  "deck-antigravity",
		"antigravity2": "deck-antigravity2",
		"a/b c":        "deck-a-b-c",
		"":             "deck",
	} {
		if got := mcpServerName(in); got != want {
			t.Fatalf("mcpServerName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokenTravelsAsEnvPair(t *testing.T) {
	var gotArgs []string
	a := &adapter{mcpRunFn: func(_ context.Context, _ string, args ...string) error {
		gotArgs = args
		return nil
	}}
	srv := &agent.MCPServer{Command: "/opt/deck", Env: map[string]string{"DECK_APPROVE_TOKEN": "spec-token"}}
	if _, err := a.mcpAdd(context.Background(), "agy", srv, "x"); err != nil {
		t.Fatal(err)
	}
	// The token must only ever appear as the value of a --env KEY=value
	// pair, never as a bare argument of its own.
	if !hasArgs(gotArgs, "--env", "DECK_APPROVE_TOKEN=spec-token") {
		t.Fatalf("no --env pair in %v", gotArgs)
	}
	for _, arg := range gotArgs {
		if arg == "spec-token" {
			t.Fatalf("bare token in %v", gotArgs)
		}
	}
}
