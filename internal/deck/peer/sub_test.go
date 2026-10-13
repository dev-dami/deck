package peer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dev-dami/deck/internal/deck/sub"
)

// subCapsule makes a store with one capsule changing a.txt.
func subCapsule(t *testing.T) (*sub.Store, sub.ID, sub.ID) {
	t.Helper()
	s, err := sub.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	p := filepath.Join(root, "a.txt")
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base, err := s.Snap(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := s.Capture(ctx, root, base, "", sub.Meta{})
	if err != nil || id == "" {
		t.Fatalf("capture: %v", err)
	}
	return s, id, base
}

func TestSubHandlerServesOnlyPublished(t *testing.T) {
	s, id, base := subCapsule(t)
	h := &SubHandler{Base: &StatusHandler{Name: "me"}, Store: func() *sub.Store { return s }}
	ctx := context.Background()
	exec := func(op string, args any) (string, error) {
		b, _ := json.Marshal(args)
		return h.Execute(ctx, Caller{Name: "peer"}, op, b)
	}

	if _, err := exec(OpSubClosure, map[string]any{"id": id}); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("closure of an unpublished capsule: %v", err)
	}
	if _, err := s.Publish(id); err != nil {
		t.Fatal(err)
	}
	out, err := exec(OpSubClosure, map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	var cl struct {
		IDs []sub.ID `json:"ids"`
	}
	if err := json.Unmarshal([]byte(out), &cl); err != nil || len(cl.IDs) == 0 {
		t.Fatalf("closure %q: %v", out, err)
	}
	// The base snapshot is in the store but not in the closure: refused.
	if _, err := exec(OpSubObjects, map[string]any{"ids": []sub.ID{base}}); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("serving the base snapshot: %v", err)
	}

	out, err = exec(OpSubObjects, map[string]any{"ids": cl.IDs})
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		N    int    `json:"n"`
		Pack string `json:"pack"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.N != len(cl.IDs) {
		t.Fatalf("objects: n=%d of %d: %v", r.N, len(cl.IDs), err)
	}
	raw, _ := base64.StdEncoding.DecodeString(r.Pack)
	far, _ := sub.Open(t.TempDir())
	if _, err := far.Unpack(bytes.NewReader(raw), 1<<20); err != nil {
		t.Fatal(err)
	}
	if len(far.Missing(cl.IDs)) != 0 {
		t.Fatal("the pack lacks objects")
	}

	// Other ops still reach the base handler, and search lists both.
	if _, err := exec(OpPeerStatus, nil); err != nil {
		t.Fatal(err)
	}
	ops, _ := h.Search(ctx, Caller{}, "")
	if len(ops) != 3 {
		t.Fatalf("search = %+v", ops)
	}
}
