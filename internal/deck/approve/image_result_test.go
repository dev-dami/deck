package approve

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent"
)

func TestMCPImageResultRoundTrip(t *testing.T) {
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	// Every CLI shares this MCP helper. Exercise both its Claude approval
	// variant and the ordinary two-tool variant with real broker transport.
	for _, name := range []string{"claude", "codex", "opencode", "antigravity", "pi"} {
		t.Run(name, func(t *testing.T) {
			b, err := NewBroker()
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			b.SetResultExecutor(func(_ context.Context, _ string, op string, _ json.RawMessage) agent.OpResult {
				switch op {
				case "browser.screenshot":
					return agent.OpResult{Text: "tab t1 " + b.Token(), Images: []agent.OpImage{{MimeType: "image/png", Data: pngBytes.Bytes()}}}
				case "invalid":
					return agent.OpResult{Images: []agent.OpImage{{MimeType: "image/png", Data: []byte("broken")}}}
				default:
					return agent.TextResult("plain text", false)
				}
			})
			call := mcpClient(t, b, name, name == "claude")
			m := call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{"op":"browser.screenshot"}}}`)
			res, ok := m["result"].(map[string]any)
			if !ok {
				t.Fatalf("missing result: %v", m)
			}
			content := res["content"].([]any)
			if len(content) != 2 {
				t.Fatalf("got %d content blocks", len(content))
			}
			txt := content[0].(map[string]any)
			im := content[1].(map[string]any)
			if txt["type"] != "text" || !strings.Contains(txt["text"].(string), "[redacted]") {
				t.Fatal("metadata not scrubbed")
			}
			if im["type"] != "image" || im["mimeType"] != "image/png" {
				t.Fatalf("wrong image block: %v", im)
			}
			data, err := base64.StdEncoding.DecodeString(im["data"].(string))
			if err != nil || !bytes.Equal(data, pngBytes.Bytes()) {
				t.Fatal("image bytes changed over MCP")
			}
			if text, bad := resultText(t, call(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"execute","arguments":{"op":"invalid"}}}`)); !bad || !strings.Contains(text, "Invalid image result") {
				t.Fatal("corrupt image was delivered")
			}
			if text, bad := resultText(t, call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute","arguments":{"op":"git.status"}}}`)); bad || text != "plain text" {
				t.Fatal("text result changed")
			}
		})
	}
}

func TestMCPImageBudgetIsSeparateFromText(t *testing.T) {
	// Incompressible enough to exceed the 64 KiB text budget. Padding valid
	// PNG bytes also checks that the binary transport never text-truncates.
	var bpng bytes.Buffer
	if err := png.Encode(&bpng, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	imageBytes := append(bpng.Bytes(), bytes.Repeat([]byte{0x91, 0x17, 0x03}, maxExecOutput)...)
	b, err := NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.SetResultExecutor(func(context.Context, string, string, json.RawMessage) agent.OpResult {
		return agent.OpResult{Text: strings.Repeat("x", maxExecOutput*2), Images: []agent.OpImage{{MimeType: "image/png", Data: imageBytes}}}
	})
	call := mcpClient(t, b, "codex", false)
	m := call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{"op":"browser.screenshot"}}}`)
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatal("missing result")
	}
	content := res["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if len(text) > maxExecOutput+200 || !strings.Contains(text, "truncated") {
		t.Fatal("text budget not enforced")
	}
	im := content[1].(map[string]any)
	got, err := base64.StdEncoding.DecodeString(im["data"].(string))
	if err != nil || !bytes.Equal(got, imageBytes) {
		t.Fatal("image was truncated by text budget")
	}
}

func TestResultExecutorCancelledWhenCallerLeaves(t *testing.T) {
	b, err := NewBroker()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	started, cancelled := make(chan struct{}), make(chan struct{})
	b.SetResultExecutor(func(ctx context.Context, _, _ string, _ json.RawMessage) agent.OpResult {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return agent.TextResult("cancelled", true)
	})
	c, err := net.Dial("tcp", b.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := json.NewEncoder(c).Encode(wireRequest{Token: b.Token(), Kind: kindExec, AgentID: "codex", Op: "browser.snapshot"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not start")
	}
	c.Close()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnected caller left browser work running")
	}
}
