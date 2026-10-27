package approve

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// askOverMCP sends testdata/ask_call.json, Claude's --permission-prompt-tool
// call for an AskUserQuestion, through the MCP server, lets answer reply to
// the request it raises, and returns the decision Claude would read.
func askOverMCP(t *testing.T, b *Broker, answer func(agent.ApprovalRequest)) map[string]any {
	t.Helper()
	ch := b.Register("claude")
	call, err := os.ReadFile("testdata/ask_call.json")
	if err != nil {
		t.Fatal(err)
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { _ = MCPServe(inR, outW, b.Addr(), b.Token(), "claude", true); outW.Close() }()
	defer inW.Close()
	go func() { answer(recv(t, ch)) }()
	io.WriteString(inW, strings.TrimSpace(string(call))+"\n")
	line, err := bufio.NewReader(outR).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil || len(res.Result.Content) != 1 {
		t.Fatalf("reply %q: %v", line, err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(res.Result.Content[0].Text), &d); err != nil {
		t.Fatalf("decision %q: %v", res.Result.Content[0].Text, err)
	}
	return d
}

func TestAskRaisesQuestionAndReturnsAnswers(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	var got agent.ApprovalRequest
	d := askOverMCP(t, b, func(r agent.ApprovalRequest) {
		got = r
		r.Respond(agent.Decision{Allow: true, Answers: map[string]agent.Answer{
			r.Questions[0].ID: {Selected: []string{"Post as comments"}},
			r.Questions[1].ID: {Selected: []string{"Lint", "Unit"}, Other: "and go vet"},
		}})
	})
	if got.Kind != agent.ApproveQuestion || got.ID != "toolu_01AskQ" || got.ToolName != AskToolName || len(got.Questions) != 2 {
		t.Fatalf("request %+v", got)
	}
	q := got.Questions[0]
	if q.Header != "Delivery" || q.Multi || !q.Other || len(q.Options) != 2 || q.Options[0].Label != "Post as comments" || q.Options[0].Description == "" {
		t.Fatalf("question %+v", q)
	}
	if !got.Questions[1].Multi || got.Summary != q.Text {
		t.Fatalf("second %+v summary %q", got.Questions[1], got.Summary)
	}
	// Allow, with the input as it came plus answers keyed by question text:
	// what AskUserQuestion reads as the user's reply.
	if d["behavior"] != "allow" {
		t.Fatalf("decision %v", d)
	}
	in := d["updatedInput"].(map[string]any)
	if in["metadata"].(map[string]any)["source"] != "review" || len(in["questions"].([]any)) != 2 {
		t.Fatalf("input changed: %v", in)
	}
	answers := in["answers"].(map[string]any)
	if len(answers) != 2 ||
		answers["The reviews are done but only reported here. How should I deliver them?"] != "Post as comments" ||
		answers["Which checks should I re-run?"] != "Lint, Unit, and go vet" {
		t.Fatalf("answers %v", answers)
	}
}

func TestAskDeclined(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	d := askOverMCP(t, b, func(r agent.ApprovalRequest) { r.Respond(agent.Decision{Allow: false}) })
	if d["behavior"] != "deny" || d["message"] != agent.QuestionDeclined || d["updatedInput"] != nil {
		t.Fatalf("decline %v", d)
	}
	// An answer with nothing in it says the same.
	d = askOverMCP(t, b, func(r agent.ApprovalRequest) { r.Respond(agent.Decision{Allow: true}) })
	if d["behavior"] != "deny" || d["message"] != agent.QuestionDeclined {
		t.Fatalf("empty answer %v", d)
	}
}

// An "always" never answers a question for the person.
func TestAskNotAutoAllowed(t *testing.T) {
	b, _ := NewBroker()
	defer b.Close()
	b.state("claude").always[AskToolName] = true
	asked := false
	d := askOverMCP(t, b, func(r agent.ApprovalRequest) {
		asked = true
		r.Respond(agent.Decision{Allow: true, Always: true, Answers: map[string]agent.Answer{r.Questions[0].ID: {Other: "later"}}})
	})
	if !asked || d["behavior"] != "allow" {
		t.Fatalf("asked=%v %v", asked, d)
	}
}
