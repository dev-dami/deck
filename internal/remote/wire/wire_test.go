package wire

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden fixtures")

func ptr[T any](v T) *T { return &v }

// fixtures are shared with the web client's tests, so a field renamed here
// breaks them rather than a phone in someone's pocket.
func fixtures() map[string]any {
	return map[string]any{
		"frame_hello": Frame{T: FrameHello, V: ProtocolVersion, Token: "dev1.secret", Resume: &Resume{Epoch: "e1", Seq: 4011}},
		"frame_welcome": Frame{T: FrameWelcome, Welcome: Welcome{
			Epoch:  "e1",
			Replay: true,
			Server: &ServerInfo{Version: "0.9.0", Project: "deck", Cwd: "/work/deck", Kinds: []string{"claude", "codex"}, AllowYolo: false, ProtocolV: ProtocolVersion},
			Device: &DeviceRef{ID: "dev1", Name: "Pixel"},
		}},
		"frame_req":   Frame{T: FrameReq, ID: "c17", M: MChatSend, P: json.RawMessage(`{"session_id":"s1","text":"hi"}`)},
		"frame_res":   Frame{T: FrameRes, ID: "c17", OK: ptr(true), R: json.RawMessage(`{}`)},
		"frame_err":   Frame{T: FrameRes, ID: "c18", OK: ptr(false), Err: &Error{Code: CodeYoloDisabled, Msg: "start deck serve with --allow-yolo"}},
		"frame_event": Frame{T: FrameEv, Seq: 4012, E: EvBusy, P: json.RawMessage(`{"session_id":"s1","busy":true}`)},
		"session":     Session{ID: "s1", Title: "Fix bug", UpdatedAt: 1760000000, Busy: true, Pending: 1, LastText: "done"},
		"message": Message{ID: "m1", SessionID: "s1", Role: RoleAssistant, Agent: "claude", CreatedAt: 1, UpdatedAt: 2, Finished: true, Parts: []Part{
			{Type: PartText, Text: "hello"},
			{Type: PartToolCall, ID: "t1", Name: "bash", Summary: "go test"},
			{Type: PartToolResult, ToolCallID: "t1", Name: "bash", Output: "ok", Truncated: true},
			{Type: PartImage, Name: "a.png", Mime: "image/png"},
		}},
		"approval_command":   Approval{ID: "p1", SessionID: "s1", Agent: "claude", Kind: ApprovalCommand, Description: "Run a command", Command: "rm -rf build", Cwd: "/work/deck", CreatedAt: 3},
		"approval_edit":      Approval{ID: "p2", SessionID: "s1", Kind: ApprovalEdit, Description: "Edit a file", Path: "a.go", Diff: "@@ -1 +1 @@\n-a\n+b\n", CreatedAt: 4},
		"queued_item":        QueuedItem{ID: "q1", Agent: "codex", Text: "next", At: 5},
		"agent_line":         AgentLine{Name: "claude", Kind: "claude", Model: "opus", Effort: "high", Busy: true},
		"model_choice":       ModelChoice{ID: "opus", Label: "Opus", Efforts: []string{"low", "high"}, Selected: true},
		"changed_file":       ChangedFile{Path: "a.go", Added: 1, Removed: 1, Diff: "@@", Truncated: false},
		"device":             Device{ID: "dev1", Name: "Pixel", CreatedAt: 1, LastSeen: 2, Current: true},
		"message_edit_req":   MessageEditReq{SessionID: "s1", MessageID: "m1", Text: "better"},
		"push_subscribe_req": PushSubscribeReq{Subscription: PushSubscription{Endpoint: "https://fcm.googleapis.com/fcm/send/x"}, Approvals: true, Done: true},
		"push_status":        PushStatus{Available: true, Enabled: true, Approvals: true},
		"message_editable":   Message{ID: "m2", SessionID: "s1", Role: RoleUser, Finished: true, Editable: true, Parts: []Part{{Type: PartText, Text: "hi"}}},
		"pair_request":       PairRequest{Code: "abc", DeviceName: "Pixel"},
		"pair_response":      PairResponse{Token: "dev1.secret", DeviceID: "dev1"},
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, v := range fixtures() {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", name+".json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing fixture (run go test -update): %v", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
				t.Fatalf("fixture drifted:\nwant %s\ngot  %s", want, got)
			}
		})
	}
}

func TestFrameRoundTrip(t *testing.T) {
	in := fixtures()["frame_welcome"].(Frame)
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Frame
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.T != FrameWelcome || out.Server == nil || out.Server.Project != "deck" || out.Device.ID != "dev1" || !out.Replay {
		t.Fatalf("round trip lost fields: %+v", out)
	}
}

func TestErrorFrameKeepsOKFalse(t *testing.T) {
	b, err := json.Marshal(fixtures()["frame_err"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"ok":false`)) {
		t.Fatalf("ok:false omitted: %s", b)
	}
}
