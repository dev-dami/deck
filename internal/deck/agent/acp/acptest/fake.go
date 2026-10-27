// Package acptest is a scripted ACP agent for the tests of Deck's ACP
// adapters. A test binary becomes it by calling Main from TestMain and
// naming itself as the adapter's binary: run with EnvLog set (the tests set
// it before starting a run), the binary answers ACP on stdio instead of
// running tests.
//
// The agent answers the handshake, keeps a session's config options, and
// plays one turn per prompt: a thought, an edit tool call with a permission
// request, the reply streamed in two chunks and a usage update. A prompt
// containing SLOW waits for session/cancel, BASH runs a command whose output
// streams in _meta (as pi-acp sends it), and CRASH exits mid-turn. Every
// request it gets is appended to the file named by EnvLog, one
// "method params" line each; the first line is "env <OPENCODE_CONFIG_CONTENT>".
package acptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// EnvLog names the request log, and turns the binary into the agent.
	EnvLog = "DECK_FAKE_ACP"
	// EnvFlavor picks the agent's options: "opencode" (default: model, mode,
	// and an "effort" with a "default" value) or "pi" (model and
	// thought_level with "off", no mode, a banner after session/new and no
	// session/resume).
	EnvFlavor = "DECK_FAKE_ACP_FLAVOR"
	// EnvDir is the folder the edit's file is in.
	EnvDir = "DECK_FAKE_DIR"
)

// Main runs the fake agent and exits when EnvLog is set; otherwise it
// returns at once.
func Main() {
	if log := os.Getenv(EnvLog); log != "" {
		serve(log, os.Getenv(EnvFlavor) == "pi")
		os.Exit(0)
	}
}

func serve(logPath string, pi bool) {
	logf, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	defer logf.Close()
	var wmu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		os.Stdout.Write(append(b, '\n'))
		wmu.Unlock()
	}
	note := func(s string) { fmt.Fprintln(logf, s) }
	note("env " + os.Getenv("OPENCODE_CONFIG_CONTENT"))

	model, effort, mode := "p/a", "default", "build"
	if pi {
		effort = "off"
	}
	reasons := func() bool { return model == "p/b" }
	options := func() []map[string]any {
		opts := []map[string]any{
			{"id": "model", "category": "model", "currentValue": model, "options": []map[string]string{
				{"value": "p/a", "name": "p/Model A"}, {"value": "p/b", "name": "p/Model B"}}},
		}
		switch {
		case pi:
			levels := []map[string]string{{"value": "off", "name": "Thinking: off"}}
			if reasons() {
				levels = append(levels, map[string]string{"value": "low", "name": "Thinking: low"}, map[string]string{"value": "high", "name": "Thinking: high"})
			}
			opts = append(opts, map[string]any{"id": "thought_level", "category": "thought_level", "currentValue": effort, "options": levels})
		default:
			opts = append(opts, map[string]any{"id": "mode", "category": "mode", "currentValue": mode, "options": []map[string]string{{"value": "build"}, {"value": "plan"}}})
			if reasons() {
				opts = append(opts, map[string]any{"id": "effort", "category": "thought_level", "currentValue": effort, "options": []map[string]string{
					{"value": "low", "name": "Low"}, {"value": "high", "name": "High"}, {"value": "default", "name": "Default"}}})
			}
		}
		return opts
	}
	update := func(u map[string]any) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "ses_1", "update": u}})
	}
	responses := make(chan map[string]any, 4)
	cancels := make(chan struct{}, 4)

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m struct {
			V      string          `json:"jsonrpc"`
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.V != "2.0" {
			note("missing jsonrpc 2.0")
		}
		if m.Method == "" { // a response to our permission request
			var r map[string]any
			_ = json.Unmarshal(m.Result, &r)
			responses <- r
			continue
		}
		note(m.Method + " " + string(m.Params))
		reply := func(res any) { send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": res}) }
		switch m.Method {
		case "initialize":
			sessionCaps := map[string]any{"resume": map[string]any{}, "delete": map[string]any{}}
			if pi {
				sessionCaps = map[string]any{"list": map[string]any{}, "delete": map[string]any{}}
			}
			reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{
				"loadSession": true, "promptCapabilities": map[string]any{"image": true},
				"sessionCapabilities": sessionCaps}})
		case "session/new":
			res := map[string]any{"sessionId": "ses_1", "configOptions": options()}
			if !pi {
				reply(res)
				break
			}
			// pi-acp's startup info: announced in the answer, then sent
			// as a message, outside any turn. A banner a client reads
			// only after its first prompt went out is still the banner.
			const banner = "pi v1 banner"
			res["_meta"] = map[string]any{"piAcp": map[string]any{"startupInfo": banner}}
			reply(res)
			update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": banner}})
		case "session/load":
			// The history comes back as updates before the answer.
			update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "old reply"}})
			reply(map[string]any{"configOptions": options()})
		case "session/resume", "session/delete":
			reply(map[string]any{"configOptions": options()})
		case "session/set_config_option":
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			_ = json.Unmarshal(m.Params, &p)
			switch p.ConfigID {
			case "model":
				model = p.Value
				if pi {
					effort = "off"
					if reasons() {
						effort = "low"
					}
				}
			case "mode":
				mode = p.Value
			case "effort", "thought_level":
				effort = p.Value
			}
			reply(map[string]any{"configOptions": options()})
		case "session/cancel":
			cancels <- struct{}{}
		case "session/prompt":
			id := *m.ID
			params := string(m.Params)
			go func() {
				switch {
				case strings.Contains(params, "SLOW"):
					<-cancels
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "cancelled"}})
					return
				case strings.Contains(params, "CRASH"):
					update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": "about to"}})
					fmt.Fprintln(os.Stderr, "fake agent crashed")
					os.Exit(3)
				case strings.Contains(params, "SELECT"):
					// pi-acp asks an extension's select prompt as one
					// allow_once option per choice.
					send(map[string]any{"jsonrpc": "2.0", "id": 101, "method": "session/request_permission", "params": map[string]any{
						"sessionId": "ses_1",
						"toolCall": map[string]any{"toolCallId": "pi-ui-7", "title": "Pick a theme", "kind": "other", "status": "pending",
							"rawInput": map[string]any{"method": "select", "title": "Pick a theme", "options": []string{"dark", "light", "auto"}}},
						"options": []map[string]string{{"optionId": "choice-0", "name": "dark", "kind": "allow_once"}, {"optionId": "choice-1", "name": "light", "kind": "allow_once"}, {"optionId": "choice-2", "name": "auto", "kind": "allow_once"}}}})
					r := <-responses
					b, _ := json.Marshal(r)
					note("select " + string(b))
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
					return
				case strings.Contains(params, "BASH"):
					update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "b1", "title": "go test ./...", "kind": "execute", "status": "in_progress",
						"content": []map[string]any{{"type": "terminal", "terminalId": "b1"}}})
					update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "b1", "status": "in_progress", "_meta": map[string]any{"terminal_output": map[string]any{"terminal_id": "b1", "data": "ok  \tpkg\n"}}})
					update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "b1", "status": "completed", "_meta": map[string]any{
						"terminal_output": map[string]any{"terminal_id": "b1", "data": "FAIL\n"}, "terminal_exit": map[string]any{"terminal_id": "b1", "exit_code": 1}}})
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
					return
				}
				update(map[string]any{"sessionUpdate": "agent_thought_chunk", "messageId": "r1", "content": map[string]any{"type": "text", "text": "think"}})
				update(map[string]any{"sessionUpdate": "agent_thought_chunk", "messageId": "r1", "content": map[string]any{"type": "text", "text": "ing"}})
				edit := map[string]any{"toolCallId": "t1", "name": "edit", "title": "edit", "kind": "edit", "status": "pending", "rawInput": map[string]any{}}
				input := map[string]any{"filePath": filepath.Join(os.Getenv(EnvDir), "a.go"), "oldString": "x", "newString": "y"}
				if pi { // pi-acp names the tool by its title only
					delete(edit, "name")
					input = map[string]any{"path": filepath.Join(os.Getenv(EnvDir), "a.go"), "oldText": "x", "newText": "y"}
				}
				update(merge(map[string]any{"sessionUpdate": "tool_call"}, edit))
				send(map[string]any{"jsonrpc": "2.0", "id": 100, "method": "session/request_permission", "params": map[string]any{
					"sessionId": "ses_1",
					"toolCall": merge(edit, map[string]any{"rawInput": input,
						"content": []map[string]any{{"type": "diff", "path": "a.go", "oldText": "x\n", "newText": "y\n"}}}),
					"options": []map[string]string{{"optionId": "once", "kind": "allow_once"}, {"optionId": "always", "kind": "allow_always"}, {"optionId": "reject", "kind": "reject_once"}}}})
				r := <-responses
				b, _ := json.Marshal(r)
				note("permission " + string(b))
				update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
					"content": []map[string]any{{"type": "content", "content": map[string]any{"type": "text", "text": "Edit applied"}}}})
				update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": "Hel"}})
				update(map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "m1", "content": map[string]any{"type": "text", "text": "lo"}})
				update(map[string]any{"sessionUpdate": "usage_update", "used": 10, "size": 100, "cost": map[string]any{"amount": 0.5, "currency": "USD"}})
				send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn",
					"usage": map[string]any{"inputTokens": 3, "outputTokens": 4, "cachedReadTokens": 1}}})
			}()
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "error": map[string]any{"code": -32601, "message": "no"}})
		}
	}
}

// merge is a copy of a with b's keys laid over it.
func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
