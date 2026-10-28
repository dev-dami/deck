package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// Deck's own MCP server (registered as "deck") gives every agent exactly two
// tools: search, which finds Deck's ops, skills and past threads, and
// execute, which runs one op. Claude also gets "approve", its permission
// prompt tool.
const (
	DeckToolSearch  = "search"
	DeckToolExecute = "execute"

	// OpThreadSend is the execute op that messages another agent in the
	// thread; it replaces the old send_message tool.
	OpThreadSend = "thread.send"
	// OpThreadRead is the execute op that reads the shared thread; it
	// replaces the old read_thread tool.
	OpThreadRead = "thread.read"
	// OpBrowserType types text into a page of the shared browser. What it
	// types may be a password, so no row, log or briefing shows it.
	OpBrowserType = "browser.type"
)

const typedMask = `"•••"`

// MaskTyped hides the text of a browser.type call in an execute call's
// input: the call is shown as typing into a field, not as what was typed.
// The JSON is read, not matched as bytes, so an escaped key or op name
// ("text") is masked too. Any other input comes back as it is.
func MaskTyped(input json.RawMessage) json.RawMessage {
	if ParseExecInput(input).Op != OpBrowserType {
		return input
	}
	out, ok := maskText(input, true)
	if !ok {
		return json.RawMessage(`{"op":"` + OpBrowserType + `","args":{"text":` + typedMask + `}}`)
	}
	return out
}

// maskText rewrites a JSON object with its "text" members masked, in key
// order, and its "args" object too when deep is set. ok is false when raw is
// not a JSON object.
func maskText(raw json.RawMessage, deep bool) (json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for n := 0; dec.More(); n++ {
		kt, err := dec.Token()
		key, isKey := kt.(string)
		if err != nil || !isKey {
			return nil, false
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, false
		}
		switch {
		case key == "text":
			v = json.RawMessage(typedMask)
		case deep && key == "args":
			if inner, ok := maskText(v, false); ok {
				v = inner
			}
		}
		if n > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), true
}

// Executor answers Deck's search and execute MCP tools for one agent. op is
// "search" for the search tool. isErr marks a result the agent should see as
// a failed call.
type Executor func(agentID, op string, args json.RawMessage) (text string, isErr bool)

// ResultExecutor additionally supports images. Text-only executors remain
// usable through SetExecutor; the session uses this path for browser ops.
type ResultExecutor func(ctx context.Context, agentID, op string, args json.RawMessage) OpResult

// ExecInput is the input of an execute call. Args may be absent when an
// agent put the op's arguments next to "op" instead of under "args".
type ExecInput struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ParseExecInput reads an execute call's input. Arguments given next to
// "op" (flattened) are moved under Args.
func ParseExecInput(input json.RawMessage) ExecInput {
	var in ExecInput
	_ = json.Unmarshal(input, &in)
	in.Op = strings.TrimSpace(in.Op)
	if len(in.Args) > 0 && !bytes.Equal(bytes.TrimSpace(in.Args), []byte("null")) {
		return in
	}
	in.Args = nil
	var flat map[string]json.RawMessage
	if json.Unmarshal(input, &flat) != nil {
		return in
	}
	delete(flat, "op")
	delete(flat, "args")
	if len(flat) > 0 {
		in.Args, _ = json.Marshal(flat)
	}
	return in
}

// DeckToolDisplay maps a call of one of Deck's own MCP tools (its bare name,
// "search" or "execute") to the name and title Deck draws it under: a
// thread.send becomes "message" ("to codex: ..."), a thread.read "read", and
// everything else "deck" with the op as the title. ok is false for any other
// tool.
func DeckToolDisplay(tool string, input json.RawMessage) (name, title string, ok bool) {
	switch tool {
	case DeckToolSearch:
		var in struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(input, &in)
		return "deck", strings.TrimSpace("search " + oneLineClip(in.Query, 80)), true
	case DeckToolExecute:
		in := ParseExecInput(input)
		switch in.Op {
		case OpThreadSend:
			var a struct {
				To      string `json:"to"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(in.Args, &a)
			return "message", "to " + a.To + ": " + oneLineClip(a.Message, 60), true
		case OpThreadRead:
			return "read", "shared thread", true
		}
		title := in.Op
		if in.Op == OpBrowserType {
			if masked, ok := maskText(in.Args, false); ok {
				in.Args = masked
			}
		}
		if args := compact(in.Args); args != "" {
			title += " " + args
		}
		if title == "" {
			title = "execute"
		}
		return "deck", oneLineClip(title, 160), true
	}
	return "", "", false
}

func oneLineClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}

func compact(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return strings.TrimSpace(string(raw))
	}
	s := buf.String()
	if s == "{}" || s == "null" {
		return ""
	}
	return s
}
