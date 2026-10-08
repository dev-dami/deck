package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/agent"
)

func TestImageSenderAndSteererImplemented(t *testing.T) {
	r, _ := startFake(t, nil)
	if _, ok := r.(agent.ImageSender); !ok {
		t.Error("run is not an ImageSender")
	}
	if _, ok := r.(agent.Steerer); !ok {
		t.Error("run is not a Steerer")
	}
}

type wireMsg struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Source *struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
			} `json:"source"`
		} `json:"content"`
	} `json:"message"`
}

func TestBuildUserMessageImageShape(t *testing.T) {
	png, err := os.ReadFile(filepath.Join("testdata", "red.png"))
	if err != nil {
		t.Fatal(err)
	}
	wire, echo, err := buildUserMessage("what colour?", []agent.Image{
		{Path: filepath.Join("testdata", "red.png"), MediaType: "image/png"},
		{Path: filepath.Join("testdata", "red.png")}, // detected from content
	})
	if err != nil {
		t.Fatal(err)
	}
	var m wireMsg
	if err := json.Unmarshal(wire, &m); err != nil {
		t.Fatal(err)
	}
	c := m.Message.Content
	if m.Type != "user" || m.Message.Role != "user" || len(c) != 3 {
		t.Fatalf("message %s", wire)
	}
	for i := 0; i < 2; i++ {
		if c[i].Type != "image" || c[i].Source == nil || c[i].Source.Type != "base64" || c[i].Source.MediaType != "image/png" {
			t.Fatalf("block %d: %s", i, wire)
		}
		got, err := base64.StdEncoding.DecodeString(c[i].Source.Data)
		if err != nil || !bytes.Equal(got, png) {
			t.Fatalf("block %d data mismatch (%v)", i, err)
		}
	}
	if c[2].Type != "text" || c[2].Text != "what colour?" || c[2].Source != nil {
		t.Fatalf("text block %+v", c[2])
	}
	// exact key names of an image block: only "type" and "source"
	var raw struct {
		Message struct {
			Content []map[string]json.RawMessage `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(wire, &raw)
	if len(raw.Message.Content[0]) != 2 {
		t.Errorf("image block keys: %v", raw.Message.Content[0])
	}
	if bytes.Contains(echo, []byte(c[0].Source.Data)) || !bytes.Contains(echo, []byte("elided")) {
		t.Errorf("echo must not carry image data: %s", echo)
	}
}

func TestBuildUserMessageNoImagesUnchanged(t *testing.T) {
	wire, _, err := buildUserMessage("hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`
	if string(wire) != want {
		t.Errorf("wire %s\nwant %s", wire, want)
	}
	// images without a prompt: no empty text block
	wire, _, err = buildUserMessage("", []agent.Image{{Path: filepath.Join("testdata", "red.png")}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), `"type":"text"`) {
		t.Errorf("empty prompt must not produce a text block: %s", wire)
	}
}

func TestLoadImageValidation(t *testing.T) {
	dir := t.TempDir()
	png, _ := os.ReadFile(filepath.Join("testdata", "red.png"))
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	text := write("note.png", []byte("this is not an image"))
	empty := write("empty.png", nil)
	big := write("big.png", append(append([]byte(nil), png...), make([]byte, maxImageBytes)...))
	exact := make([]byte, maxImageBytes)
	copy(exact, png)
	atLimit := write("limit.png", exact)
	jpg := write("pic.bin", append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, make([]byte, 32)...))
	gif := write("pic.gif", append([]byte("GIF89a"), make([]byte, 32)...))
	webp := write("pic.webp", append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...))

	red := filepath.Join("testdata", "red.png")
	cases := []struct {
		name    string
		img     agent.Image
		wantMT  string
		wantErr string
	}{
		{"declared png", agent.Image{Path: red, MediaType: "image/png"}, "image/png", ""},
		{"detected png", agent.Image{Path: red}, "image/png", ""},
		{"detected jpeg", agent.Image{Path: jpg}, "image/jpeg", ""},
		{"detected gif", agent.Image{Path: gif}, "image/gif", ""},
		{"detected webp", agent.Image{Path: webp}, "image/webp", ""},
		{"image/jpg alias, content wins", agent.Image{Path: red, MediaType: "IMAGE/JPG"}, "image/png", ""},
		{"exactly 5 MB ok", agent.Image{Path: atLimit}, "image/png", ""},
		{"over 5 MB", agent.Image{Path: big}, "", "5 MB"},
		{"unsupported declared type", agent.Image{Path: red, MediaType: "image/bmp"}, "", "unsupported media type"},
		{"svg declared", agent.Image{Path: red, MediaType: "image/svg+xml"}, "", "unsupported media type"},
		{"not an image", agent.Image{Path: text, MediaType: "image/png"}, "", "not png, jpeg, gif or webp"},
		{"empty file", agent.Image{Path: empty}, "", "empty"},
		{"missing file", agent.Image{Path: filepath.Join(dir, "nope.png")}, "", "image:"},
		{"no path", agent.Image{}, "", "no path"},
		{"directory", agent.Image{Path: dir}, "", "not a regular file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := loadImage(c.img)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v want containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Source.MediaType != c.wantMT {
				t.Errorf("media type %q want %q", p.Source.MediaType, c.wantMT)
			}
		})
	}
}

func fakeStdin(t *testing.T, f string) []wireMsg {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []wireMsg
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m wireMsg
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad stdin line: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func TestSendWithImagesReachesClaude(t *testing.T) {
	stdinFile := filepath.Join(t.TempDir(), "stdin.jsonl")
	r, _ := startFake(t, func(s *agent.Spec) { s.Env["DECK_FAKE_STDIN_FILE"] = stdinFile })
	is := r.(agent.ImageSender)
	img := agent.Image{Path: filepath.Join("testdata", "red.png"), MediaType: "image/png"}
	if err := is.SendWithImages("what colour is this?", []agent.Image{img}); err != nil {
		t.Fatal(err)
	}
	evs := waitKind(t, r, agent.KindTurnDone)
	if evs[0].Kind != agent.KindUserText || evs[0].Text != "what colour is this?" {
		t.Errorf("echo must carry the prompt only: %+v", evs[0])
	}
	msgs := fakeStdin(t, stdinFile)
	if len(msgs) != 1 || len(msgs[0].Message.Content) != 2 ||
		msgs[0].Message.Content[0].Type != "image" || msgs[0].Message.Content[1].Type != "text" {
		t.Fatalf("stdin message %+v", msgs)
	}
	if len(evs[0].Raw) == 0 || bytes.Contains(evs[0].Raw, []byte(msgs[0].Message.Content[0].Source.Data)) {
		t.Errorf("echo Raw must not hold the image data")
	}

	// a bad image fails before anything is written or echoed
	err := is.SendWithImages("x", []agent.Image{{Path: filepath.Join(t.TempDir(), "missing.png")}})
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	if got := fakeStdin(t, stdinFile); len(got) != 1 {
		t.Errorf("failed send wrote to claude: %d messages", len(got))
	}
}

// ---- steering ----

func TestSteerIdleIsPlainSend(t *testing.T) {
	stdinFile := filepath.Join(t.TempDir(), "stdin.jsonl")
	r, argsFile := startFake(t, func(s *agent.Spec) { s.Env["DECK_FAKE_STDIN_FILE"] = stdinFile })
	if err := r.(agent.Steerer).Steer("go left", nil); err != nil {
		t.Fatal(err)
	}
	evs := waitKind(t, r, agent.KindTurnDone)
	for _, e := range evs {
		if e.Kind == agent.KindTurnDone && e.Text == "interrupted" {
			t.Fatal("idle steer must not interrupt")
		}
	}
	if evs[0].Kind != agent.KindUserText || evs[0].Text != "go left" {
		t.Errorf("first event %+v", evs[0])
	}
	if n := launchCount(t, argsFile); n != 1 {
		t.Errorf("launches = %d want 1", n)
	}
	if m := fakeStdin(t, stdinFile); len(m) != 1 || m[0].Message.Content[0].Text != "go left" {
		t.Errorf("stdin %+v", m)
	}
}

func TestSteerIdleWithImages(t *testing.T) {
	stdinFile := filepath.Join(t.TempDir(), "stdin.jsonl")
	r, _ := startFake(t, func(s *agent.Spec) { s.Env["DECK_FAKE_STDIN_FILE"] = stdinFile })
	img := agent.Image{Path: filepath.Join("testdata", "red.png")}
	if err := r.(agent.Steerer).Steer("look", []agent.Image{img}); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	m := fakeStdin(t, stdinFile)
	if len(m) != 1 || m[0].Message.Content[0].Type != "image" {
		t.Errorf("stdin %+v", m)
	}
}

func TestSteerBusyInterruptsThenDelivers(t *testing.T) {
	stdinFile := filepath.Join(t.TempDir(), "stdin.jsonl")
	r, argsFile := startFake(t, func(s *agent.Spec) { s.Env["DECK_FAKE_STDIN_FILE"] = stdinFile })
	if err := r.Send("first"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindTurnDone)
	sid := r.SessionID()
	if sid == "" {
		t.Fatal("no session id")
	}

	if err := r.Send("HANG please"); err != nil { // the turn never finishes by itself
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindUserText)
	if err := r.(agent.Steerer).Steer("actually do X", nil); err != nil {
		t.Fatal(err)
	}

	// Expect: TurnDone "interrupted", the steer echo, then the steer's own
	// turn finishing normally. No exit, and "interrupted" exactly once.
	var evs []agent.Event
	deadline := time.After(20 * time.Second)
	for done := 0; done < 2; {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatalf("events closed: %v", kinds(evs))
			}
			evs = append(evs, ev)
			if ev.Kind == agent.KindTurnDone {
				done++
			}
		case <-deadline:
			t.Fatalf("timeout; got %v", kinds(evs))
		}
	}
	interrupted, echoAt, firstDoneAt := 0, -1, -1
	for i, e := range evs {
		switch e.Kind {
		case agent.KindExited:
			t.Fatalf("steer must not report exit: %v", kinds(evs))
		case agent.KindTurnDone:
			if e.Text == "interrupted" {
				interrupted++
			}
			if firstDoneAt < 0 {
				firstDoneAt = i
			}
		case agent.KindUserText:
			if e.Text == "actually do X" {
				echoAt = i
			}
		}
	}
	if interrupted != 1 || evs[firstDoneAt].Text != "interrupted" {
		t.Errorf("interrupted TurnDone count = %d, first = %q: %v", interrupted, evs[firstDoneAt].Text, kinds(evs))
	}
	if echoAt < firstDoneAt {
		t.Errorf("steer prompt echoed before the interrupt completed: %v", kinds(evs))
	}

	all := launches(t, argsFile, 2)
	if len(all) != 2 {
		t.Fatalf("launches = %d want 2", len(all))
	}
	if v, _ := argAfter(all[1], "--resume"); v != sid {
		t.Errorf("restart resume = %q want %q", v, sid)
	}
	// The steer prompt went to the replacement process, once, after HANG.
	var texts []string
	for _, m := range fakeStdin(t, stdinFile) {
		texts = append(texts, m.Message.Content[0].Text)
	}
	if len(texts) != 3 || texts[0] != "first" || texts[1] != "HANG please" || texts[2] != "actually do X" {
		t.Errorf("stdin messages %v", texts)
	}
}

func TestSteerBadImageKeepsTurnRunning(t *testing.T) {
	r, argsFile := startFake(t, nil)
	if err := r.Send("HANG please"); err != nil {
		t.Fatal(err)
	}
	waitKind(t, r, agent.KindUserText)
	err := r.(agent.Steerer).Steer("x", []agent.Image{{Path: filepath.Join(t.TempDir(), "missing.png")}})
	if err == nil {
		t.Fatal("expected error")
	}
	launches(t, argsFile, 1) // the first process has started (and logged its args)
	if n := launchCount(t, argsFile); n != 1 {
		t.Errorf("bad steer restarted claude: %d launches", n)
	}
	// still interruptible: the turn is genuinely still running
	if err := r.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if evs := waitKind(t, r, agent.KindTurnDone); evs[len(evs)-1].Text != "interrupted" {
		t.Errorf("last %+v", evs[len(evs)-1])
	}
}

func TestSteerAfterClose(t *testing.T) {
	a, spec, _ := newFake(t)
	r, err := a.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if err := r.(agent.Steerer).Steer("x", nil); err == nil {
		t.Error("expected error on closed run")
	}
}
