package rpc

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/dev-dami/deck/pkg/remote/wire"
	"github.com/dev-dami/deck/pkg/rpc/deckv1"
)

func TestBrowserContractRoundTrip(t *testing.T) {
	want := &deckv1.Event{SessionId: "room", Kind: &deckv1.Event_BrowserRequest{BrowserRequest: &deckv1.BrowserRequest{
		Id: "request", SessionId: "room", ControllerId: "controller", ControllerGeneration: "generation",
		Owner: "codex", Command: &deckv1.BrowserCommand{Action: "click", TabId: "tab", ElementRef: "e1", SnapshotVersion: "v1"},
	}}}
	b, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got deckv1.Event
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(want, &got) {
		t.Fatal("controller or element version lost over protobuf")
	}
	im := &deckv1.OpResult{Images: []*deckv1.OpImage{{MimeType: "image/png", Data: []byte{0, 255, 1, 0}}}}
	b, err = proto.Marshal(im)
	if err != nil {
		t.Fatal(err)
	}
	var gotImage deckv1.OpResult
	if err := proto.Unmarshal(b, &gotImage); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(im.Images[0].Data, gotImage.Images[0].Data) {
		t.Fatal("binary image changed over protobuf")
	}
}

func TestBrowserOriginApprovalConversion(t *testing.T) {
	got := toApproval(wire.Approval{ID: "approval", SessionID: "room", Kind: "browser_origin", Origin: "https://example.com"})
	if got.Kind != "browser_origin" || got.Origin != "https://example.com" {
		t.Fatal("origin approval lost its kind or origin")
	}
}
