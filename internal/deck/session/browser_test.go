package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/dev-dami/deck/internal/deck/agent"
	pb "github.com/dev-dami/deck/pkg/rpc/deckv1"
)

// browserRig is a thread with a claude and a desktop that answers every
// command with "did <action>" and keeps them.
type browserRig struct {
	*rig
	tid, claude string
	mu          sync.Mutex
	got         []*pb.BrowserCommand
}

func newBrowserRig(t *testing.T, attached bool) *browserRig {
	t.Helper()
	b := &browserRig{rig: newRig(t, nil, nil)}
	b.tid = b.thread(t)
	b.claude = b.add(t, b.tid, "claude")
	if attached {
		b.m.SetBrowserAction(func(_ context.Context, _, _ string, c *pb.BrowserCommand) *pb.BrowserActionResponse {
			b.mu.Lock()
			b.got = append(b.got, c)
			b.mu.Unlock()
			return &pb.BrowserActionResponse{Tab: &pb.BrowserTab{Id: "t1"}, Result: &pb.OpResult{Text: "did " + c.Action}}
		})
	}
	return b
}

func (b *browserRig) commands() []*pb.BrowserCommand {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]*pb.BrowserCommand(nil), b.got...)
}

// op runs a browser op as the thread's claude.
func (b *browserRig) op(op string, args any) agent.OpResult {
	raw, _ := json.Marshal(args)
	return b.m.execOpResult(context.Background(), b.claude, op, raw)
}

func TestBrowserOpWithoutADesktop(t *testing.T) {
	b := newBrowserRig(t, false)
	res := b.op("browser.tabs", nil)
	if !res.IsError || !strings.Contains(res.Text, "browser_unavailable") {
		t.Fatalf("result = %+v", res)
	}
}

// Each op's arguments reach the desktop as the command it runs.
func TestBrowserOpsReachTheDesktopAsCommands(t *testing.T) {
	b := newBrowserRig(t, true)
	ref := map[string]any{"tab_id": "t1", "element_ref": "e12", "snapshot_version": "v1"}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range ref {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	for _, c := range []struct {
		op   string
		args any
		want func(*pb.BrowserCommand) bool
	}{
		{"browser.open", map[string]any{"url": "https://example.com"}, func(c *pb.BrowserCommand) bool { return c.Url == "https://example.com" }},
		{"browser.snapshot", map[string]any{"tab_id": "t1", "interactive": true, "max_lines": 40}, func(c *pb.BrowserCommand) bool { return c.Interactive && c.MaxLines == 40 }},
		{"browser.click", ref, func(c *pb.BrowserCommand) bool { return c.ElementRef == "e12" && c.SnapshotVersion == "v1" }},
		{"browser.select", with(map[string]any{"text": "Chile"}), func(c *pb.BrowserCommand) bool { return c.Text == "Chile" && c.ElementRef == "e12" }},
		{"browser.press", map[string]any{"tab_id": "t1", "key": "Control+A"}, func(c *pb.BrowserCommand) bool { return c.Key == "Control+A" }},
		{"browser.scroll", map[string]any{"tab_id": "t1", "dy": -300}, func(c *pb.BrowserCommand) bool { return c.Dy == -300 }},
		{"browser.scroll", map[string]any{"tab_id": "t1", "to": "bottom"}, func(c *pb.BrowserCommand) bool { return c.To == "bottom" }},
		{"browser.wait", map[string]any{"tab_id": "t1", "text": "Done", "timeout_s": 5}, func(c *pb.BrowserCommand) bool { return c.Text == "Done" && c.TimeoutS == 5 }},
		{"browser.back", map[string]any{"tab_id": "t1"}, func(c *pb.BrowserCommand) bool { return c.TabId == "t1" }},
		{"browser.screenshot", with(nil), func(c *pb.BrowserCommand) bool { return c.ElementRef == "e12" }},
	} {
		res := b.op(c.op, c.args)
		if res.IsError || !strings.Contains(res.Text, "did "+strings.TrimPrefix(c.op, "browser.")) {
			t.Fatalf("%s: %+v", c.op, res)
		}
		got := b.commands()
		if last := got[len(got)-1]; last.Action != strings.TrimPrefix(c.op, "browser.") || !c.want(last) {
			t.Fatalf("%s reached the desktop as %+v", c.op, last)
		}
	}
}

// Plan mode lets an agent read pages and refuses what acts on them, and the
// mode is read at the call, not when the thread started.
func TestBrowserPlanModeReadsButDoesNotAct(t *testing.T) {
	b := newBrowserRig(t, true)
	acts := map[string]any{
		"browser.click":  map[string]any{"tab_id": "t1", "element_ref": "e1", "snapshot_version": "v"},
		"browser.type":   map[string]any{"tab_id": "t1", "element_ref": "e1", "snapshot_version": "v", "text": "x"},
		"browser.select": map[string]any{"tab_id": "t1", "element_ref": "e1", "snapshot_version": "v", "text": "x"},
		"browser.press":  map[string]any{"tab_id": "t1", "key": "Enter"},
	}
	reads := map[string]any{
		"browser.open":     map[string]any{"url": "http://localhost:3000"},
		"browser.snapshot": map[string]any{"tab_id": "t1"},
		"browser.scroll":   map[string]any{"tab_id": "t1", "dy": 100},
		"browser.wait":     map[string]any{"tab_id": "t1", "ms": 10},
	}
	for _, mode := range []agent.Mode{agent.ModeAsk, agent.ModePlan, agent.ModeYolo} {
		if err := b.m.SetMode(b.tid, mode); err != nil {
			t.Fatal(err)
		}
		before := len(b.commands())
		for op, args := range acts {
			res := b.op(op, args)
			if refused := res.IsError && strings.Contains(res.Text, "plan_mode"); refused != (mode == agent.ModePlan) {
				t.Fatalf("%s in %s mode: %+v", op, mode, res)
			}
		}
		for op, args := range reads {
			if res := b.op(op, args); res.IsError {
				t.Fatalf("%s in %s mode: %+v", op, mode, res)
			}
		}
		want := len(reads)
		if mode != agent.ModePlan {
			want += len(acts)
		}
		if n := len(b.commands()) - before; n != want {
			t.Fatalf("%s mode sent %d commands, want %d", mode, n, want)
		}
	}
}
