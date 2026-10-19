package relayclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dev-dami/deck/pkg/remote/identity"
	"github.com/gorilla/websocket"
)

func TestRegisterAfterUnknownComputerWebSocketClose(t *testing.T) {
	id, err := identity.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var registrations atomic.Int32
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/r/v1/register" {
			registrations.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"cid": id.CID()})
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4404, "unknown"), time.Now().Add(time.Second))
	}))
	defer srv.Close()
	c := New(Config{Identity: id, URL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.session(ctx, nil); err == nil {
		t.Fatal("unknown connection unexpectedly succeeded")
	}
	if registrations.Load() != 1 {
		t.Fatalf("registration requests = %d", registrations.Load())
	}
}
