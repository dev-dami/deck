package mobile

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/devstore"
	"github.com/dev-dami/deck/pkg/remote/e2e"
	"github.com/dev-dami/deck/pkg/remote/projects"
	"github.com/dev-dami/deck/pkg/remote/wire"
)

// A queued action for a project whose Deck is not running waits; that is what
// the command line lists and cancels.
func TestQueueListAndCancelFromTheCLI(t *testing.T) {
	e := start(t)
	kp, w := e.pair()
	ident := e2eIdent(t, e.dir)
	cid := e.status().CID

	reg, err := projects.Open(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	idle, err := reg.Add(t.TempDir(), "idle-project") // shared, but no Deck running
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the daemon to see the project", func() bool {
		for _, p := range e.status().Projects {
			if p.ID == idle.ID {
				return true
			}
		}
		return false
	})

	queue := func(id, text string) {
		t.Helper()
		qa := wire.QueuedAction{
			QID: id, DeviceID: w.DeviceID, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
			PJ: idle.ID, SessionID: "s1", Kind: wire.QKindChatSend, Payload: json.RawMessage(`{"text":"` + text + `"}`),
		}
		raw, _ := json.Marshal(qa)
		sealed, err := e2e.SealK(kp, ident.StaticPublic(), cid, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.relay.Mail(id, devstore.HashToken(w.RelayToken), sealed)
		if ack, ok := e.relay.WaitFrame(wire.HostAck, 10*time.Second); !ok || ack.ID != id {
			t.Fatalf("mail %s not acked: %+v", id, ack)
		}
	}
	queue("q-one", "first thing")
	queue("q-two", "second thing")

	var items []QueueItem
	if err := e.control().Call(e.ctx, "queue.list", nil, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("listed %d, want 2: %+v", len(items), items)
	}
	for _, it := range items {
		if it.Device != "Pixel 9" || it.Project != "idle-project" || it.Kind != wire.QKindChatSend || it.Text == "" {
			t.Fatalf("item %+v", it)
		}
	}

	var n int
	if err := e.control().Call(e.ctx, "queue.cancel", QueueCancelArgs{QID: "q-one"}, &n); err != nil || n != 1 {
		t.Fatalf("cancel one: %d %v", n, err)
	}
	if err := e.control().Call(e.ctx, "queue.cancel", QueueCancelArgs{QID: "q-one"}, &n); err == nil {
		t.Fatal("cancelling twice succeeded")
	}
	if err := e.control().Call(e.ctx, "queue.cancel", QueueCancelArgs{}, &n); err == nil {
		t.Fatal("cancel with no target succeeded")
	}
	if err := e.control().Call(e.ctx, "queue.cancel", QueueCancelArgs{All: true}, &n); err != nil || n != 1 {
		t.Fatalf("cancel all: %d %v", n, err)
	}
	items = nil
	if err := e.control().Call(e.ctx, "queue.list", nil, &items); err != nil || len(items) != 0 {
		t.Fatalf("after cancelling: %v %v", items, err)
	}

	// The phone can see what happened.
	p, _ := e.connect(kp, w, "lane-q")
	rs := p.call("", wire.MQueueStatus, struct{}{})
	var receipts []wire.Receipt
	if json.Unmarshal(rs.R, &receipts) != nil || len(receipts) != 2 {
		t.Fatalf("receipts %+v", rs)
	}
	for _, r := range receipts {
		if r.Status != wire.ReceiptDropped || r.Reason != wire.ReasonCancelled {
			t.Fatalf("receipt %+v", r)
		}
	}
}
