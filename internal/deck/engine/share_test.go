package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dev-dami/deck/internal/deck/agent/fake"
	"github.com/dev-dami/deck/internal/deck/peer"
	"github.com/dev-dami/deck/internal/deck/session"
)

// a shares a thread with b: b finds it in a's status, follows it, posts to
// it as participant and answers its approval as editor.
func TestSharedThreadBetweenTwoDecks(t *testing.T) {
	a := newDeck(t, "a", []fake.Script{
		turn(say("hello"), finish()),
		turn(asks("ap"), say("done it"), finish()),
	}, nil)
	b := newDeck(t, "b", nil, nil)
	link(t, a, b)
	tid, err := a.mgr.NewThread(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.mgr.AddMember(tid, "claude", ""); err != nil {
		t.Fatal(err)
	}
	_ = a.mgr.Post(tid, "$claude hi")
	ctx := context.Background()

	if _, err := b.svc.ReadShared(ctx, "a", tid, 0, 0); err == nil {
		t.Fatal("read a thread not shared")
	}
	if err := a.mgr.SetShare(tid, "b", session.ShareObserver); err != nil {
		t.Fatal(err)
	}
	status, err := b.svc.PeerStatus(ctx, "a")
	if err != nil || !strings.Contains(status, tid) || !strings.Contains(status, `"observer"`) {
		t.Fatalf("a's status = %s, %v", status, err)
	}
	var next int
	waitFor(t, "the reply in the mirror", func() bool {
		mr, err := b.svc.ReadShared(ctx, "a", tid, 0, 0)
		next = mr.Next
		return err == nil && strings.Contains(strings.Join(mr.Lines, "\n"), "hello")
	})
	if err := b.svc.PostShared(ctx, "a", tid, "please"); err == nil {
		t.Fatal("an observer posted")
	}

	if err := a.mgr.SetShare(tid, "b", session.ShareEditor); err != nil {
		t.Fatal(err)
	}
	if err := b.svc.PostShared(ctx, "a", tid, "run make please"); err != nil {
		t.Fatal(err)
	}
	mr := waitMirror(t, b, tid, &next, func(lines string, pending int) bool { return pending == 1 })
	if !strings.Contains(strings.Join(mr.Lines, "\n"), "[b] run make please") {
		t.Fatalf("lines = %q", mr.Lines)
	}
	if err := b.svc.AnswerShared(ctx, "a", tid, mr.Pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	waitMirror(t, b, tid, &next, func(lines string, _ int) bool { return strings.Contains(lines, "done it") })
}

// waitMirror follows tid from *next until ok holds for what came, and
// returns it: all the lines, and the approvals of the last read.
func waitMirror(t *testing.T, b *deck, tid string, next *int, ok func(lines string, pending int) bool) peer.Mirror {
	t.Helper()
	var all []string
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); {
		mr, err := b.svc.ReadShared(context.Background(), "a", tid, *next, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		*next = mr.Next
		all = append(all, mr.Lines...)
		mr.Lines = all
		if ok(strings.Join(all, "\n"), len(mr.Pending)) {
			return mr
		}
	}
	t.Fatalf("the mirror never showed it: %q", all)
	return peer.Mirror{}
}
