package approve

import (
	"encoding/json"

	"github.com/dev-dami/deck/internal/deck/agent"
)

// AskToolName is Claude Code's built-in tool for asking the user questions.
// Headless (stream-json) it has no terminal to ask on, so the CLI hands the
// call to the --permission-prompt-tool in every permission mode,
// bypassPermissions too: the tool needs the person. Deck raises it as an
// agent.ApproveQuestion and replies with an allow whose updatedInput is the
// tool's input plus "answers", keyed by question text (several labels joined
// with ", "), which the tool reports to the model as the user's answers. The
// CLI admits an updatedInput that adds answers and changes nothing else.
const AskToolName = "AskUserQuestion"

// askInput is the part of AskUserQuestion's input Deck shows.
type askInput struct {
	Questions []struct {
		Question string `json:"question"`
		Header   string `json:"header"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		MultiSelect bool `json:"multiSelect"`
	} `json:"questions"`
}

// askQuestions reads the questions out of an AskUserQuestion input. Claude
// keys answers by question text, so the text is the question's ID; its form
// always offers "Other".
func askQuestions(input json.RawMessage) []agent.Question {
	var in askInput
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	out := make([]agent.Question, 0, len(in.Questions))
	for _, q := range in.Questions {
		if q.Question == "" {
			continue
		}
		aq := agent.Question{ID: q.Question, Header: q.Header, Text: q.Question, Multi: q.MultiSelect, Other: true}
		for _, o := range q.Options {
			aq.Options = append(aq.Options, agent.QuestionOption{Label: o.Label, Description: o.Description})
		}
		out = append(out, aq)
	}
	return out
}

// answerQuestion is the permission reply to an AskUserQuestion call: allow
// with the answers merged into the input, or a deny that says the person
// declined (the tool then fails with that message rather than reporting
// "The user did not answer the questions"), whatever the reason: skipped,
// interrupted, the thread closed. An answer with nothing in it is a decline
// too.
func answerQuestion(input json.RawMessage, qs []agent.Question, d agent.Decision) wireResponse {
	declined := wireResponse{Behavior: "deny", Message: agent.QuestionDeclined}
	if !d.Allow {
		return declined
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil || fields == nil {
		return declined
	}
	answers := map[string]string{}
	for _, q := range qs {
		if t := d.Answers[q.ID].Text(); t != "" {
			answers[q.ID] = t
		}
	}
	if len(answers) == 0 {
		return declined
	}
	fields["answers"], _ = json.Marshal(answers)
	updated, err := json.Marshal(fields)
	if err != nil {
		return declined
	}
	return wireResponse{Behavior: "allow", UpdatedInput: updated}
}
