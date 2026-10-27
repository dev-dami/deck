package codex

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// Codex's request_user_input (item/tool/requestUserInput) is raised as a
// question, and the answers go back by question id; a declined one goes
// back with no answers.
func TestRequestUserInputIsAQuestion(t *testing.T) {
	ask := func(id int, item string) string {
		return recv(`{"id":` + strconv.Itoa(id) + `,"method":"item/tool/requestUserInput","params":{"threadId":"thr-syn","turnId":"turn-syn","itemId":"` + item + `","questions":[` +
			`{"id":"scope","header":"Scope","question":"Which packages?","isOther":true,"isSecret":false,"options":[{"label":"All","description":"every package"},{"label":"Changed","description":"only what changed"}]},` +
			`{"id":"name","header":"Name","question":"Branch name?","isOther":false,"isSecret":false,"options":null}]}}`)
	}
	script := writeScript(t,
		send(`{"id":1,"method":"initialize","params":{}}`),
		recv(`{"id":1,"result":{"userAgent":"x"}}`),
		send(`{"id":2,"method":"thread/start","params":{}}`),
		recv(`{"id":2,"result":{"thread":{"id":"thr-syn"}}}`),
		send(`{"id":3,"method":"turn/start","params":{}}`),
		recv(`{"id":3,"result":{"turn":{"id":"turn-syn","status":"inProgress"}}}`),
		recv(`{"method":"turn/started","params":{"threadId":"thr-syn","turn":{"id":"turn-syn","status":"inProgress"}}}`),
		ask(7, "call-1"),
		ask(8, "call-2"),
	)
	fe := newFakeEnv(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run, err := New(os.Args[0]).Start(ctx, agent.Spec{ID: "cx", Dir: t.TempDir(), Env: fe.env, FirstPrompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	drain(run)

	next := func() agent.ApprovalRequest {
		select {
		case q := <-run.Approvals():
			return q
		case <-time.After(15 * time.Second):
			t.Fatal("no question")
		}
		return agent.ApprovalRequest{}
	}
	q := next()
	if q.Kind != agent.ApproveQuestion || q.ID != "call-1" || len(q.Questions) != 2 || q.Summary != "Which packages?" {
		t.Fatalf("question = %+v", q)
	}
	if s := q.Questions[0]; s.ID != "scope" || s.Header != "Scope" || !s.Other || len(s.Options) != 2 || s.Options[1].Description != "only what changed" {
		t.Fatalf("scope = %+v", s)
	}
	if n := q.Questions[1]; !n.Other || len(n.Options) != 0 {
		t.Fatalf("a question without options is free text: %+v", n)
	}
	q.Respond(agent.Decision{Allow: true, Answers: map[string]agent.Answer{
		"scope": {Selected: []string{"Changed"}, Other: "and cmd/"},
		"name":  {Other: "fix-ask"},
	}})
	next().Respond(agent.Decision{Allow: false})

	waitFor(t, "both answers", func() bool {
		out := fe.read(t)
		return strings.Contains(out, `"id":7,"result":{"answers":{"name":{"answers":["fix-ask"]},"scope":{"answers":["Changed","and cmd/"]}}}`) &&
			strings.Contains(out, `"id":8,"result":{"answers":{}}`)
	})
}
