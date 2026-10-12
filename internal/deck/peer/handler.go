package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// OpPeerStatus is the one op phase 1 answers: this Deck's name, version and
// uptime.
const OpPeerStatus = "peer.status"

// ErrUnknownOp is returned by a Handler for an op it does not know.
var ErrUnknownOp = errors.New("unknown op")

// Caller is the pinned peer on the other end of a connection, as this Deck
// knows it. Name and Fingerprint come from the pinned store, never from the
// peer, so a handler can trust them.
type Caller struct {
	Name        string
	Fingerprint string
	Role        string
	// AllowInbound is the ops this peer may run here (config
	// peers.remote[].allow_inbound); empty means no restriction beyond the
	// handler's own.
	AllowInbound []string
}

// allowed reports whether the caller may run op.
func (c Caller) allowed(op string) bool {
	if len(c.AllowInbound) == 0 {
		return true
	}
	for _, a := range c.AllowInbound {
		if a == op {
			return true
		}
	}
	return false
}

// Handler answers the two tools a peer can call. It returns the text the peer
// sees; an error becomes a tool result with isError set and the error's text.
type Handler interface {
	// Search answers the search tool; args is its arguments object.
	Search(ctx context.Context, from Caller, args json.RawMessage) (string, error)
	// Execute runs op with args for the execute tool.
	Execute(ctx context.Context, from Caller, op string, args json.RawMessage) (string, error)
}

// StatusHandler is the phase 1 handler: execute answers peer.status and
// nothing else, and search lists that op.
type StatusHandler struct {
	Name    string
	Version string
	Started time.Time
	Now     func() time.Time // nil means time.Now
}

// Search implements Handler.
func (h *StatusHandler) Search(_ context.Context, _ Caller, _ json.RawMessage) (string, error) {
	return OpPeerStatus + " {}: this Deck's name, version and uptime", nil
}

// Execute implements Handler.
func (h *StatusHandler) Execute(_ context.Context, _ Caller, op string, _ json.RawMessage) (string, error) {
	if op != OpPeerStatus {
		return "", fmt.Errorf("%w %q (call search to list ops)", ErrUnknownOp, op)
	}
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	up := now().Sub(h.Started).Truncate(time.Second)
	b, err := json.Marshal(map[string]any{
		"name":           h.Name,
		"version":        h.Version,
		"uptime":         up.String(),
		"uptime_seconds": int64(up / time.Second),
	})
	return string(b), err
}
