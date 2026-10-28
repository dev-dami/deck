//go:build ignore

// Deterministic Claude stream-json fixture for browser-integration.cjs.
// Build explicitly with go build; it never calls a model or a real agent.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	var config string
	for i, arg := range os.Args {
		if arg == "--help" {
			fmt.Println("fixture --model --input-format --output-format")
			return
		}
		if arg == "--mcp-config" && i+1 < len(os.Args) {
			config = os.Args[i+1]
		}
	}
	if config == "" {
		return
	}
	emit := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": "browser-integration", "model": "fixture"})
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 4096), 8<<20)
	for sc.Scan() {
		var input struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &input) != nil || input.Type != "user" {
			continue
		}
		text := string(input.Message.Content)
		var literal string
		if json.Unmarshal(input.Message.Content, &literal) == nil {
			text = literal
		} else {
			var parts []struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(input.Message.Content, &parts)
			text = ""
			for _, p := range parts {
				text += p.Text
			}
		}
		answer := "Fixture ready"
		if at := strings.LastIndex(text, "FIXTURE:"); at >= 0 {
			var request struct {
				Op   string         `json:"op"`
				Args map[string]any `json:"args"`
			}
			if err := json.NewDecoder(strings.NewReader(text[at+len("FIXTURE:"):])).Decode(&request); err == nil {
				result, err := execute(config, request.Op, request.Args)
				if err != nil {
					result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
				}
				data, _ := json.Marshal(result)
				answer = "FIXTURE_RESULT " + string(data)
			} else {
				answer = "FIXTURE_ERROR " + err.Error()
			}
		}
		emit(map[string]any{"type": "assistant", "message": map[string]any{"id": fmt.Sprint(time.Now().UnixNano()), "role": "assistant", "model": "fixture", "content": []any{map[string]any{"type": "text", "text": answer}}}})
		emit(map[string]any{"type": "result", "subtype": "success", "result": answer, "session_id": "browser-integration", "usage": map[string]any{}})
	}
}

func execute(path, op string, args map[string]any) (map[string]any, error) {
	var cfg struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	srv := cfg.Servers["deck"]
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, srv.Command, srv.Args...)
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	enc := json.NewEncoder(stdin)
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "integration-fixture", "version": "1"}}})
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "execute", "arguments": map[string]any{"op": op, "args": args}}})
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 4096), 64<<20)
	for sc.Scan() {
		var response struct {
			ID     int             `json:"id"`
			Result map[string]any  `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &response) != nil || response.ID != 2 {
			continue
		}
		if len(response.Error) > 0 {
			return nil, fmt.Errorf("MCP error: %s", response.Error)
		}
		if blocks, ok := response.Result["content"].([]any); ok {
			for _, block := range blocks {
				b, ok := block.(map[string]any)
				if !ok || b["type"] != "image" {
					continue
				}
				image, err := base64.StdEncoding.DecodeString(fmt.Sprint(b["data"]))
				if err != nil {
					return nil, err
				}
				b["bytes"] = len(image)
				if len(image) >= 8 {
					b["signature"] = fmt.Sprintf("%x", image[:8])
				}
				delete(b, "data") // Keep the UI transcript small; image travelled through MCP.
			}
		}
		return response.Result, nil
	}
	return nil, fmt.Errorf("MCP returned no result: %v %s", sc.Err(), stderr.String())
}
