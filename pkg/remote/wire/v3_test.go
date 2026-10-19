package wire

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var updateV3 = flag.Bool("update-v3", false, "rewrite the v3 and relay fixtures from v3Samples")

const digest = "9f2c1a7e5b3d48c0a6e1f7d2b9c4a835e0d1f6b7c2a94e8d3b5f0a1c7e6d2b48"

// v3Samples is the source of the v3_* and relay_* fixtures. Run
// `go test ./pkg/remote/wire -update-v3` after editing; the files are then
// the shared surface with the phone client.
var v3Samples = map[string]string{
	"v3_frame_hello":   `{"t":"hello","v":3,"resume":{"epoch":"ep_c41d9e","seq":1204,"focus":{"project_id":"prj_a1","session_id":"ses_8f2a"}}}`,
	"v3_frame_welcome": `{"t":"welcome","epoch":"ep_c41d9e","replay":true,"device":{"id":"dev_7a1f","name":"Pixel 9","expires_at":"2027-01-12T09:30:00Z"},"server":{"version":"0.9.0","cid":"Zk3p0Jq8vYb1nT5cR2sWxA","name":"erioluwa-pc","projects":[{"id":"prj_a1","name":"deck","online":true},{"id":"prj_b2","name":"site","online":false}],"kinds":["claude","codex"],"allow_yolo":false,"features":["images","inbox","offline_queue","passkey","push_e2e"],"protocol":3,"tunnel":"","push_key":"BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM","theme":{"palette":{"accent":"#7dd3c0","bg_base":"#111416","fg_base":"#e6e8ea"},"kinds":{"claude":{"mark":"✻","tint":"#d97757"}}}}}`,
	"v3_frame_req":     `{"t":"req","id":"7","m":"chat.send","pj":"prj_a1","p":{"session_id":"ses_8f2a","text":"run the tests"}}`,
	"v3_frame_res_err": `{"t":"res","id":"8","ok":false,"err":{"code":"assertion_required","msg":"This approval needs your fingerprint or face."}}`,
	"v3_frame_ev":      `{"t":"ev","seq":1205,"e":"busy","pj":"prj_a1","p":{"session_id":"ses_8f2a","busy":true}}`,

	"v3_entity_inbox_item":      `{"pj":"prj_a1","session_id":"ses_8f2a","title":"Fix flaky queue test","state":"needs_you","approvals":2,"last_activity":"2026-10-14T09:41:00Z","summary":"Waiting to run: rm -rf build","unread":true}`,
	"v3_entity_approval":        `{"id":"apr_51e9","session_id":"ses_8f2a","agent":"claude-1","kind":"command","description":"Run a command","command":"git push --force origin main","created_at":"2026-10-14T09:40:40Z","tier":"destructive","digest":"` + digest + `"}`,
	"v3_entity_assertion":       `{"credential_id":"AaBbCc_dEf","authenticator_data":"SZYN5YgOjGh0NBcPZHZgW4_krrmihjLHmVzzuoMdl2MFAAAAAQ","client_data_json":"eyJ0eXBlIjoid2ViYXV0aG4uZ2V0In0","signature":"MEUCIQDx1y2z"}`,
	"v3_entity_queued_action":   `{"qid":"0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a10","device_id":"dev_7a1f","created_at":"2026-10-14T21:05:00Z","expires_at":"2026-10-15T05:05:00Z","pj":"prj_a1","session_id":"ses_8f2a","kind":"chat.send","payload":{"text":"continue with the migration"}}`,
	"v3_entity_queued_approval": `{"qid":"0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a11","device_id":"dev_7a1f","created_at":"2026-10-14T21:06:00Z","expires_at":"2026-10-14T22:06:00Z","pj":"prj_a1","session_id":"ses_8f2a","kind":"approvals.answer","payload":{"decision":"allow"},"precond":{"approval_id":"apr_51e9","digest":"` + digest + `"},"assertion":{"credential_id":"AaBbCc_dEf","authenticator_data":"SZYN5YgOjGh0NBcPZHZgW4_krrmihjLHmVzzuoMdl2MFAAAAAQ","client_data_json":"eyJ0eXBlIjoid2ViYXV0aG4uZ2V0In0","signature":"MEUCIQDx1y2z"}}`,
	"v3_entity_receipt":         `{"qid":"0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a11","status":"dropped","reason":"digest_mismatch","at":"2026-10-15T07:00:12Z"}`,
	"v3_entity_snapshot":        `{"at":"2026-10-14T21:04:50Z","inbox":[{"pj":"prj_a1","session_id":"ses_8f2a","title":"Fix flaky queue test","state":"needs_you","approvals":1,"last_activity":"2026-10-14T21:04:00Z","summary":"Waiting to run: go test ./...","unread":false}],"approvals":[{"pj":"prj_a1","approval":{"id":"apr_51e9","session_id":"ses_8f2a","agent":"claude-1","kind":"command","description":"Run a command","command":"go test ./...","created_at":"2026-10-14T21:04:00Z","tier":"normal","digest":"` + digest + `"}}],"receipts":[]}`,
	"v3_entity_qr_blob":         `{"v":1,"cid":"Zk3p0Jq8vYb1nT5cR2sWxA","relay":"https://app.example.dev","s":"3q2-7w9tYl0mQeXy1c8dKpR5uVnB4aZs6HfG0jTiOWc","psk":"vJ0b8xkP2mQ7sT1dN4yR9uLwE3cZ6aGhF5iOtXqKnYs","exp":1792006200,"name":"erioluwa-pc","lan":["192.168.1.20:7879"],"fp":"4b:1e:aa:09"}`,
	"v3_pair_hello":             `{"device_name":"Pixel 9"}`,
	"v3_pair_welcome":           `{"device_id":"dev_7a1f","relay_token":"rt_Qm9xT2nL7vYb3cR8sWk1dA5uHf0jEiZp4gNtXo6yCaM","expires_at":"2027-01-12T09:30:00Z","name":"erioluwa-pc"}`,

	"v3_req_inbox.ack":        `{"pj":"prj_a1","session_id":"ses_8f2a"}`,
	"v3_req_approvals.answer": `{"session_id":"ses_8f2a","id":"apr_51e9","decision":"deny","reason":"use --force-with-lease instead"}`,
	"v3_req_mode.set":         `{"session_id":"ses_8f2a","mode":"yolo","assertion":{"credential_id":"AaBbCc_dEf","authenticator_data":"SZYN5YgOjGh0NBcPZHZgW4_krrmihjLHmVzzuoMdl2MFAAAAAQ","client_data_json":"eyJ0eXBlIjoid2ViYXV0aG4uZ2V0In0","signature":"MEUCIQDx1y2z"}}`,
	"v3_req_changes.get":      `{"session_id":"ses_8f2a","summary":true}`,
	"v3_res_passkey.begin":    `{"challenge":"Zm9vYmFyYmF6cXV4Zm9vYmFyYmF6cXV4Zm9vYmFyYmE","rp_id":"app.example.dev","user_id":"ZGV2XzdhMWY"}`,
	"v3_req_passkey.finish":   `{"credential_id":"AaBbCc_dEf","client_data_json":"eyJ0eXBlIjoid2ViYXV0aG4uY3JlYXRlIn0","attestation_object":"o2NmbXRkbm9uZWdhdHRTdG10oGhhdXRoRGF0YQ"}`,
	"v3_res_passkey.list":     `[{"id":"AaBbCc_dEf","created_at":"2026-10-14T09:00:00Z"}]`,
	"v3_req_assert.challenge": `{"action_digest":"` + digest + `"}`,
	"v3_res_assert.challenge": `{"nonce":"n_5c1d8e2f6a10b7"}`,
	"v3_req_push.register":    `{"p256dh":"BMKdE4mQ1p0aVxT7sY2uLwR9nC3gHfZ8jO5iNbA6kXe_tDq1vYlPc0rWmSz4hGuJo2xEaTn7bFiKyC9dLsRw3Qk","auth":"c0VjUm9vdHNlY3JldDE","approvals":true,"done":true}`,
	"v3_res_projects.list":    `[{"id":"prj_a1","name":"deck","online":true},{"id":"prj_b2","name":"site","online":false}]`,
	"v3_res_inbox.get":        `[{"pj":"prj_a1","session_id":"ses_8f2a","title":"Fix flaky queue test","state":"running","approvals":0,"last_activity":"2026-10-14T09:41:00Z","summary":"Editing queue_test.go","unread":false}]`,
	"v3_res_queue.status":     `[{"qid":"0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a10","status":"applied","at":"2026-10-15T07:00:10Z"}]`,
	"v3_event_inbox.changed":  `[{"pj":"prj_a1","session_id":"ses_8f2a","title":"Fix flaky queue test","state":"done","approvals":0,"last_activity":"2026-10-14T09:50:00Z","summary":"Tests pass.","unread":true}]`,

	"v3_push_approval": `{"kind":"approval","pj":"prj_a1","session_id":"ses_8f2a","title":"deck","body":"Run: go test ./...","approval_id":"apr_51e9","digest":"` + digest + `","tier":"normal","at":"2026-10-14T21:04:00Z"}`,
	"v3_push_done":     `{"kind":"done","pj":"prj_a1","session_id":"ses_8f2a","title":"deck","body":"Fix flaky queue test finished","at":"2026-10-14T21:09:00Z"}`,

	"relay_host_challenge":    `{"t":"challenge","nonce":"Fq8Yb2nT5cR0sWxAZk3p0J"}`,
	"relay_host_auth":         `{"t":"auth","sig":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gISIjJCUmJygpKissLS4vMDEyMzQ1Njc4OTo7PD0-Pw"}`,
	"relay_host_ready":        `{"t":"ready"}`,
	"relay_host_devices":      `{"t":"devices","hashes":["h_1a2b3c4d5e6f","h_9f8e7d6c5b4a"]}`,
	"relay_host_pair_open":    `{"t":"pair_open","pair_id_hash":"ph_7c1d8e2f6a10b7e3","exp":1792006200}`,
	"relay_host_open":         `{"t":"open","lane":"l_01","device":"h_1a2b3c4d5e6f"}`,
	"relay_host_data":         `{"t":"data","lane":"l_01","b64":"AQIDBAUGBwgJCgsMDQ4PEA"}`,
	"relay_host_close":        `{"t":"close","lane":"l_01","code":4403}`,
	"relay_host_mail":         `{"t":"mail","id":"0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a10","device":"h_1a2b3c4d5e6f","b64":"AQIDBAUGBwgJCgsMDQ4PEA"}`,
	"relay_host_ack":          `{"t":"ack","id":"0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a10"}`,
	"relay_host_snap":         `{"t":"snap","device_hash":"h_1a2b3c4d5e6f","b64":"AQIDBAUGBwgJCgsMDQ4PEA"}`,
	"relay_host_push":         `{"t":"push","device_hash":"h_1a2b3c4d5e6f","b64":"AQIDBAUGBwgJCgsMDQ4PEA","ttl":3600,"urgency":"high"}`,
	"relay_register_request":  `{"relay_pub":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"}`,
	"relay_register_response": `{"cid":"Zk3p0Jq8vYb1nT5cR2sWxA"}`,
	"relay_phone_auth":        `{"token":"rt_Qm9xT2nL7vYb3cR8sWk1dA5uHf0jEiZp4gNtXo6yCaM"}`,
	"relay_pair_open":         `{"pair_id":"pid_Xo6yCaMQm9xT2nL7vYb3cR8s"}`,
	"relay_mail_post":         `{"id":"0192f3a0-7b1c-7d2e-9a44-5c1d8e2f6a10","expires_at":"2026-10-15T05:05:00Z","b64":"AQIDBAUGBwgJCgsMDQ4PEA"}`,
	"relay_snap_response":     `{"b64":"AQIDBAUGBwgJCgsMDQ4PEA","computer":{"online":false,"last_seen":"2026-10-14T21:04:55Z"}}`,
	"relay_push_put":          `{"endpoint":"https://fcm.googleapis.com/fcm/send/dLhYb3cR8sWk1dA5uHf0jEiZp4"}`,
}

func TestV3Fixtures(t *testing.T) {
	all := append(FixturesV3(), FixturesRelay()...)
	if *updateV3 {
		for name, src := range v3Samples {
			raw := pretty(t, []byte(src))
			if err := os.WriteFile(filepath.Join("testdata", name+".json"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[string]bool{}
	for _, f := range all {
		seen[f.Name] = true
		if _, ok := v3Samples[f.Name]; !ok {
			t.Errorf("%s is registered but has no sample", f.Name)
		}
		t.Run(f.Name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", f.Name+".json"))
			if err != nil {
				t.Fatalf("missing fixture (run with -update-v3): %v", err)
			}
			raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
			if want := pretty(t, raw); !bytes.Equal(raw, want) {
				t.Errorf("not in canonical pretty form")
			}
			ptr := reflect.New(reflect.TypeOf(f.Zero))
			if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
				t.Fatalf("decode into %T: %v", f.Zero, err)
			}
			got, err := json.Marshal(ptr.Elem().Interface())
			if err != nil {
				t.Fatal(err)
			}
			if want, got := canonical(t, raw), canonical(t, got); !bytes.Equal(want, got) {
				t.Errorf("round trip through %T is not byte-equal:\nwant %s\ngot  %s", f.Zero, want, got)
			}
		})
	}
	for name := range v3Samples {
		if !seen[name] {
			t.Errorf("sample %s is not registered", name)
		}
	}
}

func TestQueuedActionValidate(t *testing.T) {
	now := time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)
	ok := func(mut func(*QueuedAction)) QueuedAction {
		a := QueuedAction{QID: "q", DeviceID: "d", CreatedAt: now, ExpiresAt: now.Add(time.Hour), PJ: "p", SessionID: "s", Kind: QKindChatSend}
		mut(&a)
		return a
	}
	cases := []struct {
		name string
		a    QueuedAction
		ok   bool
	}{
		{"chat.send", ok(func(*QueuedAction) {}), true},
		{"continue", ok(func(a *QueuedAction) { a.Kind = QKindChatContinue }), true},
		{"models.set", ok(func(a *QueuedAction) { a.Kind = QKindModelsSet }), true},
		{"session.new needs no session", ok(func(a *QueuedAction) { a.Kind = QKindSessionNew; a.SessionID = "" }), true},
		{"cancel", ok(func(a *QueuedAction) { a.Kind = QKindCancel; a.PJ, a.SessionID = "", "" }), true},
		{"approval bound", ok(func(a *QueuedAction) { a.Kind = QKindApproval; a.Precond = &Precond{ApprovalID: "a", Digest: digest} }), true},
		{"approval unbound", ok(func(a *QueuedAction) { a.Kind = QKindApproval }), false},
		{"interrupt never queues", ok(func(a *QueuedAction) { a.Kind = "agent.interrupt" }), false},
		{"yolo never queues", ok(func(a *QueuedAction) { a.Kind = "mode.set" }), false},
		{"cancel-chat never queues", ok(func(a *QueuedAction) { a.Kind = "chat.cancel" }), false},
		{"expiry over 24h", ok(func(a *QueuedAction) { a.ExpiresAt = now.Add(25 * time.Hour) }), false},
		{"expiry before creation", ok(func(a *QueuedAction) { a.ExpiresAt = now.Add(-time.Minute) }), false},
		{"no qid", ok(func(a *QueuedAction) { a.QID = "" }), false},
		{"chat.send without session", ok(func(a *QueuedAction) { a.SessionID = "" }), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.Validate()
			if c.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.ok {
				if e, isErr := err.(*Error); !isErr || e.Code != CodeInvalid {
					t.Fatalf("want *Error invalid, got %#v", err)
				}
			}
		})
	}
}

func TestHello3Validate(t *testing.T) {
	if err := (&Hello3{V: 3}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, h := range []*Hello3{nil, {V: 2}, {V: 0}} {
		if err := h.Validate(); err == nil {
			t.Errorf("%+v accepted", h)
		}
	}
}

func TestFrame3Shape(t *testing.T) {
	var f Frame3
	if err := json.Unmarshal([]byte(`{"t":"hello","v":3}`), &f); err != nil {
		t.Fatal(err)
	}
	if f.Hello3 == nil || f.Welcome3 != nil || f.Ev != nil {
		t.Errorf("hello decoded into the wrong groups: %+v", f)
	}
	b, _ := json.Marshal(Frame3{T: FrameReq, ID: "1", M: MInboxGet})
	for _, k := range []string{"seq", "v", "pj", "epoch"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("computer-level req carries %q: %s", k, b)
		}
	}
}

// TestPrefixesAreDisjoint keeps the v2 registry strict: a v3 or relay file
// must not be mistaken for an unregistered v2 fixture.
func TestPrefixesAreDisjoint(t *testing.T) {
	for _, f := range Fixtures() {
		if strings.HasPrefix(f.Name, "v3_") || strings.HasPrefix(f.Name, "relay_") || strings.HasPrefix(f.Name, "noise_") || strings.HasPrefix(f.Name, "assert_") || strings.HasPrefix(f.Name, "pair_") || strings.HasPrefix(f.Name, "hostauth_") || strings.HasPrefix(f.Name, "push_") {
			t.Errorf("v2 fixture %s uses a reserved prefix", f.Name)
		}
	}
}
