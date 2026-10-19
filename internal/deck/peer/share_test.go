package peer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeShared struct{ posts []string }

func (f *fakeShared) List(string) []SharedThread { return nil }
func (f *fakeShared) Read(_ context.Context, peer, thread string, after int, wait time.Duration) (Mirror, error) {
	return Mirror{Thread: SharedThread{ID: thread, Title: peer}, Next: after + int(wait/time.Second)}, nil
}
func (f *fakeShared) Post(peer, thread, text string) error {
	f.posts = append(f.posts, peer+" "+thread+" "+text)
	return nil
}
func (f *fakeShared) Answer(string, string, string, bool) error {
	return errors.New("no such approval waits")
}

func TestShareHandler(t *testing.T) {
	f := &fakeShared{}
	h := &ShareHandler{Base: &StatusHandler{Name: "me"}, Shared: f}
	ctx := context.Background()
	run := func(from, op, args string) (string, error) {
		return h.Execute(ctx, Caller{Name: from}, op, json.RawMessage(args))
	}
	for range 2 { // the outbox, or a retry, sends it again
		if _, err := run("vps", OpThreadPost, `{"thread":"t1","text":"hi\u001b[2J","id":"k1"}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run("pc", OpThreadPost, `{"thread":"t1","text":"hi","id":"k1"}`); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.posts, "|") != "vps t1 hi[2J|pc t1 hi" {
		t.Fatalf("posts = %q", f.posts)
	}
	out, err := run("vps", OpThreadMirror, `{"thread":"t1","after":3,"wait":999}`)
	if err != nil || !strings.Contains(out, `"next":23`) || !strings.Contains(out, `"title":"vps"`) {
		t.Fatalf("mirror = %s, %v (the wait is capped at 20s)", out, err)
	}
	if _, err := run("vps", OpThreadMirror, `{}`); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("no thread: %v", err)
	}
	if _, err := run("vps", OpThreadAnswer, `{"thread":"t1","approval":"a1","allow":true}`); err == nil {
		t.Fatal("answered what does not wait")
	}

	mr := Mirror{Thread: SharedThread{Title: "x‮evil"}, Lines: []string{"a\u001b[31mb"}, Pending: []PendingApproval{{Summary: "rm\n-rf"}}}.clean()
	if mr.Thread.Title != "xevil" || mr.Lines[0] != "a[31mb" || mr.Pending[0].Summary != "rm -rf" {
		t.Fatalf("clean = %+v", mr)
	}
}
