package hub

import (
	"context"
	"encoding/json"
	"errors"
)

func highlightOps() []Op {
	key := Arg{Name: "key", Type: tString, Desc: "stable key within your findings (max 128 characters)"}
	id := Arg{Name: "id", Type: tString, Desc: "highlight id; supply id or key"}
	kind := Arg{Name: "kind", Type: tString, Desc: "needs_you, blocker or finding (default)"}
	title := Arg{Name: "title", Type: tString, Desc: "one line, at most 160 characters"}
	detail := Arg{Name: "detail", Type: tString, Desc: "optional explanation, at most 2000 characters"}
	source := Arg{Name: "source", Type: tJSON, Desc: "same-room {message_id} or {task_id}; otherwise the engine stamps your current turn"}
	requiredKey, requiredTitle := key, title
	requiredKey.Required = true
	requiredTitle.Required = true
	return []Op{
		{Name: "highlight.add", Desc: "Flag a blocker, decision or key finding for the user. Dedupe by your key; identical retries do not create unread noise. Re-adding preserves resolved/dismissed state.", Args: []Arg{requiredKey, kind, requiredTitle, detail, source}, run: highlightOp("highlight.add")},
		{Name: "highlight.update", Desc: "Update your finding by id or key. Omitted detail clears it. State remains unchanged.", Args: []Arg{id, key, kind, title, detail, source}, run: highlightOp("highlight.update")},
		{Name: "highlight.resolve", Desc: "Explicitly resolve your finding by id or key. Does not approve requests or complete tasks.", Args: []Arg{id, key}, run: highlightOp("highlight.resolve")},
		{Name: "highlight.dismiss", Desc: "Explicitly dismiss your finding by id or key. Does not dismiss underlying approvals.", Args: []Arg{id, key}, run: highlightOp("highlight.dismiss")},
		{Name: "highlight.list", Desc: "Read this room's explicit findings and latest agent summaries. Reading never marks items seen or resolved.", run: highlightOp("highlight.list")},
		{Name: "summary.update", Desc: "Publish your current status in one line (max 280 characters), with optional same-room source. Explicit updates only; no automatic summarizer.", Args: []Arg{{Name: "text", Type: tString, Required: true}, source}, run: highlightOp("summary.update")},
	}
}
func highlightOp(op string) func(context.Context, *Env, args) (string, error) {
	return func(ctx context.Context, env *Env, a args) (string, error) {
		if env.Highlights == nil {
			return "", errors.New("Highlights are not available in this workspace")
		}
		raw, err := json.Marshal(a)
		if err != nil {
			return "", err
		}
		return env.Highlights(ctx, op, raw)
	}
}
