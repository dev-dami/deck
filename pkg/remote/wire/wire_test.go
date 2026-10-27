package wire

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// canonical re-encodes JSON with sorted keys, no whitespace and no HTML
// escaping; numbers are kept verbatim.
func canonical(t *testing.T, raw []byte) []byte {
	t.Helper()
	return reencode(t, raw, "")
}

// pretty re-encodes JSON the way fixtures are written: sorted keys,
// 2-space indent, trailing newline.
func pretty(t *testing.T, raw []byte) []byte {
	t.Helper()
	return reencode(t, raw, "  ")
}

func reencode(t *testing.T, raw []byte, indent string) []byte {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if indent == "" {
		return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	}
	return buf.Bytes()
}

// TestFixtures decodes every registered fixture into its Go type, encodes it
// back and compares with the file after canonicalisation. A field the type
// does not declare is dropped on the way and fails the comparison, as does a
// required field the fixture leaves out.
func TestFixtures(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Fixtures() {
		if seen[f.Name] {
			t.Errorf("fixture %q registered twice", f.Name)
		}
		seen[f.Name] = true
		t.Run(f.Name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", f.Name+".json"))
			if err != nil {
				t.Fatalf("missing fixture: %v", err)
			}
			raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
			if want := pretty(t, raw); !bytes.Equal(raw, want) {
				t.Errorf("fixture is not in canonical pretty form (sorted keys, 2-space indent, trailing newline):\nwant %s\ngot  %s", want, raw)
			}
			ptr := reflect.New(reflect.TypeOf(f.Zero))
			if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
				t.Fatalf("decode into %T: %v", f.Zero, err)
			}
			got, err := json.Marshal(ptr.Elem().Interface())
			if err != nil {
				t.Fatalf("encode %T: %v", f.Zero, err)
			}
			if want, got := canonical(t, raw), canonical(t, got); !bytes.Equal(want, got) {
				t.Errorf("round trip through %T is not byte-equal:\nwant %s\ngot  %s", f.Zero, want, got)
			}
		})
	}

	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		// v3, relay and Noise fixtures are checked by their own tests.
		if strings.HasPrefix(name, "v3_") || strings.HasPrefix(name, "relay_") || strings.HasPrefix(name, "noise_") || strings.HasPrefix(name, "assert_") || strings.HasPrefix(name, "pair_") || strings.HasPrefix(name, "hostauth_") || strings.HasPrefix(name, "push_") {
			continue
		}
		if !seen[name] {
			t.Errorf("testdata/%s.json is not registered in Fixtures()", name)
		}
	}
}

// TestFixturesCoverProtocol checks that every declared frame type, method
// and event has a fixture.
func TestFixturesCoverProtocol(t *testing.T) {
	names := map[string]bool{}
	for _, f := range Fixtures() {
		names[f.Name] = true
	}
	for _, ft := range []string{FrameHello, FrameWelcome, FrameReq, FrameRes, FrameEv, FrameResync} {
		if !names["frame_"+ft] {
			t.Errorf("no fixture for frame %q", ft)
		}
	}
	for _, m := range []string{
		MSessionsList, MSessionsCreate, MSessionsRename, MSessionsDelete, MSessionsFocus,
		MMessagesList, MChatSend, MMessageEdit, MChatCancel, MChatContinue, MAgentInterrupt,
		MQueueList, MQueueSteer, MQueueCancel, MRosterGet, MRosterKinds, MRosterAdd, MModelsList, MModelsSet, MRaceStart,
		MModeGet, MModeSet, MApprovalsList, MApprovalsAnswer, MChangesGet,
		MDevicesList, MDevicesRevoke, MPushStatus, MPushSubscribe, MPushUnsubscribe, MPushTest,
	} {
		if !names["req_"+m] || !names["res_"+m] {
			t.Errorf("no params+result fixture pair for method %q", m)
		}
	}
	for _, e := range []string{
		EvSessionCreated, EvSessionUpdated, EvSessionDeleted, EvMessageCreated, EvMessageUpdated,
		EvMessageDeleted, EvApprovalRequested, EvApprovalResolved, EvBusy, EvQueueChanged,
		EvRosterChanged, EvModeChanged, EvNotice, EvThemeChanged, EvDevicePaired, EvDeviceRevoked,
	} {
		if !names["event_"+e] {
			t.Errorf("no fixture for event %q", e)
		}
	}
}

func TestFrameShape(t *testing.T) {
	// An event always writes seq, even zero; other frames never do.
	b, err := json.Marshal(Frame{T: FrameEv, Ev: &Ev{Seq: 0, E: EvBusy}, P: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"seq":0`)) {
		t.Errorf("ev frame lost seq: %s", b)
	}
	b, _ = json.Marshal(Frame{T: FrameReq, ID: "1", M: MSessionsList})
	for _, k := range []string{"seq", "v", "epoch", "replay"} {
		if bytes.Contains(b, []byte(`"`+k+`"`)) {
			t.Errorf("req frame carries %q: %s", k, b)
		}
	}
	// A failed response keeps ok:false; a welcome keeps replay:false.
	b, _ = json.Marshal(Frame{T: FrameRes, ID: "1", OK: new(bool), Err: &Error{Code: CodeNotFound, Msg: "no such session"}})
	if !bytes.Contains(b, []byte(`"ok":false`)) {
		t.Errorf("res frame lost ok:false: %s", b)
	}
	b, _ = json.Marshal(Frame{T: FrameWelcome, Welcome: &Welcome{Epoch: "e"}})
	if !bytes.Contains(b, []byte(`"replay":false`)) {
		t.Errorf("welcome frame lost replay:false: %s", b)
	}
	// Decoding allocates the group of the frame's type only.
	var f Frame
	if err := json.Unmarshal([]byte(`{"t":"hello","v":2,"token":"x"}`), &f); err != nil {
		t.Fatal(err)
	}
	if f.Hello == nil || f.Welcome != nil || f.Ev != nil {
		t.Errorf("hello decoded into the wrong groups: %+v", f)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		v    interface{ Validate() error }
		ok   bool
	}{
		{"hello v2", &Hello{V: 2, Token: "tok"}, true},
		{"hello v1", &Hello{V: 1, Token: "tok"}, false},
		{"hello no token", &Hello{V: 2}, false},
		{"hello nil", (*Hello)(nil), false},
		{"mode plan", ModeSetParams{SessionID: "s", Mode: ModePlan}, true},
		{"mode ask", ModeSetParams{SessionID: "s", Mode: ModeAsk}, true},
		{"mode yolo", ModeSetParams{SessionID: "s", Mode: ModeYolo}, true},
		{"mode other", ModeSetParams{SessionID: "s", Mode: "auto"}, false},
		{"mode empty", ModeSetParams{SessionID: "s"}, false},
		{"decision allow", ApprovalsAnswerParams{Decision: DecisionAllow}, true},
		{"decision allow_session", ApprovalsAnswerParams{Decision: DecisionAllowSession}, true},
		{"decision deny", ApprovalsAnswerParams{Decision: DecisionDeny}, true},
		{"decision other", ApprovalsAnswerParams{Decision: "yes"}, false},
		{"limit default", MessagesListParams{SessionID: "s"}, true},
		{"limit max", MessagesListParams{SessionID: "s", Limit: MaxPageLimit}, true},
		{"limit over", MessagesListParams{SessionID: "s", Limit: MaxPageLimit + 1}, false},
		{"limit negative", MessagesListParams{SessionID: "s", Limit: -1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.v.Validate()
			if c.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.ok {
				e, isErr := err.(*Error)
				if !isErr || e.Code != CodeInvalid {
					t.Fatalf("want *Error with code invalid, got %#v", err)
				}
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  string
		cut   bool
	}{
		{"", 0, "", false},
		{"abc", 3, "abc", false},
		{"abc", 10, "abc", false},
		{"abcdef", 3, "abc", true},
		{"abc", 0, "", true},
		{"abc", -1, "", true},
		// "é" is 2 bytes: a cut inside it backs up to the boundary.
		{"aé", 2, "a", true},
		{"aé", 3, "aé", false},
		// "✻" is 3 bytes.
		{"✻✻", 4, "✻", true},
		{"✻✻", 5, "✻", true},
		{"✻✻", 6, "✻✻", false},
		// A 4-byte rune.
		{"x😀", 2, "x", true},
		{"x😀", 4, "x", true},
	}
	for _, c := range cases {
		got, cut := Truncate(c.in, c.limit)
		if got != c.want || cut != c.cut {
			t.Errorf("Truncate(%q, %d) = %q, %v; want %q, %v", c.in, c.limit, got, cut, c.want, c.cut)
		}
	}
}
