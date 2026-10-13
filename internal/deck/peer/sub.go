package peer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dev-dami/deck/internal/deck/sub"
	"github.com/dev-dami/deck/internal/version"
)

// The Source Update Bridge between Decks: a peer fetches a capsule this Deck
// published (sub.capsule.publish) by asking for its closure, keeping the
// object ids it lacks, and pulling those in packs. Only objects in the
// closure of a published capsule are ever served, whatever ids a peer asks
// for, so a checkpoint or an unpublished capsule never leaves this machine.
const (
	OpSubClosure = "sub.closure"
	OpSubObjects = "sub.objects"
)

// subPackBudget bounds the object bytes one sub.objects reply carries, so
// the reply (zstd, then base64, in JSON) stays well inside a message.
const subPackBudget = 512 << 10

var subOps = []OpInfo{
	{Name: OpSubClosure, Args: "{id*}", Desc: "the object ids a published SUB capsule needs"},
	{Name: OpSubObjects, Args: "{ids*}", Desc: "a pack of SUB objects from published capsules"},
}

// SubHandler is Base plus the SUB ops, served from the store Store returns.
type SubHandler struct {
	Base  Handler
	Store func() *sub.Store
	// OnRequest, when set, hears every sub.closure a peer asks for a
	// capsule this Deck has: served when it was published and handed over,
	// not served when it is private, so the person can choose to share it.
	OnRequest func(from Caller, id sub.ID, served bool)
}

// Search implements Handler.
func (h *SubHandler) Search(ctx context.Context, from Caller, query string) ([]OpInfo, error) {
	ops, err := h.Base.Search(ctx, from, query)
	if err != nil {
		return nil, err
	}
	return append(ops, matchOps(subOps, query)...), nil
}

// Execute implements Handler.
func (h *SubHandler) Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error) {
	if op != OpSubClosure && op != OpSubObjects {
		return h.Base.Execute(ctx, from, op, args)
	}
	var s *sub.Store
	if h.Store != nil {
		s = h.Store()
	}
	if s == nil {
		return "", errors.New("no SUB store")
	}
	switch op {
	case OpSubClosure:
		var a struct {
			ID sub.ID `json:"id"`
		}
		if json.Unmarshal(args, &a) != nil || !a.ID.Valid() {
			return "", fmt.Errorf("%w: not a published capsule", ErrUnknownOp)
		}
		if !s.Shareable(a.ID) {
			// A private capsule this Deck has: the person hears of the request
			// and can share it. Any other id is just unknown.
			if _, err := s.Capsule(a.ID); err == nil && h.OnRequest != nil {
				h.OnRequest(from, a.ID, false)
				return "", fmt.Errorf("%w: capsule %s is private; its owner was asked to share it, try again once they have", ErrUnknownOp, a.ID.Short())
			}
			return "", fmt.Errorf("%w: not a published capsule", ErrUnknownOp)
		}
		if h.OnRequest != nil {
			if _, err := s.Capsule(a.ID); err == nil {
				h.OnRequest(from, a.ID, true)
			}
		}
		ids, err := s.Closure(a.ID)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(map[string]any{"ids": ids})
		return string(b), err
	default:
		var a struct {
			IDs []sub.ID `json:"ids"`
		}
		if json.Unmarshal(args, &a) != nil || len(a.IDs) == 0 {
			return "", errors.New("sub.objects needs ids")
		}
		var take []sub.ID
		size := 0
		for _, id := range a.IDs {
			if !id.Valid() || !s.Shareable(id) {
				return "", fmt.Errorf("%w: object not shared", ErrUnknownOp)
			}
			b, err := s.Get(id)
			if err != nil {
				return "", err
			}
			if len(take) > 0 && size+len(b) > subPackBudget {
				break
			}
			take = append(take, id)
			size += len(b)
		}
		var buf bytes.Buffer
		if err := s.Pack(&buf, take); err != nil {
			return "", err
		}
		b, err := json.Marshal(map[string]any{"n": len(take), "pack": base64.StdEncoding.EncodeToString(buf.Bytes())})
		return string(b), err
	}
}

// FetchCapsule pulls capsule id from the pinned peer name into s and returns
// how many objects were transferred: none when s has them all already.
func (s *Service) FetchCapsule(ctx context.Context, name string, id sub.ID, store *sub.Store) (int, error) {
	p, _ := s.loadPeers()
	return fetchCapsule(ctx, s.dir, name, SelfName(p), id, store)
}

func fetchCapsule(ctx context.Context, dir, name, self string, id sub.ID, store *sub.Store) (int, error) {
	pin, ok, err := OpenStore(dir).Get(name)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("no peer %q is paired", name)
	}
	if pin.Addr == "" {
		return 0, fmt.Errorf("peer %q has no address (it dials in)", name)
	}
	ident, err := LoadIdentity(dir)
	if err != nil {
		return 0, err
	}
	conn, err := Dial(ctx, DialOptions{
		Identity: ident, Peer: pin, Name: self, Version: version.Version,
		DialTimeout: 10 * time.Second, Limits: Limits{HandshakeTimeout: 10 * time.Second},
	})
	if err != nil {
		return 0, fmt.Errorf("fetch from %s: %s", name, Sanitize(err.Error()))
	}
	defer conn.Close()
	cl, err := conn.OpenMCP(ctx)
	if err != nil {
		return 0, err
	}
	defer cl.Close()
	if _, err := cl.Initialize(ctx); err != nil {
		return 0, fmt.Errorf("initialize: %w", err)
	}
	call := func(op string, args any, out any) error {
		r, err := cl.Execute(ctx, op, args)
		if err != nil {
			return err
		}
		if r.IsError {
			return fmt.Errorf("%s on %s: %s (is the capsule published, and %s allowed for this Deck?)", op, name, Sanitize(truncate(r.Text(), 200)), op)
		}
		return json.Unmarshal([]byte(r.Text()), out)
	}
	var cl1 struct {
		IDs []sub.ID `json:"ids"`
	}
	if err := call(OpSubClosure, map[string]any{"id": id}, &cl1); err != nil {
		return 0, err
	}
	want := store.Missing(cl1.IDs)
	got := 0
	for len(want) > 0 {
		var r struct {
			N    int    `json:"n"`
			Pack string `json:"pack"`
		}
		if err := call(OpSubObjects, map[string]any{"ids": want}, &r); err != nil {
			return got, err
		}
		raw, err := base64.StdEncoding.DecodeString(r.Pack)
		if err != nil || r.N <= 0 || r.N > len(want) {
			return got, fmt.Errorf("%s sent a bad pack", name)
		}
		ids, err := store.Unpack(bytes.NewReader(raw), 4*subPackBudget+sub.MaxObject)
		if err != nil {
			return got, err
		}
		got += len(ids)
		want = store.Missing(want[r.N:])
	}
	if _, err := store.Capsule(id); err != nil {
		return got, fmt.Errorf("%s did not send capsule %s", name, id.Short())
	}
	return got, nil
}
