package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
	"github.com/dev-dami/deck/internal/deck/approve"
)

// fakeAskClaude plays testdata/ask_question.jsonl the way Claude Code runs
// AskUserQuestion headless: the tool_use, then a --permission-prompt-tool
// call to Deck's approve tool (through Deck's MCP server, built from the
// --mcp-config it was given), then the tool_result the CLI would write from
// the decision. Like the CLI it refuses an updatedInput that changes more
// than "answers". The decision is recorded in DECK_FAKE_ASK_FILE.
func fakeAskClaude() {
	data, _ := os.ReadFile(filepath.Join(os.Getenv("DECK_FAKE_DIR"), "testdata", "ask_question.jsonl"))
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"control_request"`) {
			continue
		}
		var input json.RawMessage
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var l struct {
				Type    string `json:"type"`
				Message struct {
					Content []struct {
						Name  string          `json:"name"`
						Input json.RawMessage `json:"input"`
					} `json:"content"`
				} `json:"message"`
				ToolUseID string `json:"tool_use_id"`
			}
			_ = json.Unmarshal([]byte(line), &l)
			if l.Type == "assistant" && len(l.Message.Content) > 0 && l.Message.Content[0].Name == approve.AskToolName {
				input = l.Message.Content[0].Input
			}
			if l.Type != "deck_fake_ask" {
				os.Stdout.WriteString(line + "\n")
				continue
			}
			decision := askDeck(input, l.ToolUseID)
			if f := os.Getenv("DECK_FAKE_ASK_FILE"); f != "" {
				os.WriteFile(f, []byte(decision), 0o600)
			}
			os.Stdout.WriteString(toolResultLine(l.ToolUseID, input, decision) + "\n")
		}
	}
}

// askDeck makes the permission prompt call through Deck's MCP server and
// returns the decision text.
func askDeck(input json.RawMessage, toolUseID string) string {
	var cfgPath string
	for i, a := range os.Args {
		if a == "--mcp-config" && i+1 < len(os.Args) {
			cfgPath = os.Args[i+1]
		}
	}
	raw, _ := os.ReadFile(cfgPath)
	var cfg struct {
		MCPServers map[string]struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	_ = json.Unmarshal(raw, &cfg)
	srv := cfg.MCPServers["deck"]
	addr, _ := argAfter(srv.Args, "--addr")
	agentID, _ := argAfter(srv.Args, "--agent")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { _ = approve.MCPServe(inR, outW, addr, srv.Env[approve.TokenEnv], agentID, true); outW.Close() }()
	args, _ := json.Marshal(map[string]any{"tool_name": approve.AskToolName, "input": input, "tool_use_id": toolUseID})
	fmt.Fprintf(inW, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"approve","arguments":%s}}`+"\n", args)
	line, _ := bufio.NewReader(outR).ReadString('\n')
	inW.Close()
	var res struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(line), &res) != nil || len(res.Result.Content) == 0 {
		return ""
	}
	return res.Result.Content[0].Text
}

// toolResultLine is the user line Claude Code writes for the tool's result.
func toolResultLine(id string, input json.RawMessage, decision string) string {
	var d struct {
		Behavior     string                     `json:"behavior"`
		Message      string                     `json:"message"`
		UpdatedInput map[string]json.RawMessage `json:"updatedInput"`
	}
	_ = json.Unmarshal([]byte(decision), &d)
	content, isErr := "", false
	switch {
	case d.Behavior != "allow":
		content, isErr = d.Message, true
	case !admitted(input, d.UpdatedInput):
		content, isErr = "the permission answer changed the questions", true
	default:
		var answers map[string]string
		_ = json.Unmarshal(d.UpdatedInput["answers"], &answers)
		var parts []string
		var in askFixture
		_ = json.Unmarshal(input, &in)
		for _, q := range in.Questions {
			if a, ok := answers[q.Question]; ok {
				parts = append(parts, fmt.Sprintf("%q=%q", q.Question, a))
			}
		}
		content = "Your questions have been answered: " + strings.Join(parts, ", ") + ". You can now continue with these answers in mind."
	}
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": isErr},
	}}, "parent_tool_use_id": nil})
	return string(b)
}

type askFixture struct {
	Questions []struct {
		Question string `json:"question"`
	} `json:"questions"`
}

// admitted is the CLI's rule for an AskUserQuestion permission answer: the
// shown fields unchanged, plus answers keyed by asked question text.
func admitted(input json.RawMessage, updated map[string]json.RawMessage) bool {
	var orig map[string]json.RawMessage
	_ = json.Unmarshal(input, &orig)
	for k, v := range updated {
		if k == "answers" {
			continue
		}
		var a, b any
		_ = json.Unmarshal(orig[k], &a)
		_ = json.Unmarshal(v, &b)
		if !reflect.DeepEqual(a, b) {
			return false
		}
	}
	var answers map[string]any
	if json.Unmarshal(updated["answers"], &answers) != nil {
		return false
	}
	var in askFixture
	_ = json.Unmarshal(input, &in)
	asked := map[string]bool{}
	for _, q := range in.Questions {
		asked[q.Question] = true
	}
	for k, v := range answers {
		if _, ok := v.(string); !ok || !asked[k] {
			return false
		}
	}
	return len(updated) == len(orig)+1
}

// startAsk starts the adapter against the asking fake claude in mode.
func startAsk(t *testing.T, mode agent.Mode) (agent.Run, string) {
	t.Helper()
	a, spec, _ := newFake(t)
	spec.Env["DECK_FAKE_ASK"] = "1"
	decisionFile := filepath.Join(t.TempDir(), "decision.json")
	spec.Env["DECK_FAKE_ASK_FILE"] = decisionFile
	spec.Mode = mode
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err := r.Send("review it"); err != nil {
		t.Fatal(err)
	}
	return r, decisionFile
}

func nextQuestion(t *testing.T, r agent.Run) agent.ApprovalRequest {
	t.Helper()
	select {
	case req := <-r.Approvals():
		return req
	case <-time.After(15 * time.Second):
		t.Fatal("no question raised")
	}
	return agent.ApprovalRequest{}
}

// toolResult waits for the AskUserQuestion tool's result.
func toolResult(t *testing.T, r agent.Run) *agent.Tool {
	t.Helper()
	for _, ev := range waitKind(t, r, agent.KindTurnDone) {
		if ev.Kind == agent.KindToolResult && ev.Tool != nil && ev.Tool.Name == approve.AskToolName {
			return ev.Tool
		}
	}
	t.Fatal("no AskUserQuestion result")
	return nil
}

// In yolo (bypassPermissions) the CLI still hands AskUserQuestion to the
// permission prompt tool; Deck raises it as a question and the answers come
// back as the tool's result.
func TestAskUserQuestionIsAnsweredThroughDeck(t *testing.T) {
	r, decisionFile := startAsk(t, agent.ModeYolo)
	req := nextQuestion(t, r)
	if req.Kind != agent.ApproveQuestion || req.ID != "toolu_01AskQ" || len(req.Questions) != 2 || req.Questions[0].Header != "Delivery" {
		t.Fatalf("question %+v", req)
	}
	req.Respond(agent.Decision{Allow: true, Answers: map[string]agent.Answer{
		req.Questions[0].ID: {Selected: []string{"Post as comments"}},
		req.Questions[1].ID: {Selected: []string{"Lint"}, Other: "and vet"},
	}})
	tool := toolResult(t, r)
	if tool.Status != agent.ToolOK || !strings.Contains(tool.Output, `"The reviews are done but only reported here. How should I deliver them?"="Post as comments"`) ||
		!strings.Contains(tool.Output, `"Which checks should I re-run?"="Lint, and vet"`) {
		t.Fatalf("result %+v", tool)
	}
	raw, _ := os.ReadFile(decisionFile)
	var d struct {
		Behavior     string `json:"behavior"`
		UpdatedInput struct {
			Answers map[string]string `json:"answers"`
		} `json:"updatedInput"`
	}
	if json.Unmarshal(raw, &d) != nil || d.Behavior != "allow" || len(d.UpdatedInput.Answers) != 2 {
		t.Fatalf("decision %s", raw)
	}
}

func TestAskUserQuestionDeclined(t *testing.T) {
	r, _ := startAsk(t, agent.ModeAsk)
	req := nextQuestion(t, r)
	req.Respond(agent.Decision{Allow: false, Reason: "interrupted"})
	tool := toolResult(t, r)
	if tool.Status != agent.ToolError || tool.Output != agent.QuestionDeclined {
		t.Fatalf("result %+v", tool)
	}
}

// The tool call's title is the question, not its JSON.
func TestAskUserQuestionTitle(t *testing.T) {
	for _, ev := range replay(t, filepath.Join("testdata", "ask_question.jsonl")) {
		if ev.Kind == agent.KindToolCall && ev.Tool.Name == approve.AskToolName {
			if ev.Tool.Title != "The reviews are done but only reported here. How should I deliver them?" {
				t.Fatalf("title %q", ev.Tool.Title)
			}
			return
		}
	}
	t.Fatal("no tool call")
}
